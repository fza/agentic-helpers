package dump_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/dump"
	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
	"github.com/fza/agentic-helpers/source/hooks/internal/space"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

var filed = time.Date(2026, 10, 29, 9, 30, 0, 0, time.UTC)

func dumpLines(t *testing.T, path string) []map[string]any {
	t.Helper()

	var out bytes.Buffer

	err := dump.Dump(context.Background(), &out, path)
	if err != nil {
		t.Fatalf("dumping %s: %v", filepath.Base(path), err)
	}

	var lines []map[string]any

	for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		var decoded map[string]any

		err = json.Unmarshal([]byte(line), &decoded)
		if err != nil {
			t.Fatalf("every line should be JSON, got %q: %v", line, err)
		}

		lines = append(lines, decoded)
	}

	return lines
}

func event(entry string, payload string) memorylog.Event {
	return memorylog.Event{Kind: memorylog.KindNew, Entry: entry, Time: filed, Seat: "s-main", Payload: []byte(payload)}
}

func TestEveryFileKindDumpsToJSONLines(t *testing.T) {
	dir := t.TempDir()

	slotFile := slots.Open(filepath.Join(dir, "sessions.slots"), 4096)
	slotFile.Now = func() time.Time { return filed }

	for _, pair := range [][2]string{{"shown/s1", `{"ids":["m-1@ab"]}`}, {"fork/s2", "plain bytes"}} {
		err := slotFile.Put(context.Background(), pair[0], []byte(pair[1]))
		if err != nil {
			t.Fatalf("writing a slot: %v", err)
		}
	}

	logDir := filepath.Join(dir, "log")

	log, err := memorylog.Open(logDir)
	if err != nil {
		t.Fatalf("opening the log: %v", err)
	}

	err = log.Append(context.Background(), event("m-000001", `{"title":"Gate reads Bash"}`))
	if err != nil {
		t.Fatalf("appending: %v", err)
	}

	built := filepath.Join(dir, "built")

	err = memorylog.Build(context.Background(), built, []memorylog.Event{event("m-000002", `{"title":"Sealed"}`)})
	if err != nil {
		t.Fatalf("building a log: %v", err)
	}

	segments, _ := filepath.Glob(filepath.Join(built, "*.seg"))

	history := filepath.Join(dir, "history.jnl")
	test.WriteFile(t, history, string(memorylog.EncodeJournal(
		memorylog.Record{Kind: memorylog.RecordHistory, Schema: 1, Body: []byte(`{"v":1,"by":"s1","at":"2026-10-29T09:30:00Z","why":"first"}`)},
		memorylog.Record{Kind: 77, Schema: 1, Body: []byte{0xff, 0x00}},
	)))

	component := filepath.Join(dir, "component")
	test.WriteFile(t, filepath.Join(component, "index.json"), `{"tools":{}}`)

	err = space.Refresh(context.Background(), component, nil)
	if err != nil {
		t.Fatalf("refreshing a ledger: %v", err)
	}

	cases := []struct {
		name  string
		path  string
		check func(t *testing.T, lines []map[string]any)
	}{
		{name: "slot file", path: slotFile.Path, check: func(t *testing.T, lines []map[string]any) {
			if len(lines) != 2 || lines[0]["key"] != "shown/s1" || lines[0]["written"] != "2026-10-29T09:30:00Z" {
				t.Fatalf("a slot file should print one line per key, got %v", lines)
			}

			if value, _ := lines[0]["value"].(map[string]any); value["ids"] == nil {
				t.Errorf("a JSON value should print as JSON, got %v", lines[0])
			}

			if lines[1]["value_base64"] != "cGxhaW4gYnl0ZXM=" {
				t.Errorf("any other value should print in base64, got %v", lines[1])
			}
		}},
		{name: "memory log tail", path: filepath.Join(logDir, memorylog.TailName), check: func(t *testing.T, lines []map[string]any) {
			payload, _ := lines[0]["payload"].(map[string]any)
			if len(lines) != 1 || lines[0]["record"] != "event" || lines[0]["event"] != "new" ||
				lines[0]["entry"] != "m-000001" || payload["title"] != "Gate reads Bash" {
				t.Errorf("a tail should print each event with its payload, got %v", lines)
			}
		}},
		{name: "sealed segment", path: segments[0], check: func(t *testing.T, lines []map[string]any) {
			if len(lines) != 1 || lines[0]["entry"] != "m-000002" || lines[0]["time"] != "2026-10-29T09:30:00Z" {
				t.Errorf("a segment should print each event, got %v", lines)
			}
		}},
		{name: "tool journal", path: history, check: func(t *testing.T, lines []map[string]any) {
			body, _ := lines[0]["body"].(map[string]any)
			if len(lines) != 2 || lines[0]["record"] != "history" || body["why"] != "first" {
				t.Errorf("a tool journal should print each record's body, got %v", lines)
			}

			if lines[1]["record"] != float64(77) || lines[1]["body_base64"] != "/wA=" {
				t.Errorf("a record of a kind this release does not know should print its kind and body, got %v", lines[1])
			}
		}},
		{name: "size ledger", path: filepath.Join(component, space.LedgerName), check: func(t *testing.T, lines []map[string]any) {
			if len(lines) != 1 || lines[0]["name"] != "index.json" || lines[0]["length"] != float64(12) ||
				lines[0]["crc32c"] == "" {
				t.Errorf("a ledger should print each file with its length and checksum, got %v", lines)
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.check(t, dumpLines(t, tc.path))
		})
	}
}

func TestADumpStoppingEarlyPrintsWhatCameBeforeAndSaysWhy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "takes.jnl")
	journal := memorylog.EncodeJournal(
		memorylog.Record{Kind: memorylog.RecordTake, Schema: 1, Body: []byte(`{"v":1}`)},
		memorylog.Record{Kind: memorylog.RecordTake, Schema: 1, Body: []byte(`{"v":2}`)},
	)
	test.WriteFile(t, path, string(journal[:len(journal)-2]))

	var out bytes.Buffer

	err := dump.Dump(context.Background(), &out, path)
	if !errors.Is(err, memorylog.ErrTornRecord) || strings.Count(out.String(), "\n") != 1 {
		t.Errorf("a torn journal should print the records before the tear and fail naming it, got %q, %v", out.String(), err)
	}
}

func TestAFileOfAnotherKindIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.json")

	err := os.WriteFile(path, []byte(`{"tools":{}}`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = dump.Dump(context.Background(), &bytes.Buffer{}, path)
	if !errors.Is(err, dump.ErrUnknownFile) {
		t.Errorf("a file kept as JSON should be refused, got %v", err)
	}
}
