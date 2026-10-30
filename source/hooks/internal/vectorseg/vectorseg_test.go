package vectorseg_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/vectorseg"
)

func everything(string, string) bool { return true }

func openStore(t *testing.T) *vectorseg.Store {
	t.Helper()

	dir := t.TempDir()

	return vectorseg.Open(dir, dir)
}

func unit(dims int, axis int) []float32 {
	vector := make([]float32, dims)
	vector[axis%dims] = 1

	return vector
}

func segmentNames(t *testing.T, dir string) []string {
	t.Helper()

	listed, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("listing the segments: %v", err)
	}

	var names []string

	for _, entry := range listed {
		if strings.HasSuffix(entry.Name(), ".seg") {
			names = append(names, entry.Name())
		}
	}

	return names
}

func snapshot(t *testing.T, store *vectorseg.Store) *vectorseg.Snapshot {
	t.Helper()

	taken, err := store.Snapshot()
	if err != nil {
		t.Fatalf("taking a snapshot: %v", err)
	}

	t.Cleanup(func() { _ = taken.Close() })

	return taken
}

func TestARowWrittenIsHeldAndScoredByItsBestChunk(t *testing.T) {
	store := openStore(t)

	err := store.Write([]vectorseg.Row{
		{ID: "m-one", Hash: "h1", Chunks: [][]float32{unit(4, 0), unit(4, 1)}},
		{ID: "m-two", Hash: "h2", Chunks: [][]float32{unit(4, 2)}},
	}, everything)
	if err != nil {
		t.Fatalf("writing: %v", err)
	}

	taken := snapshot(t, store)

	if !taken.Holds("m-one", "h1") || !taken.Holds("m-two", "h2") || taken.Holds("m-one", "h2") {
		t.Error("a snapshot should hold exactly the rows written, by id and hash")
	}

	scores, err := taken.Scores(unit(4, 1), everything)
	if err != nil {
		t.Fatalf("scoring: %v", err)
	}

	if scores["m-one"] != 1 || scores["m-two"] != 0 || len(scores) != 2 {
		t.Errorf("each entry should score by its best chunk, got %v", scores)
	}
}

func TestARowOfAnotherHashIsNotCounted(t *testing.T) {
	store := openStore(t)
	current := func(id string, hash string) bool { return hash == "new" }

	err := store.Write([]vectorseg.Row{{ID: "m-one", Hash: "old", Chunks: [][]float32{unit(4, 0)}}}, everything)
	if err != nil {
		t.Fatalf("writing: %v", err)
	}

	scores, err := snapshot(t, store).Scores(unit(4, 0), current)
	if err != nil {
		t.Fatalf("scoring: %v", err)
	}

	if len(scores) != 0 {
		t.Errorf("a row embedded from content the entry no longer holds should not score, got %v", scores)
	}

	err = store.Write([]vectorseg.Row{{ID: "m-one", Hash: "new", Chunks: [][]float32{unit(4, 1)}}}, current)
	if err != nil {
		t.Fatalf("writing the new content: %v", err)
	}

	if names := segmentNames(t, store.Dir()); len(names) != 1 {
		t.Errorf("a merge should drop the segment whose only row went stale, got %v", names)
	}
}

func TestASegmentMostlyStaleIsRewrittenToItsLiveRows(t *testing.T) {
	store := openStore(t)

	err := store.Write([]vectorseg.Row{
		{ID: "m-one", Hash: "h1", Chunks: [][]float32{unit(4, 0)}},
		{ID: "m-two", Hash: "h2", Chunks: [][]float32{unit(4, 1)}},
		{ID: "m-three", Hash: "h3", Chunks: [][]float32{unit(4, 2)}},
	}, everything)
	if err != nil {
		t.Fatalf("writing: %v", err)
	}

	onlyOne := func(id string, hash string) bool { return id != "m-two" && id != "m-three" }

	err = store.Write([]vectorseg.Row{{ID: "m-four", Hash: "h4", Chunks: [][]float32{unit(4, 3)}}}, onlyOne)
	if err != nil {
		t.Fatalf("writing: %v", err)
	}

	taken := snapshot(t, store)
	if taken.Holds("m-two", "h2") || taken.Holds("m-three", "h3") {
		t.Error("a segment whose dead rows outnumber its live ones should be rewritten without them")
	}

	if !taken.Holds("m-one", "h1") || !taken.Holds("m-four", "h4") {
		t.Error("a rewrite should keep every live row")
	}
}

