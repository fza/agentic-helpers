package memorylog

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"
)

const (
	segmentMagic  = "AGMSEG\x00"
	segmentFormat = 1
	headerSize    = 24
)

var (
	errTableTruncated = errors.New("row table truncated")
	errPayloadPastEnd = errors.New("payload runs past the end")
)

// sealedRow is a decoded row table entry. payload slices the segment's
// mapping without touching it, so a fault surfaces only when it is copied.
type sealedRow struct {
	Row
	payload []byte
}

func encodeSegment(events []Event) ([]byte, error) {
	if len(events) > math.MaxUint32 {
		return nil, fmt.Errorf("sealing %d events: more than a segment holds", len(events))
	}

	var table bytes.Buffer
	var payloads bytes.Buffer

	for _, event := range events {
		index := slices.Index(kinds, event.Kind)
		if index < 0 {
			return nil, fmt.Errorf("%w: %q", ErrUnknownKind, event.Kind)
		}

		payload := normalPayload(event.Payload)
		if len(payload) > math.MaxUint32 {
			return nil, fmt.Errorf("sealing the %s event of %s: payload too large", event.Kind, event.Entry)
		}

		table.WriteByte(byte(index))
		table.Write(binary.LittleEndian.AppendUint64(nil, uint64(event.Time.UnixNano())))

		for _, field := range []string{event.Entry, event.Seat, event.Target} {
			table.Write(binary.AppendUvarint(nil, uint64(len(field))))
			table.WriteString(field)
		}

		table.Write(binary.LittleEndian.AppendUint64(nil, uint64(payloads.Len())))
		table.Write(binary.LittleEndian.AppendUint32(nil, uint32(len(payload))))
		payloads.Write(payload)
	}

	segment := make([]byte, 0, headerSize+table.Len()+payloads.Len())
	segment = append(segment, segmentMagic...)
	segment = append(segment, segmentFormat)
	segment = binary.LittleEndian.AppendUint32(segment, uint32(len(events)))
	segment = append(segment, 0, 0, 0, 0)
	segment = binary.LittleEndian.AppendUint64(segment, uint64(table.Len()))
	segment = append(segment, table.Bytes()...)
	segment = append(segment, payloads.Bytes()...)

	return segment, nil
}

// decodeSegment reads the row table of a segment. Called on a mapping, it runs
// inside guardFault.
func decodeSegment(data []byte) ([]sealedRow, error) {
	if len(data) < headerSize {
		return nil, fmt.Errorf("%w: segment shorter than its header", ErrCorrupt)
	}

	if string(data[:len(segmentMagic)]) != segmentMagic {
		return nil, fmt.Errorf("%w: not a segment", ErrCorrupt)
	}

	if data[len(segmentMagic)] != segmentFormat {
		return nil, fmt.Errorf("%w: segment format %d", ErrCorrupt, data[len(segmentMagic)])
	}

	count := binary.LittleEndian.Uint32(data[8:12])

	if binary.LittleEndian.Uint32(data[12:16]) != 0 {
		return nil, fmt.Errorf("%w: segment reserved bytes set", ErrCorrupt)
	}

	tableLength := binary.LittleEndian.Uint64(data[16:24])
	if tableLength > uint64(len(data)-headerSize) {
		return nil, fmt.Errorf("%w: segment row table runs past the end", ErrCorrupt)
	}

	table := data[headerSize : headerSize+int(tableLength)]
	area := data[headerSize+int(tableLength):]

	rows := make([]sealedRow, 0, min(int(count), len(table)))
	reader := tableReader{table: table}

	for range count {
		row, err := reader.row(area)
		if err != nil {
			return nil, fmt.Errorf("%w: segment row %d: %w", ErrCorrupt, len(rows), err)
		}

		rows = append(rows, row)
	}

	if reader.offset != len(table) {
		return nil, fmt.Errorf("%w: segment row table longer than its rows", ErrCorrupt)
	}

	return rows, nil
}

type tableReader struct {
	table  []byte
	offset int
}

func (reader *tableReader) row(area []byte) (sealedRow, error) {
	kind, err := reader.fixed(1)
	if err != nil {
		return sealedRow{}, err
	}

	if int(kind[0]) >= len(kinds) {
		return sealedRow{}, fmt.Errorf("%w: kind %d", ErrUnknownKind, kind[0])
	}

	stamp, err := reader.fixed(8)
	if err != nil {
		return sealedRow{}, err
	}

	var fields [3]string

	for index := range fields {
		fields[index], err = reader.text()
		if err != nil {
			return sealedRow{}, err
		}
	}

	offset, err := reader.fixed(8)
	if err != nil {
		return sealedRow{}, err
	}

	length, err := reader.fixed(4)
	if err != nil {
		return sealedRow{}, err
	}

	start := binary.LittleEndian.Uint64(offset)
	size := uint64(binary.LittleEndian.Uint32(length))

	if start > uint64(len(area)) || size > uint64(len(area))-start {
		return sealedRow{}, errPayloadPastEnd
	}

	return sealedRow{
		Row: Row{
			Kind:   kinds[kind[0]],
			Entry:  fields[0],
			Time:   time.Unix(0, int64(binary.LittleEndian.Uint64(stamp))).UTC(),
			Seat:   fields[1],
			Target: fields[2],
		},
		payload: area[start : start+size : start+size],
	}, nil
}

func (reader *tableReader) fixed(size int) ([]byte, error) {
	if size > len(reader.table)-reader.offset {
		return nil, errTableTruncated
	}

	field := reader.table[reader.offset : reader.offset+size]
	reader.offset += size

	return field, nil
}

func (reader *tableReader) text() (string, error) {
	length, read := binary.Uvarint(reader.table[reader.offset:])
	if read <= 0 {
		return "", errTableTruncated
	}

	reader.offset += read

	if length > uint64(len(reader.table)-reader.offset) {
		return "", errTableTruncated
	}

	field, err := reader.fixed(int(length))
	if err != nil {
		return "", err
	}

	return string(field), nil
}
