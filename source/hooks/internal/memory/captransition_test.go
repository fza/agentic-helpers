package memory_test

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
	"github.com/fza/agentic-helpers/source/hooks/internal/vectorseg"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestAPartialFirstFillLeavesACapForTheNextSearch(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.fileUnembedded()

	// The first fill lands one request, then the embedder goes down.
	h.embedder.Err = errors.New("connection refused")
	h.embedder.Answering = h.embedder.Calls() + 1
	h.run("", "search", "--query", "finding")
	h.embedder.Err = nil

	release, taken, err := vectorseg.Open(h.roots().Cache, filepath.Join(h.storeDir(), "segments")).TryFill()
	if err != nil || !taken {
		t.Fatalf("the test should hold the store's fill: %v", err)
	}

	defer release()

	got := h.run("", "search", "--query", "finding")
	if got.code != 0 {
		t.Errorf("a search after a partial first fill should score the rows that landed, exit %d: %s", got.code, got.err)
	}
}

func TestAStoreMovingToANewCapStillCountsItsOldRows(t *testing.T) {
	limit := &test.TokenLimit{Limit: 2048, Unreported: true}
	h, _ := limitedHarness(t, limit)

	// One stream across the runs: a fresh one per run repeats the ids drawn
	// before, and runs out of free ones past 64 entries.
	h.draws = test.Random()

	// More entries than one request carries, so moving them takes two.
	var extra []string
	for number := range embed.RequestTexts {
		extra = append(extra, fmt.Sprintf("Note number %d", number), fmt.Sprintf("note%d body", number))
	}

	h.fileEntries(extra...)
	h.must("", "search", "--query", "gate reads")

	// The gateway counts, so its route measures a new cap; it answers the
	// first request of the move, then goes down.
	limit.Unreported = false
	test.WriteFile(t, h.settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"embedding_path":"/gw/embeddings"}}}}`)

	answered := 0
	flaky := &test.Endpoint{Answer: func(request test.EmbedRequest) (int, string) {
		if !test.IsProbe(request.Inputs[0]) {
			answered++
			if answered > 1 {
				return http.StatusServiceUnavailable, ""
			}
		}

		return limit.Answer(request)
	}}
	h.embedders = func(config embed.Config) (embed.Embedder, error) { return embed.New(config, flaky.Client(t)) }

	if got := h.run("", "search", "--query", "gate reads"); got.code == 0 {
		t.Fatal("the move should fail partway")
	}

	if h.storeCap() != embed.FallbackRunes {
		t.Fatalf("a move cut short should keep the old cap recorded, got %d", h.storeCap())
	}

	release, taken, err := h.vectorStore().TryFill()
	if err != nil || !taken {
		t.Fatalf("the test should hold the store's fill: %v", err)
	}

	defer release()

	// The endpoint is back; the fill stays with the holder of the lock.
	working := &test.Endpoint{Answer: limit.Answer}
	h.embedders = func(config embed.Config) (embed.Embedder, error) { return embed.New(config, working.Client(t)) }

	got := h.run("", "search", "--query", "gate ignores MCP reads always")
	if got.code != 0 || !strings.Contains(got.out, "Gate ignores MCP reads always") {
		t.Errorf("a search during a move should still find entries by their rows of the old cap, exit %d: %s%s", got.code, got.out, got.err)
	}
}
