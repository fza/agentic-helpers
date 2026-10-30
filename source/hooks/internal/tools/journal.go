package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
)

// A tool folder's history, takes and pruned versions are journals of typed
// records (memorylog): one record per line, its body the line as JSON. A
// folder's journal supersedes the JSON-lines file an earlier release kept
// under the legacy name: a journal is only ever created holding every line
// that file held, which then goes.
type journal struct {
	name   string
	legacy string
	kind   memorylog.RecordKind
}

// lineSchema is a tool journal record's body: the line as JSON.
const lineSchema = 1

var (
	historyJournal = journal{name: "history.jnl", legacy: "history.jsonl", kind: memorylog.RecordHistory}
	takesJournal   = journal{name: "takes.jnl", legacy: "takes.jsonl", kind: memorylog.RecordTake}
	prunedJournal  = journal{name: "pruned.jnl", legacy: "pruned.jsonl", kind: memorylog.RecordPruned}
	journals       = []journal{historyJournal, takesJournal, prunedJournal}
)

// readJournal is every line of the folder's journal, read up to where the
// read stops, records of another kind skipped; a folder without one reads its
// legacy lines.
func readJournal[T any](folder *safefs.Dir, held journal) ([]T, error) {
	data, err := folder.ReadOptional(held.name, lineFileLimit)
	if err != nil {
		return nil, err
	}

	if len(data) == 0 {
		legacy, err := folder.ReadOptional(held.legacy, lineFileLimit)
		if err != nil {
			return nil, err
		}

		lines, _ := jsonLines[T](legacy)

		return lines, nil
	}

	records, _, err := memorylog.ReadJournal(data)
	if errors.Is(err, memorylog.ErrNotJournal) {
		return nil, fmt.Errorf("%s: %w", path.Join(folder.Path(), held.name), err)
	}

	var lines []T

	for _, record := range records {
		if record.Kind != held.kind || record.Schema != lineSchema {
			continue
		}

		var line T

		if json.Unmarshal(record.Body, &line) == nil {
			lines = append(lines, line)
		}
	}

	return lines, nil
}

func lineRecord(held journal, value any) (memorylog.Record, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return memorylog.Record{}, fmt.Errorf("encoding a line of %s: %w", held.name, err)
	}

	return memorylog.Record{Kind: held.kind, Schema: lineSchema, Body: body}, nil
}

// appendJournal appends value to the folder's journal; the caller holds the
// store lock. A journal a crash tore is cut where its read stops, and a
// folder still holding legacy lines gets them in its new journal first.
func appendJournal[T any](folder *safefs.Dir, held journal, values ...T) error {
	var framed []byte

	for _, value := range values {
		record, err := lineRecord(held, value)
		if err != nil {
			return err
		}

		framed = memorylog.AppendRecord(framed, record)
	}

	existing, err := folder.ReadOptional(held.name, lineFileLimit)
	if err != nil {
		return err
	}

	_, end, stopped := memorylog.ReadJournal(existing)
	if errors.Is(stopped, memorylog.ErrNotJournal) {
		return fmt.Errorf("%s: %w", path.Join(folder.Path(), held.name), stopped)
	}

	if len(existing) > 0 && stopped == nil {
		return appendBytes(folder, held.name, framed)
	}

	base := memorylog.JournalHeader()
	if end > len(base) {
		base = existing[:end]
	}

	if len(existing) == 0 {
		carried, err := legacyRecords[T](folder, held)
		if err != nil {
			return err
		}

		base = append(base, carried...)
	}

	err = folder.Replace(held.name, append(base, framed...), 0o600)
	if err != nil {
		return err
	}

	return removeLegacy(folder, held)
}

// legacyRecords is every line of the folder's legacy file, framed.
func legacyRecords[T any](folder *safefs.Dir, held journal) ([]byte, error) {
	legacy, err := folder.ReadOptional(held.legacy, lineFileLimit)
	if err != nil {
		return nil, err
	}

	lines, _ := jsonLines[T](legacy)

	var framed []byte

	for _, line := range lines {
		record, err := lineRecord(held, line)
		if err != nil {
			return nil, err
		}

		framed = memorylog.AppendRecord(framed, record)
	}

	return framed, nil
}

func removeLegacy(folder *safefs.Dir, held journal) error {
	err := folder.Remove(held.legacy)
	if err != nil && !isMissing(err) {
		return fmt.Errorf("removing %s: %w", path.Join(folder.Path(), held.legacy), err)
	}

	return nil
}

func appendBytes(folder *safefs.Dir, name string, data []byte) error {
	at := path.Join(folder.Path(), name)

	file, err := folder.Append(name, 0o600)
	if err != nil {
		return err
	}

	_, err = file.Write(data)
	closeErr := file.Close()

	if err != nil {
		return fmt.Errorf("appending to %s: %w", at, err)
	}

	if closeErr != nil {
		return fmt.Errorf("closing %s: %w", at, closeErr)
	}

	return nil
}

// convertJournals writes a journal for every legacy file of the folder that
// has none yet, keeping the legacy files: the store's migration removes them
// once its format is written.
func convertJournals(folder *safefs.Dir) error {
	for _, held := range journals {
		var (
			framed []byte
			err    error
		)

		switch held.kind {
		case memorylog.RecordHistory:
			framed, err = legacyRecords[historyLine](folder, held)
		case memorylog.RecordTake:
			framed, err = legacyRecords[takeLine](folder, held)
		default:
			framed, err = legacyRecords[prunedLine](folder, held)
		}

		if err != nil {
			return err
		}

		existing, err := folder.ReadOptional(held.name, lineFileLimit)
		if err != nil {
			return err
		}

		if len(framed) == 0 || len(existing) > 0 {
			continue
		}

		err = folder.Replace(held.name, append(memorylog.JournalHeader(), framed...), 0o600)
		if err != nil {
			return err
		}
	}

	return nil
}
