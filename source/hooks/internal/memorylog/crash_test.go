package memorylog_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
)

// sealStates are the files a seal leaves, step by step: the tail it seals, the
// segment it writes, and the manifest listing that segment.
type sealStates struct {
	events   []memorylog.Event
	fullTail []byte
	segment  string
	content  []byte
}

func buildSealStates(t *testing.T) sealStates {
	t.Helper()

	sealing := t.TempDir()
	log := openLog(t, sealing)

	first := append(events(0, 50, 1000), event(50, 10000))
	appendEvents(t, log, first[:50]...)
	head := readFile(t, filepath.Join(sealing, "tail.jsonl"))

	if len(segmentFiles(t, sealing)) != 0 {
		t.Fatal("building the seal states needs the first appends to stay in the tail")
	}

	last := t.TempDir()
	appendEvents(t, openLog(t, last), first[50])
	lastLine := readFile(t, filepath.Join(last, "tail.jsonl"))

	appendEvents(t, log, first[50])

	listed := readManifest(t, sealing)
	if len(listed.Segments) != 1 {
		t.Fatal("building the seal states needs the last append to seal")
	}

	return sealStates{
		events:   first,
		fullTail: append(head, lastLine...),
		segment:  listed.Segments[0],
		content:  readFile(t, filepath.Join(sealing, listed.Segments[0])),
	}
}

func sealingManifest(segments []string, tail []byte) []byte {
	digest := sha256.Sum256(tail)

	return fmt.Appendf(nil, `{"format":1,"segments":["%s"],"sealing":{"tail_sha256":"%s","tail_size":%d}}`,
		strings.Join(segments, `","`), hex.EncodeToString(digest[:]), len(tail))
}

func listingManifest(segments []string) []byte {
	return fmt.Appendf(nil, `{"format":1,"segments":["%s"]}`, strings.Join(segments, `","`))
}

func TestSealCrashStatesRepaired(t *testing.T) {
	states := buildSealStates(t)
	torn := states.fullTail[:len(states.fullTail)-400]

	otherTail := bytes.Replace(states.fullTail, []byte(`"pad":"x`), []byte(`"pad":"y`), 1)
	other := slices.Clone(states.events)
	other[0].Payload = bytes.Replace(other[0].Payload, []byte(`"pad":"x`), []byte(`"pad":"y`), 1)

	cases := []struct {
		name  string
		files map[string][]byte
		want  []memorylog.Event
	}{
		{
			name:  "temp file left by a crashed write",
			files: map[string][]byte{"tail.jsonl": states.fullTail, ".tmp-123": []byte("half a segment")},
			want:  states.events,
		},
		{
			name:  "segment written, manifest not yet",
			files: map[string][]byte{"tail.jsonl": states.fullTail, states.segment: states.content},
			want:  states.events,
		},
		{
			name: "manifest lists the segment, tail still full",
			files: map[string][]byte{
				"tail.jsonl":    states.fullTail,
				states.segment:  states.content,
				"manifest.json": sealingManifest([]string{states.segment}, states.fullTail),
			},
			want: states.events,
		},
		{
			name: "tail emptied, manifest still marks the seal",
			files: map[string][]byte{
				"tail.jsonl":    {},
				states.segment:  states.content,
				"manifest.json": sealingManifest([]string{states.segment}, states.fullTail),
			},
			want: states.events,
		},
		{
			name: "manifest marks a seal the tail does not hold",
			files: map[string][]byte{
				"tail.jsonl":    otherTail,
				states.segment:  states.content,
				"manifest.json": sealingManifest([]string{states.segment}, states.fullTail),
			},
			want: append(slices.Clone(states.events), other...),
		},
		{
			name:  "tail line torn by a crashed append",
			files: map[string][]byte{"tail.jsonl": torn},
			want:  states.events[:50],
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTree(t, dir, tc.files)
			log := openLog(t, dir)

			if !sameEvents(readEvents(t, log), tc.want) {
				t.Fatal("a reader should see every event once in a state a crash left")
			}

			extra := event(1000, 10)
			appendEvents(t, log, extra)

			if !sameEvents(readEvents(t, log), append(slices.Clone(tc.want), extra)) {
				t.Error("the next write should repair the state without losing or duplicating an event")
			}

			assertSettled(t, dir)
		})
	}
}

func assertSettled(t *testing.T, dir string) {
	t.Helper()

	listed := manifestFile{}
	_, err := os.Stat(filepath.Join(dir, "manifest.json"))
	if err == nil {
		listed = readManifest(t, dir)
	}

	if listed.Sealing != nil {
		t.Error("a repaired log should carry no seal in progress")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("listing the log: %v", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".tmp-") || (filepath.Ext(name) == ".seg" && !slices.Contains(listed.Segments, name)) {
			t.Errorf("a repaired log should hold no stray file, found %s", name)
		}
	}
}

func TestMergeCrashStatesRepaired(t *testing.T) {
	appended := events(0, memorylog.MaxSegments, memorylog.SealBytes)

	merging := t.TempDir()
	log := openLog(t, merging)

	for _, next := range appended[:len(appended)-1] {
		appendEvents(t, log, next)
	}

	old := map[string][]byte{}
	listed := readManifest(t, merging).Segments

	for _, name := range listed {
		old[name] = readFile(t, filepath.Join(merging, name))
	}

	last := t.TempDir()
	appendEvents(t, openLog(t, last), appended[len(appended)-1])
	lastSegment := readManifest(t, last).Segments[0]
	old[lastSegment] = readFile(t, filepath.Join(last, lastSegment))
	listed = append(listed, lastSegment)

	appendEvents(t, log, appended[len(appended)-1])
	merged := readManifest(t, merging).Segments
	if len(merged) != 1 || slices.Contains(listed, merged[0]) {
		t.Fatal("building the merge states needs the last append to merge")
	}

	mergedContent := readFile(t, filepath.Join(merging, merged[0]))

	state := func(extra map[string][]byte) map[string][]byte {
		files := maps.Clone(old)
		files["tail.jsonl"] = []byte{}
		maps.Copy(files, extra)

		return files
	}

	cases := []struct {
		name  string
		files map[string][]byte
	}{
		{
			name:  "segments listed, merge not begun",
			files: state(map[string][]byte{"manifest.json": listingManifest(listed)}),
		},
		{
			name:  "merged segment written, manifest not yet",
			files: state(map[string][]byte{"manifest.json": listingManifest(listed), merged[0]: mergedContent}),
		},
		{
			name:  "manifest lists the merged segment, old segments remain",
			files: state(map[string][]byte{"manifest.json": listingManifest(merged), merged[0]: mergedContent}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTree(t, dir, tc.files)
			log := openLog(t, dir)

			if !sameEvents(readEvents(t, log), appended) {
				t.Fatal("a reader should see every event once in a state a crash left")
			}

			extra := event(1000, 10)
			appendEvents(t, log, extra)

			if !sameEvents(readEvents(t, log), append(slices.Clone(appended), extra)) {
				t.Error("the next write should repair the state without losing or duplicating an event")
			}

			assertSettled(t, dir)
		})
	}
}
