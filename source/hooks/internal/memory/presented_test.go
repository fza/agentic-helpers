package memory_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func seenPointer(id string, title string) string {
	return id + " " + title + " (seen)"
}

// bestHitPointer is the pointer a search prints for a hit scoring as the
// best of its result.
func bestHitPointer(id string, title string) string {
	return id + " 100% " + title + " (seen)"
}

func TestARepeatedSearchHitPrintsAPointerInRankOrder(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	gate := h.file("Gate reads Bash only", "finding")
	tail := h.file("Tail seal gate rename", "finding")

	h.must("", "search", "--term", "Gate reads", "--no-heat")

	lines := strings.Split(strings.TrimSpace(h.must("", "search", "--term", "gate", "--no-heat")), "\n")
	if len(lines) != 2 {
		t.Fatalf("both hits should print, one line each: %q", lines)
	}

	pointed, full := -1, -1

	for index, text := range lines {
		switch {
		case text == bestHitPointer(gate, "Gate reads Bash only"):
			pointed = index
		case strings.HasPrefix(text, tail+" 100% Tail seal gate rename  ["):
			full = index
		}
	}

	if pointed < 0 || full < 0 {
		t.Errorf("a hit an earlier search printed should print its pointer, the rest in full:\n%s", strings.Join(lines, "\n"))
	}
}

func TestShowPrintsABodyOnceThenAPointer(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	id := h.file("Gate reads Bash only", "the gate reads Bash calls and nothing else")
	h.must("", "search", "--term", "Gate reads", "--no-heat")

	if shown := h.must("", "show", id, "--no-heat"); !strings.Contains(shown, "the gate reads Bash calls and nothing else") {
		t.Errorf("show after a search hit should print the body the search never printed:\n%s", shown)
	}

	if again := strings.TrimSpace(h.must("", "show", id, "--no-heat")); again != seenPointer(id, "Gate reads Bash only") {
		t.Errorf("a second show should print the pointer alone, got:\n%s", again)
	}

	if forced := h.must("", "show", id, "--no-heat", "--again"); !strings.Contains(forced, "the gate reads Bash calls and nothing else") {
		t.Errorf("--again should print the body in full:\n%s", forced)
	}

	answer := mcpText(t, h.mcp(`{"name":"show","arguments":{"ids":["`+id+`"],"no_heat":true,"again":true}}`))
	if !strings.Contains(answer, "the gate reads Bash calls and nothing else") {
		t.Errorf("the MCP key again should print the body in full: %s", answer)
	}
}

func TestTheBriefsEntriesComeBackAsPointers(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	id := h.file("Gate reads Bash only", "finding")
	h.must("", "brief")

	if out := strings.TrimSpace(h.must("", "search", "--term", "Gate reads", "--no-heat")); out != bestHitPointer(id, "Gate reads Bash only") {
		t.Errorf("an entry the brief printed should come back as its pointer, got:\n%s", out)
	}

	if out := h.must("", "view", "--layout", "active:as-list"); !strings.Contains(out, "  "+seenPointer(id, "Gate reads Bash only")) {
		t.Errorf("a view's list should print the pointer too, got:\n%s", out)
	}
}

func TestAChangedEntryPrintsInFullAgain(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	id := h.file("Gate reads Bash only", "finding")
	h.must("", "search", "--term", "Gate reads", "--no-heat")
	h.must("", "retire", id, "--because", "fixed")

	if out := h.must("", "search", "--term", "Gate reads", "--no-heat"); !strings.Contains(out, "retired") {
		t.Errorf("an entry that changed since it was printed should print in full, its new status with it:\n%s", out)
	}
}

func TestASeenEntryAnswersWithItsIDAndTitleInJSON(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	id := h.file("Gate reads Bash only", "finding")
	h.must("", "search", "--term", "Gate reads", "--no-heat")

	var answer struct {
		Entries []map[string]any `json:"entries"`
	}

	err := json.Unmarshal([]byte(h.must("", "search", "--term", "Gate reads", "--no-heat", "--json")), &answer)
	if err != nil || len(answer.Entries) != 1 {
		t.Fatalf("search --json should answer one entry: %v", err)
	}

	seen := answer.Entries[0]
	if len(seen) != 4 || seen["id"] != id || seen["title"] != "Gate reads Bash only" || seen["seen"] != true || seen["score"] == nil {
		t.Errorf("a seen search hit should answer with its id, score, title and seen alone, got %v", seen)
	}
}

func TestAFreshContextPrintsEverythingInFull(t *testing.T) {
	for _, source := range []string{"compact", "clear", "resume"} {
		t.Run(source, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")

			id := h.file("Gate reads Bash only", "finding")
			h.must("", "search", "--term", "Gate reads", "--no-heat")

			if out := strings.TrimSpace(h.must("", "search", "--term", "Gate reads", "--no-heat")); out != bestHitPointer(id, "Gate reads Bash only") {
				t.Fatalf("a repeated hit should print its pointer before the context starts afresh, got:\n%s", out)
			}

			// A compaction's and a /clear's brief reprints the entry; a resume
			// prints none, so the next read does.
			out := h.must(hookPayload(source, "", ""), "session-start") + test.Unscored(h.must("", "search", "--term", "Gate reads", "--no-heat"))
			if !strings.Contains(out, id+" Gate reads Bash only  [") {
				t.Errorf("a context started afresh should get the entry in full first, got:\n%s", out)
			}
		})
	}
}

func TestRecallSkipsAnEntryAReadPrinted(t *testing.T) {
	for _, read := range []bool{false, true} {
		t.Run(map[bool]string{false: "unread", true: "read"}[read], func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")

			target := h.file("Tail seal manifest rename", "the tail seal writes the manifest by rename")

			for _, word := range unrelated {
				h.file("Unrelated finding about "+word, word+" "+word+" "+word)
			}

			if read {
				h.must("", "search", "--term", "Tail seal", "--no-heat")
			}

			out := h.must(hookPayload("", "", "how does the tail seal manifest rename work"), "prompt")
			if strings.Contains(out, target) == read {
				t.Errorf("recall should name the entry only where no read printed it before:\n%s", out)
			}
		})
	}
}

func TestAPointerHitWarmsAsAFullHitDid(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	id := h.file("Gate reads Bash only", "the gate reads Bash calls and nothing else")
	h.must("", "show", id)

	h.clock.Advance(time.Hour)

	before := h.heatOf(id)
	if out := strings.TrimSpace(h.must("", "show", id)); out != seenPointer(id, "Gate reads Bash only") {
		t.Fatalf("a second show should print the pointer, got:\n%s", out)
	}

	if after := h.heatOf(id); !after.Used.After(before.Used) {
		t.Error("a show answered by a pointer should warm the entry as a full show does")
	}
}
