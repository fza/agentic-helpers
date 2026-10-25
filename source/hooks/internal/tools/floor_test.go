package tools_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

// cosineTo scores a need against a tool h.tool registered, as find does.
func (h *harness) cosineTo(name string, summary string, need string) float64 {
	h.t.Helper()

	described := strings.Join([]string{name, summary, "python3 " + name + ".py <file>", "", ""}, "\n")

	vectors, err := h.embedder.Embed(h.t.Context(), []string{described, need})
	if err != nil {
		h.t.Fatalf("embedding: %v", err)
	}

	return float64(embed.Cosine(vectors[0], vectors[1]))
}

func (h *harness) queryFloor(floor float64) {
	test.WriteFile(h.t, h.settings, fmt.Sprintf(
		`{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"tools_query_floor":%v}}}}`, floor))
}

func TestTheQueryFloorDefaultsAndMoves(t *testing.T) {
	h := newHarness(t)
	h.tool("markdown-words", "Count the words of each markdown section")

	needs := []string{"words markdown section tally", "count words in markdown", "markdown section words", "words of sections"}

	var need string

	var score float64

	for _, candidate := range needs {
		cosine := h.cosineTo("markdown-words", "Count the words of each markdown section", candidate)
		if cosine >= 0.45 && cosine < 0.6 {
			need, score = candidate, cosine

			break
		}
	}

	if need == "" {
		t.Fatal("the test needs a need scoring between 0.45 and 0.6 against the tool")
	}

	if !strings.Contains(h.must("find", "--query", need), "markdown-words") {
		t.Errorf("a need at %.2f should be found under the default floor", score)
	}

	if !strings.Contains(h.hook("prompt", map[string]any{"prompt": need}), "markdown-words") {
		t.Error("the offer should hold to the default floor too")
	}

	h.queryFloor(score + 0.01)

	if strings.Contains(h.must("find", "--query", need), "markdown-words") {
		t.Error("a floor set above the need's score should drop the tool from find")
	}

	h.session = test.OtherSession
	if strings.Contains(h.hook("prompt", map[string]any{"prompt": need}), "markdown-words") {
		t.Error("a floor set above the need's score should keep the tool out of the offer")
	}

	h.queryFloor(score - 0.01)

	if !strings.Contains(h.must("find", "--query", need), "markdown-words") {
		t.Error("a floor set below the need's score should keep the tool")
	}
}
