package tools_test

import (
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
	"github.com/fza/agentic-helpers/source/hooks/internal/vectorseg"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestAWriteIntoAStoreWithoutACapRecordsOne(t *testing.T) {
	limit := &test.TokenLimit{Limit: 2048}
	h := limitedHarness(t, limit)

	// The index exists, so the next add rebuilds nothing and fills nothing.
	h.unembedded("word-count", "Count the words of standard input")
	h.tool("line-count", "Count the lines of a file")

	release, taken, err := h.limitedStore().TryFill()
	if err != nil || !taken {
		t.Fatalf("the test should hold the store's fill: %v", err)
	}

	defer release()

	if got := h.run("", "find", "--query", "count lines"); got.code != 0 {
		t.Errorf("a write into a store without a cap should record the cap it cut to, exit %d: %s", got.code, got.err)
	}
}

func TestAFindCountsOnlyRowsOfTheRecordedCap(t *testing.T) {
	limit := &test.TokenLimit{Limit: 2048}
	h := limitedHarness(t, limit)
	h.unembedded("word-count", "Count the words of standard input")

	query := "count words"
	vectors, _ := (&test.Embedder{}).Embed(t.Context(), []string{query})
	described := h.described("word-count")

	// A row cut to a cap the store does not record, matching the query
	// exactly, while another process holds the fill.
	store := h.limitedStore()

	err := store.SetManifest(vectorseg.Manifest{MaxRunes: 1792})
	if err != nil {
		t.Fatalf("recording the cap should work: %v", err)
	}

	err = store.Write([]vectorseg.Row{{ID: described, Hash: embed.RowHash(described, 2048), Chunks: vectors}},
		func(string, string) bool { return true })
	if err != nil {
		t.Fatalf("seeding the row should work: %v", err)
	}

	release, taken, err := store.TryFill()
	if err != nil || !taken {
		t.Fatalf("the test should hold the store's fill: %v", err)
	}

	defer release()

	if strings.Contains(h.must("find", "--query", query), "word-count") {
		t.Error("a row cut to a cap the store does not record should never be scored")
	}
}
