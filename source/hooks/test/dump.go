package test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/dump"
	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
	"github.com/fza/agentic-helpers/source/hooks/internal/space"
)

// Dumped is the data file at path as `agentic-helpers dump` prints it, one
// JSON line per record or key; empty where the file does not exist.
func Dumped(t *testing.T, path string) string {
	t.Helper()

	_, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return ""
	}

	var out bytes.Buffer

	err = dump.Dump(context.Background(), &out, path)
	if err != nil {
		t.Fatalf("dumping %s: %v", path, err)
	}

	return out.String()
}

// EditTail rewrites the payload of every event in the journal tail at path
// through edit, each record framed afresh: a log forged as only a process
// writing the store directly could.
func EditTail(t *testing.T, path string, edit func(payload string) string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the tail: %v", err)
	}

	records, _, err := memorylog.ReadJournal(data)
	if err != nil {
		t.Fatalf("reading the tail: %v", err)
	}

	for index, record := range records {
		event, err := memorylog.DecodeEvent(record)
		if err != nil {
			t.Fatalf("decoding a tail event: %v", err)
		}

		event.Payload = []byte(edit(string(event.Payload)))

		records[index], err = memorylog.EventRecord(event)
		if err != nil {
			t.Fatalf("framing a tail event: %v", err)
		}
	}

	err = os.WriteFile(path, memorylog.EncodeJournal(records...), 0o600)
	if err != nil {
		t.Fatalf("writing the tail: %v", err)
	}
}

// WriteLedger writes the size ledger of the component at dir from a listing
// of its files as JSON: name, length, counted, held and bytes each.
func WriteLedger(t testing.TB, dir string, listing string) {
	t.Helper()

	var files []struct {
		Name    string `json:"name"`
		Length  int64  `json:"length"`
		Counted bool   `json:"counted"`
		Held    bool   `json:"held"`
		Bytes   int64  `json:"bytes"`
	}

	err := json.Unmarshal([]byte(listing), &files)
	if err != nil {
		t.Fatalf("reading the ledger listing: %v", err)
	}

	records := make([]memorylog.Record, 0, len(files))
	for _, file := range files {
		records = append(records, space.LedgerRecord(space.LedgerFile{
			Name: file.Name, Length: file.Length, Counted: file.Counted, Held: file.Held, Bytes: file.Bytes,
		}))
	}

	err = os.MkdirAll(dir, 0o700)
	if err == nil {
		err = os.WriteFile(filepath.Join(dir, space.LedgerName), memorylog.EncodeJournal(records...), 0o600)
	}

	if err != nil {
		t.Fatalf("writing the ledger: %v", err)
	}
}

// LedgerMismatches is every way the size ledger of the component at dir
// departs from the files below it: a data file it misses or lists at another
// length, or one it lists that is gone. Lock files, temp files and the ledger
// itself are no data files.
func LedgerMismatches(t testing.TB, dir string) []string {
	t.Helper()

	listed := map[string]space.LedgerFile{}
	for _, file := range space.ReadLedger(dir) {
		listed[file.Name] = file
	}

	var mismatches []string

	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return err
		}

		name := entry.Name()
		if strings.HasPrefix(name, space.LedgerName) || strings.HasSuffix(name, ".lock") || strings.HasPrefix(name, ".tmp") {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}

		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)

		file, found := listed[rel]

		switch {
		case !found:
			mismatches = append(mismatches, rel+" is not listed")
		case file.Length != info.Size():
			mismatches = append(mismatches, fmt.Sprintf("%s is listed at %d bytes, holds %d", rel, file.Length, info.Size()))
		case file.CRC != 0 && file.CRC != fileCRC(t, path):
			mismatches = append(mismatches, rel+" is listed with a checksum its bytes no longer have")
		}

		delete(listed, rel)

		return nil
	})
	if err != nil {
		t.Fatalf("listing %s: %v", dir, err)
	}

	for name := range listed {
		mismatches = append(mismatches, name+" is listed but gone")
	}

	return mismatches
}

func fileCRC(t testing.TB, path string) uint32 {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	return crc32.Checksum(data, crc32.MakeTable(crc32.Castagnoli))
}
