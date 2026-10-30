package memorylog_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
)

var start = time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)

func event(number int, padding int) memorylog.Event {
	return memorylog.Event{
		Kind:    memorylog.KindNew,
		Entry:   fmt.Sprintf("m-%05d", number),
		Time:    start.Add(time.Duration(number) * time.Millisecond),
		Seat:    "drive",
		Payload: fmt.Appendf(nil, `{"n":%d,"pad":"%s"}`, number, strings.Repeat("x", padding)),
	}
}

func events(from int, count int, padding int) []memorylog.Event {
	made := make([]memorylog.Event, 0, count)
	for number := from; number < from+count; number++ {
		made = append(made, event(number, padding))
	}

	return made
}

func openLog(t *testing.T, dir string) *memorylog.Log {
	t.Helper()

	log, err := memorylog.Open(dir, dir)
	if err != nil {
		t.Fatalf("opening the log: %v", err)
	}

	return log
}

func appendEvents(t *testing.T, log *memorylog.Log, appended ...memorylog.Event) {
	t.Helper()

	err := log.Append(context.Background(), appended...)
	if err != nil {
		t.Fatalf("appending: %v", err)
	}
}

func readEvents(t *testing.T, log *memorylog.Log) []memorylog.Event {
	t.Helper()

	snapshot, err := log.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("taking a snapshot: %v", err)
	}
	defer snapshot.Close()

	read := []memorylog.Event{}

	for index, row := range snapshot.Rows() {
		payload, err := snapshot.Payload(index)
		if err != nil {
			t.Fatalf("reading payload %d: %v", index, err)
		}

		read = append(read, memorylog.Event{
			Kind: row.Kind, Entry: row.Entry, Time: row.Time, Seat: row.Seat, Target: row.Target, Payload: payload,
		})
	}

	return read
}

func sameEvents(left []memorylog.Event, right []memorylog.Event) bool {
	return slices.EqualFunc(left, right, func(one memorylog.Event, other memorylog.Event) bool {
		return one.Kind == other.Kind && one.Entry == other.Entry && one.Time.Equal(other.Time) &&
			one.Seat == other.Seat && one.Target == other.Target && bytes.Equal(one.Payload, other.Payload)
	})
}

type manifestFile struct {
	Format   int      `json:"format"`
	Segments []string `json:"segments"`
	Sealing  *struct {
		TailSHA256 string `json:"tail_sha256"`
		TailSize   int64  `json:"tail_size"`
	} `json:"sealing"`
}

func readManifest(t *testing.T, dir string) manifestFile {
	t.Helper()

	var listed manifestFile

	content, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("reading the manifest: %v", err)
	}

	err = json.Unmarshal(content, &listed)
	if err != nil {
		t.Fatalf("decoding the manifest: %v", err)
	}

	return listed
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	return content
}

func writeTree(t *testing.T, dir string, files map[string][]byte) {
	t.Helper()

	for name, content := range files {
		err := os.WriteFile(filepath.Join(dir, name), content, 0o600)
		if err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
}

func segmentFiles(t *testing.T, dir string) []string {
	t.Helper()

	found, err := filepath.Glob(filepath.Join(dir, "*.seg"))
	if err != nil {
		t.Fatalf("listing segments: %v", err)
	}

	names := make([]string, 0, len(found))
	for _, path := range found {
		names = append(names, filepath.Base(path))
	}

	return names
}

func journalOf(t *testing.T, held []memorylog.Event) []byte {
	t.Helper()

	records := make([]memorylog.Record, 0, len(held))

	for _, one := range held {
		record, err := memorylog.EventRecord(one)
		if err != nil {
			t.Fatalf("framing an event: %v", err)
		}

		records = append(records, record)
	}

	return memorylog.EncodeJournal(records...)
}
