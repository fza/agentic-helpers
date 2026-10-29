// Package dump decodes every data file agentic-helpers keeps in a binary
// format to JSON lines, so nothing stored is unreadable to a person or a
// script: `agentic-helpers dump <file>`. A slot file prints one line per key
// it holds; a journal or a sealed segment one line per record.
package dump

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
	"github.com/fza/agentic-helpers/source/hooks/internal/space"
)

// readLimit bounds a file dump reads whole: a journal or a segment this
// large is no file agentic-helpers wrote.
const readLimit = 256 << 20

var ErrUnknownFile = errors.New("not a data file agentic-helpers keeps in a binary format")

// Slot is the line of one slot-file key. A value that is JSON prints as it
// is; any other prints in base64.
type Slot struct {
	Key         string          `json:"key"`
	Generation  uint64          `json:"generation"`
	Written     string          `json:"written"`
	Value       json.RawMessage `json:"value,omitempty"`
	ValueBase64 string          `json:"value_base64,omitempty"`
}

// Event is the line of a memory log event, from a journal or a segment; a
// segment's event has no record schema.
type Event struct {
	Record  string          `json:"record"`
	Schema  byte            `json:"schema,omitempty"`
	Event   string          `json:"event"`
	Entry   string          `json:"entry"`
	Time    string          `json:"time"`
	Seat    string          `json:"seat"`
	Target  string          `json:"target"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// LedgerFile is the line of one file a size ledger lists.
type LedgerFile struct {
	Record   string `json:"record"`
	Schema   byte   `json:"schema"`
	Name     string `json:"name"`
	Length   int64  `json:"length"`
	CRC      string `json:"crc32c"`
	Modified string `json:"modified"`
	Counted  bool   `json:"counted"`
	Held     bool   `json:"held"`
	Bytes    int64  `json:"bytes"`
}

// Body is the line of a record whose body is JSON, or of one whose kind this
// release does not know, its body then in base64.
type Body struct {
	Record     any             `json:"record"`
	Schema     byte            `json:"schema"`
	Body       json.RawMessage `json:"body,omitempty"`
	BodyBase64 string          `json:"body_base64,omitempty"`
}

// Dump writes the file at path to w as JSON lines. A journal whose read
// stops early prints every record before the stop, then fails naming it.
func Dump(ctx context.Context, w io.Writer, path string) error {
	err := ctx.Err()
	if err != nil {
		return fmt.Errorf("dumping %s: %w", path, err)
	}

	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening %s: %w", path, err)
	}

	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, readLimit))
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	out := bufio.NewWriter(w)
	encoder := json.NewEncoder(out)
	encoder.SetEscapeHTML(false)

	switch {
	case slots.IsSlotFile(data):
		err = dumpSlots(file, encoder)
	case memorylog.IsJournal(data):
		err = dumpJournal(data, encoder)
	case memorylog.IsSegment(data):
		err = dumpSegment(data, encoder)
	default:
		err = ErrUnknownFile
	}

	flushErr := out.Flush()
	if err == nil {
		err = flushErr
	}

	if err != nil {
		return fmt.Errorf("dumping %s: %w", path, err)
	}

	return nil
}

func stamp(at time.Time) string {
	return at.UTC().Format(time.RFC3339Nano)
}

func dumpSlots(file io.ReaderAt, encoder *json.Encoder) error {
	_, held, err := slots.Read(file)
	if err != nil {
		return err
	}

	for _, slot := range held {
		line := Slot{Key: slot.Key, Generation: slot.Generation, Written: stamp(slot.Written)}
		if json.Valid(slot.Value) {
			line.Value = slot.Value
		} else {
			line.ValueBase64 = base64.StdEncoding.EncodeToString(slot.Value)
		}

		err = encoder.Encode(line)
		if err != nil {
			return err
		}
	}

	return nil
}

func dumpJournal(data []byte, encoder *json.Encoder) error {
	records, _, stopped := memorylog.ReadJournal(data)

	for _, record := range records {
		err := encoder.Encode(recordLine(record))
		if err != nil {
			return err
		}
	}

	return stopped
}

func dumpSegment(data []byte, encoder *json.Encoder) error {
	events, err := memorylog.SegmentEvents(data)
	if err != nil {
		return err
	}

	for _, event := range events {
		err = encoder.Encode(eventLine(event, 0))
		if err != nil {
			return err
		}
	}

	return nil
}

func eventLine(event memorylog.Event, schema byte) Event {
	line := Event{
		Record: memorylog.RecordEvent.Name(), Schema: schema, Event: string(event.Kind), Entry: event.Entry,
		Time: stamp(event.Time), Seat: event.Seat, Target: event.Target,
	}

	if json.Valid(event.Payload) {
		line.Payload = event.Payload
	} else if len(event.Payload) > 0 {
		line.Payload, _ = json.Marshal(base64.StdEncoding.EncodeToString(event.Payload))
	}

	return line
}

func recordLine(record memorylog.Record) any {
	switch record.Kind {
	case memorylog.RecordEvent:
		event, err := memorylog.DecodeEvent(record)
		if err == nil {
			return eventLine(event, record.Schema)
		}
	case memorylog.RecordLedgerFile:
		file, err := space.DecodeLedgerFile(record)
		if err == nil {
			return LedgerFile{
				Record: record.Kind.Name(), Schema: record.Schema, Name: file.Name, Length: file.Length,
				CRC: fmt.Sprintf("%08x", file.CRC), Modified: stamp(file.Modified), Counted: file.Counted, Held: file.Held,
				Bytes: file.Bytes,
			}
		}
	}

	line := Body{Record: record.Kind.Name(), Schema: record.Schema}
	if line.Record == "" {
		line.Record = int(record.Kind)
	}

	if json.Valid(record.Body) && len(record.Body) > 0 {
		line.Body = record.Body
	} else {
		line.BodyBase64 = base64.StdEncoding.EncodeToString(record.Body)
	}

	return line
}