func TestAWriterLeftTornIsIgnoredThenSwept(t *testing.T) {
	dir := t.TempDir()
	store := vectorseg.Open(dir, dir)

	err := store.Write([]vectorseg.Row{{ID: "m-one", Hash: "h1", Chunks: [][]float32{unit(4, 0)}}}, everything)
	if err != nil {
		t.Fatalf("writing: %v", err)
	}

	whole := segmentNames(t, dir)[0]

	content, err := os.ReadFile(filepath.Join(dir, whole))
	if err != nil {
		t.Fatalf("reading the segment: %v", err)
	}

	torn := filepath.Join(dir, "0000000000000000.seg")
	stray := filepath.Join(dir, ".segment-123")

	for path, data := range map[string][]byte{torn: content[:len(content)-4], stray: content} {
		err = os.WriteFile(path, data, 0o600)
		if err != nil {
			t.Fatalf("planting %s: %v", path, err)
		}
	}

	taken := snapshot(t, store)
	if !taken.Holds("m-one", "h1") {
		t.Error("the whole segment should still be read beside a torn one")
	}

	err = store.Write([]vectorseg.Row{{ID: "m-two", Hash: "h2", Chunks: [][]float32{unit(4, 1)}}}, everything)
	if err != nil {
		t.Fatalf("writing past the torn segment: %v", err)
	}

	for _, path := range []string{torn, stray} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("a write should sweep %s", filepath.Base(path))
		}
	}
}

func TestARowWrittenTwiceScoresOnceAndMergesAway(t *testing.T) {
	store := openStore(t)
	row := vectorseg.Row{ID: "m-one", Hash: "h1", Chunks: [][]float32{unit(4, 0)}}

	for _, chunks := range [][][]float32{{unit(4, 0)}, {unit(4, 0), unit(4, 3)}} {
		row.Chunks = chunks

		err := store.Write([]vectorseg.Row{row}, everything)
		if err != nil {
			t.Fatalf("writing: %v", err)
		}
	}

	if names := segmentNames(t, store.Dir()); len(names) != 1 {
		t.Errorf("a segment holding only a copy should be merged away, got %v", names)
	}

	scores, err := snapshot(t, store).Scores(unit(4, 0), everything)
	if err != nil || len(scores) != 1 {
		t.Errorf("a row held once should score once, got %v, %v", scores, err)
	}
}

func TestSegmentsStayFewAsEntriesAreFiledOneAtATime(t *testing.T) {
	store := openStore(t)

	const entries = 2000

	// Classes 0 to 5 cover 2,000 rows at a ratio of 4, each holding at most 3.
	const bound = 18

	most := 0

	for index := range entries {
		row := vectorseg.Row{ID: fmt.Sprintf("m-%04d", index), Hash: "h", Chunks: [][]float32{unit(8, index)}}

		err := store.Write([]vectorseg.Row{row}, everything)
		if err != nil {
			t.Fatalf("writing entry %d: %v", index, err)
		}

		most = max(most, len(segmentNames(t, store.Dir())))
	}

	if most > bound {
		t.Errorf("the segment count should stay within its bound, peaked at %d", most)
	}

	taken := snapshot(t, store)
	for index := range entries {
		if !taken.Holds(fmt.Sprintf("m-%04d", index), "h") {
			t.Fatalf("every entry filed should still be held, m-%04d is not", index)
		}
	}
}

func TestASnapshotOutlivesTheUnlinkOfItsFiles(t *testing.T) {
	dir := t.TempDir()
	store := vectorseg.Open(dir, dir)

	err := store.Write([]vectorseg.Row{{ID: "m-one", Hash: "h1", Chunks: [][]float32{unit(4, 0)}}}, everything)
	if err != nil {
		t.Fatalf("writing: %v", err)
	}

	taken := snapshot(t, store)

	for _, name := range segmentNames(t, dir) {
		err = os.Remove(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("unlinking %s: %v", name, err)
		}
	}

	scores, err := taken.Scores(unit(4, 0), everything)
	if err != nil || scores["m-one"] != 1 {
		t.Errorf("a snapshot should still read a segment unlinked after it was taken, got %v, %v", scores, err)
	}

	if snapshot(t, store).Holds("m-one", "h1") {
		t.Error("a snapshot taken after the unlink should hold nothing")
	}
}

func TestAVectorOfOtherDimensionsIsRefused(t *testing.T) {
	store := openStore(t)

	err := store.Write([]vectorseg.Row{{ID: "m-one", Hash: "h1", Chunks: [][]float32{unit(4, 0), unit(3, 0)}}}, everything)
	if !errors.Is(err, vectorseg.ErrDims) {
		t.Errorf("a row mixing dimensions should be refused, got %v", err)
	}
}
