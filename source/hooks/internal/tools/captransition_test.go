package tools_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestAFillEmbedsALongToolTextInPieces(t *testing.T) {
	limit := &test.TokenLimit{Limit: 2048}
	h := limitedHarness(t, limit)

	embedders := h.embedders
	h.embedders = nil
	h.embedding = false
	h.tool("dense-notes", "Summarise dense notes", "--outputs", strings.Repeat("密集的笔记内容。", 600))
	h.embedders = embedders

	h.must("find", "--query", "summarise notes")

	pieces := 0

	for _, text := range sentTexts(limit) {
		if utf8.RuneCountInString(text) > 1792 {
			t.Errorf("every piece a fill sends should fit the cap, one held %d runes", utf8.RuneCountInString(text))
		}

		if strings.Contains(text, "密集") {
			pieces++
		}
	}

	if pieces < 3 || len(limit.Truncated()) != 0 || !h.limitedHolds(h.described("dense-notes"), 1792) {
		t.Errorf("a fill should embed a long tool text in pieces none of which is cut, %d pieces, %d cut", pieces, len(limit.Truncated()))
	}
}

func TestAToolStoreMovingToANewCapDropsItsOldRowsOnceThrough(t *testing.T) {
	limit := &test.TokenLimit{Limit: 2048, Unreported: true}
	h := limitedHarness(t, limit)
	h.tool("word-count", "Count the words of standard input")

	described := h.described("word-count")

	if h.limitedCap() != 2048 || !h.limitedHolds(described, 2048) {
		t.Fatalf("a provider reporting no count should file the tool at the fallback cap, got %d", h.limitedCap())
	}

	// The gateway counts, so its route measures a new cap.
	limit.Unreported = false
	test.WriteFile(t, h.settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"embedding_path":"/gw/embeddings"}}}}`)

	if !strings.Contains(h.must("find", "--query", "count words"), "word-count") {
		t.Error("a find across the move should still answer")
	}

	if h.limitedCap() != 1792 || !h.limitedHolds(described, 1792) {
		t.Errorf("the fill should move the store to the new cap, got %d", h.limitedCap())
	}

	// The next write merges away the row of the old cap.
	h.tool("line-count", "Count the lines of a file")

	if h.limitedHolds(described, 2048) {
		t.Error("a row of a cap the store has left should merge away once the move is through")
	}
}
