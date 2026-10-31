package tools_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestARepeatedFindHitPrintsAPointer(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of a file")
	h.tool("line-count", "Count the lines of a file")

	h.must("find", "--term", "word")

	second := h.must("find", "--term", "count")
	if !strings.Contains(second, "\nword-count v1 (seen)\n") || strings.Count(second, "python3 word-count.py") != 0 {
		t.Errorf("a version find printed should come back as its pointer, without its usage:\n%s", second)
	}

	if !strings.Contains(second, "line-count  v1  Count the lines of a file") {
		t.Errorf("a version no read printed should print in full:\n%s", second)
	}

	if forced := h.must("find", "--term", "word", "--again"); !strings.Contains(forced, "word-count  v1  Count the words of a file") {
		t.Errorf("--again should print every hit in full:\n%s", forced)
	}

	if listed := h.must("list"); !strings.Contains(listed, "\nword-count v1 (seen)") || !strings.HasPrefix(listed, "line-count v1 (seen)\n") {
		t.Errorf("list should print a pointer for every version find printed:\n%s", listed)
	}
}

func TestANewerVersionPrintsInFull(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of a file")
	h.must("find", "--term", "word")

	h.must("update", "word-count", "--summary", "Count the words of a file, quotes aside", "--why", "quotes")

	if out := h.must("find", "--term", "word"); !strings.Contains(out, "word-count  v2  Count the words of a file, quotes aside") {
		t.Errorf("a version newer than the one printed should print in full:\n%s", out)
	}
}

func TestASeenToolAnswersWithItsNameAndVersionInJSON(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of a file")
	h.must("find", "--term", "word")

	var answer struct {
		Tools []map[string]any `json:"tools"`
	}

	err := json.Unmarshal([]byte(h.must("find", "--term", "word", "--json")), &answer)
	if err != nil || len(answer.Tools) != 1 {
		t.Fatalf("find --json should answer one tool: %v", err)
	}

	seen := answer.Tools[0]
	if seen["name"] != "word-count" || seen["version"] != float64(1) || seen["seen"] != true || seen["summary"] != nil {
		t.Errorf("a seen tool should answer with its name, label, version and seen alone, got %v", seen)
	}

	var listing struct {
		Tools []map[string]any `json:"tools"`
	}

	err = json.Unmarshal([]byte(h.must("list", "--json")), &listing)
	if err != nil || len(listing.Tools) != 1 {
		t.Fatalf("list --json should answer one tool: %v", err)
	}

	if listed := listing.Tools[0]; listed["seen"] != true || listed["summary"] != nil {
		t.Errorf("list should answer a seen tool with its name, label, version and seen alone, got %v", listed)
	}
}

func TestTheOfferSkipsAToolFindPrinted(t *testing.T) {
	h := newHarness(t)
	h.tool("markdown-words", "Count markdown section words")

	prompt := map[string]any{"prompt": "count markdown section words"}

	h.must("find", "--term", "markdown")

	if offered := h.hook("prompt", prompt); strings.Contains(offered, "markdown-words") {
		t.Errorf("the offer should skip a tool find already printed: %q", offered)
	}

	h.hook("start", map[string]any{"source": "compact"})

	if offered := h.hook("prompt", prompt); !strings.Contains(offered, "markdown-words") {
		t.Errorf("after a compaction the offer should name the tool again: %q", offered)
	}
}

func TestAFreshContextPrintsEveryToolInFull(t *testing.T) {
	cases := []struct {
		source string
		fresh  bool
	}{
		{source: "compact", fresh: true},
		{source: "clear", fresh: true},
		{source: "resume", fresh: true},
		{source: "startup"},
	}

	for _, tc := range cases {
		t.Run(tc.source, func(t *testing.T) {
			h := newHarness(t)
			h.tool("word-count", "Count the words of a file")
			h.must("find", "--term", "word")

			h.hook("start", map[string]any{"source": tc.source})

			full := strings.Contains(h.must("find", "--term", "word"), "word-count  v1  Count the words of a file")
			if full != tc.fresh {
				t.Errorf("only a compaction, /clear or resume should start the context afresh, printed in full: %t", full)
			}
		})
	}
}

func TestAnEndingSessionForgetsWhatItsContextHeld(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of a file")
	h.must("find", "--term", "word")

	h.hook("end", map[string]any{})

	if out := h.must("find", "--term", "word"); !strings.Contains(out, "word-count  v1  Count the words of a file") {
		t.Errorf("the next context of the same process should get every tool in full:\n%s", out)
	}
}
