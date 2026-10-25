package memorylog_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func dumpedEvents() []memorylog.Event {
	return []memorylog.Event{
		{Kind: memorylog.KindNew, Entry: "m-7q2k", Time: start, Seat: "drive", Payload: []byte(`{"title":"Seal at 64 KiB"}`)},
		{Kind: memorylog.KindTag, Entry: "m-7q2k", Time: start.Add(time.Minute), Seat: "drive", Target: "storage"},
		{
			Kind: memorylog.KindSupersede, Entry: "m-7q2k", Time: start.Add(2 * time.Minute), Seat: "reviewer",
			Target: "m-8r3x", Payload: []byte(`{"why":"<merged> & kept"}`),
		},
		{Kind: memorylog.KindMerge, Time: start.Add(3 * time.Minute), Seat: "drive"},
	}
}

func TestDump(t *testing.T) {
	dir := t.TempDir()
	log := openLog(t, dir)
	dumped := dumpedEvents()

	appendEvents(t, log, append(dumped, event(99, memorylog.SealBytes))...)

	dropFiller := func(rows []memorylog.Row, payload func(int) ([]byte, error)) ([]memorylog.Event, error) {
		kept := make([]memorylog.Event, 0, len(rows))

		for index, row := range rows[:len(rows)-1] {
			body, err := payload(index)
			if err != nil {
				return nil, err
			}

			kept = append(kept, memorylog.Event{
				Kind: row.Kind, Entry: row.Entry, Time: row.Time, Seat: row.Seat, Target: row.Target, Payload: body,
			})
		}

		return kept, nil
	}

	err := log.Merge(context.Background(), dropFiller)
	if err != nil {
		t.Fatalf("merging: %v", err)
	}

	appendEvents(t, log, dumped...)

	want := test.Fixture(t, "memorylog/dump.txt")

	for _, name := range []string{readManifest(t, dir).Segments[0], "tail.jsonl"} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer

			err := memorylog.Dump(context.Background(), &out, filepath.Join(dir, name))
			if err != nil {
				t.Fatalf("dumping: %v", err)
			}

			if out.String() != want {
				t.Errorf("the dump should print one readable line per event, got:\n%s", out.String())
			}
		})
	}
}

func TestDumpRefusesCorruptSegment(t *testing.T) {
	dir := t.TempDir()
	row := craftRow(craftedRow{entry: "m-1", offset: 5, length: 2})
	writeTree(t, dir, map[string][]byte{listedSegment: craftSegment("AGMSEG\x00", 1, 1, 0, len(row), row, []byte("{}"))})

	err := memorylog.Dump(context.Background(), &bytes.Buffer{}, filepath.Join(dir, listedSegment))
	if !errors.Is(err, memorylog.ErrCorrupt) {
		t.Error("dumping a corrupt segment should fail as corrupt")
	}
}
