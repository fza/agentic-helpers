package memorylog

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"time"
)

// A journal is the binary file every agentic-helpers journal is written as:
// the memory log's tail, the tool library's history, takes and pruned
// versions, and each component's size ledger. It opens with an 8-byte header,
// journalMagic and its format, and holds records one after another, each
//
//	uint32 length   kind, schema and body together, little-endian
//	uint32 crc32c   Castagnoli, over the length bytes, kind, schema and body
//	byte   kind     what the body holds; a reader skips a kind it does not know
//	byte   schema   the body's layout for its kind
//	body
//
// A record running past the end, or failing its checksum, stops the read: a
// write a crash tore leaves exactly that, and every record after it is
// unreachable, so a writer cuts the journal there before appending.
const (
	journalMagic  = "AGMJNL\x00"
	journalFormat = 1
	// JournalHeaderSize is the length of a journal's header.
	JournalHeaderSize = len(journalMagic) + 1
	recordHeadSize    = 10
	// recordLimit bounds one record's length, so a damaged length is read as
	// a torn record rather than an allocation.
	recordLimit = 64 << 20
)

// RecordKind is what a record's body holds. Every journal shares one numbering,
// so a dump names any record without knowing which file it came from. Only
// ever append.
type RecordKind byte

const (
	// RecordEvent is a memory log event.
	RecordEvent RecordKind = 1
	// RecordHistory is a version registered in the tool library.
	RecordHistory RecordKind = 2
	// RecordTake is a tool version a session took.
	RecordTake RecordKind = 3
	// RecordPruned is a tool version removed to make room.
	RecordPruned RecordKind = 4
	// RecordLedgerFile is one data file of a component's size ledger.
	RecordLedgerFile RecordKind = 5
)

// eventSchema is the body layout of a RecordEvent: the event kind's segment
// index, the time in Unix nanoseconds, entry, seat and target as
// length-prefixed strings, and the payload as the rest.
const eventSchema = 1

var (
	ErrNotJournal     = errors.New("not a journal")
	ErrTornRecord     = errors.New("torn record")
	ErrRecordChecksum = errors.New("record checksum mismatch")
)

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// Record is one typed record of a journal.
type Record struct {
	Kind   RecordKind
	Schema byte
	Body   []byte
}

// JournalHeader is the header every journal opens with.
func JournalHeader() []byte {
	return append([]byte(journalMagic), journalFormat)
}

// IsJournal is whether data opens with a journal's header.
func IsJournal(data []byte) bool {
	return bytes.HasPrefix(data, []byte(journalMagic))
}

// AppendRecord frames record onto dst.
func AppendRecord(dst []byte, record Record) []byte {
	start := len(dst)

	dst = binary.LittleEndian.AppendUint32(dst, uint32(len(record.Body)+2))
	dst = append(dst, 0, 0, 0, 0, byte(record.Kind), record.Schema)
	dst = append(dst, record.Body...)

	frame := dst[start:]
	sum := crc32.Update(crc32.Checksum(frame[:4], castagnoli), castagnoli, frame[8:])
	binary.LittleEndian.PutUint32(frame[4:8], sum)

	return dst
}

// EncodeJournal is a whole journal holding records.
func EncodeJournal(records ...Record) []byte {
	journal := JournalHeader()
	for _, record := range records {
		journal = AppendRecord(journal, record)
	}

	return journal
}

// ReadJournal reads a journal file: its records up to where the read stopped,
// the offset it stopped at, and why where it stopped before the end. An empty
// file is an empty journal; a header cut short reads as a torn record at 0.
func ReadJournal(data []byte) ([]Record, int, error) {
	if len(data) == 0 {
		return nil, 0, nil
	}

	if len(data) < JournalHeaderSize {
		if bytes.HasPrefix([]byte(journalMagic), data[:min(len(data), len(journalMagic))]) {
			return nil, 0, fmt.Errorf("%w: the header is cut short", ErrTornRecord)
		}

		return nil, 0, ErrNotJournal
	}

	if !IsJournal(data) {
		return nil, 0, ErrNotJournal
	}

	if data[len(journalMagic)] != journalFormat {
		return nil, 0, fmt.Errorf("%w: journal format %d", ErrNotJournal, data[len(journalMagic)])
	}

	return readRecords(data, JournalHeaderSize)
}

// ReadRecords reads records framed one after another, with no header.
func ReadRecords(data []byte) ([]Record, int, error) {
	return readRecords(data, 0)
}

