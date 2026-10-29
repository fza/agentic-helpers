package memorylog_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestAJournalReadsBackWhatWasFramed(t *testing.T) {
	written := []memorylog.Record{
		{Kind: memorylog.RecordHistory, Schema: 1, Body: []byte(`{"v":1}`)},
		{Kind: memorylog.RecordTake, Schema: 2, Body: nil},
	}

	read, end, err := memorylog.ReadJournal(memorylog.EncodeJournal(written...))
	if err != nil {
		t.Fatalf("reading the journal: %v", err)
	}

	if len(read) != len(written) || end != len(memorylog.EncodeJournal(written...)) {
		t.Fatal("every record should read back, up to the end")
	}

	for index := range written {
		if read[index].Kind != written[index].Kind || read[index].Schema != written[index].Schema ||
			string(read[index].Body) != string(written[index].Body) {
			t.Errorf("record %d should read back as framed", index)
		}
	}
}

func TestAJournalReadStops(t *testing.T) {
	first := memorylog.Record{Kind: memorylog.RecordHistory, Schema: 1, Body: []byte(`{"v":1}`)}
	second := memorylog.Record{Kind: memorylog.RecordHistory, Schema: 1, Body: []byte(`{"v":2}`)}
	whole := memorylog.EncodeJournal(first, second)
	secondAt := len(memorylog.EncodeJournal(first))

	flipped := slices.Clone(whole)
	flipped[len(flipped)-2] ^= 0x01

	lengthened := slices.Clone(whole)
	lengthened[secondAt] = 0xff

	cases := []struct {
		name string
		data []byte
		want error
	}{
		{name: "a record failing its checksum", data: flipped, want: memorylog.ErrRecordChecksum},
		{name: "a record cut short", data: whole[:len(whole)-3], want: memorylog.ErrTornRecord},
		{name: "a record head cut short", data: whole[:secondAt+5], want: memorylog.ErrTornRecord},
		{name: "a length running past the end", data: lengthened, want: memorylog.ErrTornRecord},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			read, end, err := memorylog.ReadJournal(tc.data)
			if !errors.Is(err, tc.want) {
				t.Fatalf("the read should stop and say why, got %v", err)
			}

			if len(read) != 1 || end != secondAt {
				t.Errorf("the read should stop at the failing record, keeping what came before, got %d records to %d",
					len(read), end)
			}
		})
	}
}

func TestAJournalOfAnotherFileIsRefused(t *testing.T) {
	_, _, err := memorylog.ReadJournal([]byte(`{"kind":"new"}` + "\n"))
	if !errors.Is(err, memorylog.ErrNotJournal) {
		t.Errorf("a file without the journal header should not read as a journal, got %v", err)
	}
}

func TestATailSkipsRecordsOfAnUnknownKind(t *testing.T) {
	dir := t.TempDir()

	before, err := memorylog.EventRecord(event(1, 0))
	if err != nil {
		t.Fatalf("framing: %v", err)
	}

	after, err := memorylog.EventRecord(event(2, 0))
	if err != nil {
		t.Fatalf("framing: %v", err)
	}

	unknown := memorylog.Record{Kind: 200, Schema: 1, Body: []byte("from a newer release")}
	newerSchema := memorylog.Record{Kind: memorylog.RecordEvent, Schema: 99, Body: []byte("a layout not known")}

	test.WriteFile(t, filepath.Join(dir, memorylog.TailName), string(memorylog.EncodeJournal(before, unknown, newerSchema, after)))

	read := readEvents(t, openLog(t, dir))
	if !sameEvents(read, []memorylog.Event{event(1, 0), event(2, 0)}) {
		t.Errorf("a reader should skip records of a kind or schema it does not know and read on, got %v", read)
	}
}

func TestATailFailingItsChecksumStopsTheReadAndIsCutByTheNextWrite(t *testing.T) {
	dir := t.TempDir()
	tail := journalOf(t, events(0, 3, 10))
	tail[len(tail)-2] ^= 0x01

	test.WriteFile(t, filepath.Join(dir, memorylog.TailName), string(tail))

	log := openLog(t, dir)
	if !sameEvents(readEvents(t, log), events(0, 2, 10)) {
		t.Fatal("a reader should stop at the record failing its checksum, keeping the ones before")
	}

	appendEvents(t, log, event(9, 10))

	if !sameEvents(readEvents(t, log), append(events(0, 2, 10), event(9, 10))) {
		t.Error("the next write should cut the failing record so its own append reads")
	}

	info, err := os.Stat(filepath.Join(dir, memorylog.TailName))
	if err != nil || info.Size() != int64(len(journalOf(t, append(events(0, 2, 10), event(9, 10))))) {
		t.Error("the tail should hold exactly the records before the cut and the append")
	}
}
