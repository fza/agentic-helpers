package memorylog_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
)

const listedSegment = "0badc0de.seg"

type craftedRow struct {
	kind   byte
	entry  string
	offset uint64
	length uint32
}

func craftRow(row craftedRow) []byte {
	encoded := []byte{row.kind}
	encoded = binary.LittleEndian.AppendUint64(encoded, 1)

	for _, field := range []string{row.entry, "drive", ""} {
		encoded = binary.AppendUvarint(encoded, uint64(len(field)))
		encoded = append(encoded, field...)
	}

	encoded = binary.LittleEndian.AppendUint64(encoded, row.offset)

	return binary.LittleEndian.AppendUint32(encoded, row.length)
}

func craftSegment(magic string, format byte, count uint32, reserved uint32, tableLength int, table []byte, area []byte) []byte {
	segment := append([]byte(magic), format)
	segment = binary.LittleEndian.AppendUint32(segment, count)
	segment = binary.LittleEndian.AppendUint32(segment, reserved)
	segment = binary.LittleEndian.AppendUint64(segment, uint64(tableLength))
	segment = append(segment, table...)

	return append(segment, area...)
}

func TestCorruptLogIsAnError(t *testing.T) {
	valid := craftRow(craftedRow{entry: "m-1", length: 2})
	unknownKind := craftRow(craftedRow{kind: 200, entry: "m-1", length: 2})
	overflowing := append(valid[:9:9], bytes.Repeat([]byte{0xff}, 11)...)
	huge := binary.AppendUvarint(valid[:9:9], 1<<63)
	segment := func(content []byte) map[string][]byte {
		return map[string][]byte{"manifest.json": listingManifest([]string{listedSegment}), listedSegment: content}
	}

	cases := []struct {
		name  string
		files map[string][]byte
	}{
		{name: "segment empty", files: segment(nil)},
		{name: "segment shorter than its header", files: segment([]byte("AGMSEG\x00\x01"))},
		{name: "segment magic wrong", files: segment(craftSegment("AGMSEX\x00", 1, 1, 0, len(valid), valid, []byte("{}")))},
		{name: "segment format unknown", files: segment(craftSegment("AGMSEG\x00", 2, 1, 0, len(valid), valid, []byte("{}")))},
		{name: "segment reserved bytes set", files: segment(craftSegment("AGMSEG\x00", 1, 1, 7, len(valid), valid, []byte("{}")))},
		{name: "row table past the end", files: segment(craftSegment("AGMSEG\x00", 1, 1, 0, len(valid)+10, valid, nil))},
		{name: "row table truncated", files: segment(craftSegment("AGMSEG\x00", 1, 2, 0, len(valid), valid, []byte("{}")))},
		{name: "row cut inside a field", files: segment(craftSegment("AGMSEG\x00", 1, 1, 0, 12, valid[:12], nil))},
		{name: "row cut inside a fixed field", files: segment(craftSegment("AGMSEG\x00", 1, 1, 0, len(valid)-2, valid[:len(valid)-2], nil))},
		{
			name:  "field length overflowing",
			files: segment(craftSegment("AGMSEG\x00", 1, 1, 0, len(overflowing), overflowing, nil)),
		},
		{
			name:  "field length past any table",
			files: segment(craftSegment("AGMSEG\x00", 1, 1, 0, len(huge), huge, nil)),
		},
		{
			name:  "row kind unknown",
			files: segment(craftSegment("AGMSEG\x00", 1, 1, 0, len(unknownKind), unknownKind, []byte("{}"))),
		},
		{
			name:  "payload past the end",
			files: segment(craftSegment("AGMSEG\x00", 1, 1, 0, len(valid), craftRow(craftedRow{entry: "m-1", offset: 1, length: 2}), []byte("{}"))),
		},
		{
			name:  "row table longer than its rows",
			files: segment(craftSegment("AGMSEG\x00", 1, 1, 0, len(valid)+1, append(bytes.Clone(valid), 0), []byte("{}"))),
		},
		{name: "tail line not JSON", files: map[string][]byte{"tail.jsonl": []byte("{\"kind\":\n")}},
		{name: "tail kind unknown", files: map[string][]byte{"tail.jsonl": []byte(`{"kind":"forget","time":1}` + "\n")}},
		{name: "manifest not JSON", files: map[string][]byte{"manifest.json": []byte("{")}},
		{name: "manifest format unknown", files: map[string][]byte{"manifest.json": []byte(`{"format":2,"segments":[]}`)}},
		{name: "manifest names a path", files: map[string][]byte{"manifest.json": listingManifest([]string{"../0badc0de.seg"})}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTree(t, dir, tc.files)

			_, err := openLog(t, dir).Snapshot(context.Background())
			if !errors.Is(err, memorylog.ErrCorrupt) {
				t.Errorf("a corrupt log should fail the snapshot as corrupt, got: %v", err)
			}
		})
	}
}