// readRecords reads the records framed from start on; every offset it
// returns or names counts from the start of data, so a torn record is named
// where it lies in the file.
func readRecords(data []byte, start int) ([]Record, int, error) {
	var records []Record

	offset := start

	for offset < len(data) {
		rest := data[offset:]
		if len(rest) < recordHeadSize {
			return records, offset, fmt.Errorf("%w at byte %d: %d bytes of a record head", ErrTornRecord, offset, len(rest))
		}

		length := binary.LittleEndian.Uint32(rest[:4])
		if length < 2 || length > recordLimit || uint64(length) > uint64(len(rest)-8) {
			return records, offset, fmt.Errorf("%w at byte %d: a record of %d bytes", ErrTornRecord, offset, length)
		}

		frame := rest[:8+int(length)]
		sum := crc32.Update(crc32.Checksum(frame[:4], castagnoli), castagnoli, frame[8:])

		if sum != binary.LittleEndian.Uint32(frame[4:8]) {
			return records, offset, fmt.Errorf("%w at byte %d", ErrRecordChecksum, offset)
		}

		records = append(records, Record{Kind: RecordKind(frame[8]), Schema: frame[9], Body: frame[recordHeadSize:]})
		offset += len(frame)
	}

	return records, offset, nil
}

// EventRecord is event as a journal record.
func EventRecord(event Event) (Record, error) {
	index := -1

	for position, kind := range kinds {
		if kind == event.Kind {
			index = position
		}
	}

	if index < 0 {
		return Record{}, fmt.Errorf("%w: %q", ErrUnknownKind, event.Kind)
	}

	body := []byte{byte(index)}
	body = binary.LittleEndian.AppendUint64(body, uint64(event.Time.UnixNano()))

	for _, field := range []string{event.Entry, event.Seat, event.Target} {
		body = binary.AppendUvarint(body, uint64(len(field)))
		body = append(body, field...)
	}

	body = append(body, normalPayload(event.Payload)...)

	return Record{Kind: RecordEvent, Schema: eventSchema, Body: body}, nil
}

// DecodeEvent reads a RecordEvent's body.
func DecodeEvent(record Record) (Event, error) {
	if record.Kind != RecordEvent || record.Schema != eventSchema {
		return Event{}, fmt.Errorf("%w: record kind %d schema %d is no event", ErrCorrupt, record.Kind, record.Schema)
	}

	reader := tableReader{table: record.Body}

	kind, err := reader.fixed(1)
	if err != nil {
		return Event{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}

	if int(kind[0]) >= len(kinds) {
		return Event{}, fmt.Errorf("%w: %w: kind %d", ErrCorrupt, ErrUnknownKind, kind[0])
	}

	stamp, err := reader.fixed(8)
	if err != nil {
		return Event{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}

	var fields [3]string

	for index := range fields {
		fields[index], err = reader.text()
		if err != nil {
			return Event{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
		}
	}

	return Event{
		Kind:    kinds[kind[0]],
		Entry:   fields[0],
		Time:    time.Unix(0, int64(binary.LittleEndian.Uint64(stamp))).UTC(),
		Seat:    fields[1],
		Target:  fields[2],
		Payload: normalPayload(bytes.Clone(record.Body[reader.offset:])),
	}, nil
}

// eventsOf is every event the records hold, skipping records of a kind or
// schema the memory log does not know.
func eventsOf(records []Record) ([]Event, error) {
	events := make([]Event, 0, len(records))

	for _, record := range records {
		if record.Kind != RecordEvent || record.Schema != eventSchema {
			continue
		}

		event, err := DecodeEvent(record)
		if err != nil {
			return nil, err
		}

		events = append(events, event)
	}

	return events, nil
}

func encodeRecords(events []Event) ([]byte, error) {
	var framed []byte

	for _, event := range events {
		record, err := EventRecord(event)
		if err != nil {
			return nil, err
		}

		framed = AppendRecord(framed, record)
	}

	return framed, nil
}

var recordNames = map[RecordKind]string{
	RecordEvent:      "event",
	RecordHistory:    "history",
	RecordTake:       "take",
	RecordPruned:     "pruned",
	RecordLedgerFile: "ledger-file",
}

// Name is the kind's name, empty for a kind this release does not know.
func (kind RecordKind) Name() string {
	return recordNames[kind]
}