func TestCraftedSegmentReads(t *testing.T) {
	dir := t.TempDir()
	row := craftRow(craftedRow{entry: "m-1", length: 2})
	writeTree(t, dir, map[string][]byte{
		"manifest.json": listingManifest([]string{listedSegment}),
		listedSegment:   craftSegment("AGMSEG\x00", 1, 1, 0, len(row), row, []byte("{}")),
	})

	read := readEvents(t, openLog(t, dir))
	if len(read) != 1 || read[0].Entry != "m-1" || string(read[0].Payload) != "{}" {
		t.Error("the crafted segment the corrupt cases start from should read as valid")
	}
}

func TestSealRefusesNameCollision(t *testing.T) {
	sealed := t.TempDir()
	oversized := event(0, memorylog.SealBytes)
	appendEvents(t, openLog(t, sealed), oversized)
	name := readManifest(t, sealed).Segments[0]

	dir := t.TempDir()
	writeTree(t, dir, map[string][]byte{"manifest.json": listingManifest([]string{name}), name: []byte("other bytes")})

	err := openLog(t, dir).Append(context.Background(), oversized)
	if !errors.Is(err, memorylog.ErrCollision) {
		t.Error("a seal should refuse to overwrite a listed segment holding other bytes")
	}

	if !bytes.Equal(readFile(t, filepath.Join(dir, name)), []byte("other bytes")) {
		t.Error("a refused seal should leave the listed segment untouched")
	}
}

func TestSealOfIdenticalEventsReusesSegment(t *testing.T) {
	dir := t.TempDir()
	log := openLog(t, dir)
	oversized := event(0, memorylog.SealBytes)

	appendEvents(t, log, oversized)
	appendEvents(t, log, oversized)

	listed := readManifest(t, dir).Segments
	if len(listed) != 2 || listed[0] != listed[1] || len(segmentFiles(t, dir)) != 1 {
		t.Error("sealing identical events twice should list the one segment twice")
	}

	if !sameEvents(readEvents(t, log), []memorylog.Event{oversized, oversized}) {
		t.Error("each seal of identical events should still read back")
	}
}

func TestMissingSegment(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string][]byte{"manifest.json": listingManifest([]string{listedSegment})})
	log := openLog(t, dir)

	_, err := log.Snapshot(context.Background())
	if !errors.Is(err, memorylog.ErrUnstable) {
		t.Error("a snapshot should give up on a listed segment that stays missing")
	}

	err = log.Merge(context.Background(), nil)
	if !errors.Is(err, memorylog.ErrCorrupt) {
		t.Error("a merge should fail as corrupt on a listed segment that is missing")
	}
}

func TestMergeRefusesUnknownKind(t *testing.T) {
	dir := t.TempDir()
	log := openLog(t, dir)
	appendEvents(t, log, event(0, memorylog.SealBytes))

	inventing := func([]memorylog.Row, func(int) ([]byte, error)) ([]memorylog.Event, error) {
		return []memorylog.Event{{Kind: "forget", Entry: "m-1", Time: start}}, nil
	}

	err := log.Merge(context.Background(), inventing)
	if !errors.Is(err, memorylog.ErrUnknownKind) {
		t.Error("a merge should refuse a kept event of an unknown kind")
	}

	if !sameEvents(readEvents(t, log), events(0, 1, memorylog.SealBytes)) {
		t.Error("a refused merge should leave the sealed events")
	}
}
