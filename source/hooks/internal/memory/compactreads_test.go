package memory_test

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

// richStore files entries carrying every kind of value an entry object holds:
// a trap filed on another branch with a trigger, an anchor, a link, an
// attachment and a recipient, then retired; a rule with a moment, a proof and
// a forbidden pattern; a todo with a declared end, a superseded note and the
// state slot.
func richStore(t *testing.T) (*harness, string, string) {
	t.Helper()

	h := newHarness(t)
	withGraph(t, h)
	h.must("", "claim", "main")

	log := filepath.Join(t.TempDir(), "build.log")
	test.WriteFile(t, log, "compile step passed\n")

	h.git.SwitchBranch("feature")
	trap := h.file("Gate trap about internal/gate/gate.go", "the gate trap body", "--kind", "trap", "--when", "command:go build",
		"--about", "internal/gate/gate.go", "--link", closedDecision, "--attach", log, "--for", "all", "--topic", "gate")
	h.git.SwitchBranch("main")

	h.file("Gate note citing the trap", "cites "+trap, "--topic", "gate")
	h.must("", "retire", trap, "--because", "fixed in the gate")

	out := h.must("read the board", "new", "--title", "Gate rule read on claim", "--kind", "rule", "--from", "user", "--on", "claim",
		"--proof", "command:go vet", "--forbids", `(?i)pilot v\d+`, "--topic", "gate")
	rule := idPattern.FindString(out)

	h.file("Gate todo ending at a claim", "a todo body", "--kind", "todo", "--until", "claim")
	old := h.file("Gate note replaced later", "an older body")
	newer := h.file("Gate note replacing the old", "a newer body")
	h.must("", "supersede", old, "--by", newer)
	h.must("where the gate work stands", "state")

	return h, trap, rule
}

func TestNoReadAnswersAKeyHoldingAnEmptyValue(t *testing.T) {
	h, trap, rule := richStore(t)

	reads := []struct {
		name string
		args []string
		call string
	}{
		{"show", []string{"show", trap, rule, "--with-superseded", "--again"},
			`{"name":"show","arguments":{"ids":["` + trap + `","` + rule + `"],"again":true}}`},
		{"search by term", []string{"search", "--term", "Gate", "--status", "live", "--status", "retired", "--again"},
			`{"name":"search","arguments":{"term":["Gate"],"status":["live","retired"],"again":true}}`},
		{"search by query", []string{"search", "--query", "gate note", "--again"},
			`{"name":"search","arguments":{"query":"gate note","again":true}}`},
		{"hybrid search", []string{"search", "--term", "Gate", "--query", "gate rule", "--again"},
			`{"name":"search","arguments":{"term":["Gate"],"query":"gate rule","again":true}}`},
		{"search listing", []string{"search", "--status", "superseded", "--again"},
			`{"name":"search","arguments":{"status":["superseded"],"again":true}}`},
		{"empty search", []string{"search", "--term", "nothing-matches-this"},
			`{"name":"search","arguments":{"term":["nothing-matches-this"]}}`},
		{"brief", []string{"brief", "--again"}, `{"name":"brief","arguments":{"again":true}}`},
		{"view as-list", []string{"view", "--layout", "active:as-list", "--again"},
			`{"name":"view","arguments":{"layout":"active:as-list","again":true}}`},
		{"view as-bodies", []string{"view", "--layout", "kind(trap):as-bodies", "--again"},
			`{"name":"view","arguments":{"layout":"kind(trap):as-bodies","again":true}}`},
		{"view as-counts", []string{"view", "--layout", "active:as-counts"}, `{"name":"view","arguments":{"layout":"active:as-counts"}}`},
		{"view as-grouped", []string{"view", "--layout", "active:group(by(kind)):as-grouped", "--again"},
			`{"name":"view","arguments":{"layout":"active:group(by(kind)):as-grouped","again":true}}`},
		{"empty view", []string{"view", "--layout", "topic(nothing-filed-here):as-list"},
			`{"name":"view","arguments":{"layout":"topic(nothing-filed-here):as-list"}}`},
		{"empty counts", []string{"view", "--layout", "topic(nothing-filed-here):as-counts"},
			`{"name":"view","arguments":{"layout":"topic(nothing-filed-here):as-counts"}}`},
		{"who", []string{"who", "--all"}, `{"name":"who","arguments":{"all":true}}`},
		{"topics", []string{"topics"}, `{"name":"topics","arguments":{}}`},
		{"holder", []string{"holder", "main"}, ""},
		{"holder of no seat", []string{"holder", "--session", "nobody"}, ""},
	}

	for _, read := range reads {
		t.Run(read.name, func(t *testing.T) {
			got := h.run("", append(read.args, "--json")...)
			if got.out == "" {
				t.Fatalf("the read should answer JSON, got exit %d: %s", got.code, got.err)
			}

			noEmptyKey(t, got.out)

			if read.call != "" {
				noEmptyKey(t, mcpText(t, h.mcp(read.call)))
			}
		})
	}
}

func TestARichEntryKeepsEveryKeyHoldingAValue(t *testing.T) {
	h, trap, rule := richStore(t)

	cases := []struct {
		name string
		id   string
		keys []string
	}{
		{"a retired trap filed on another branch", trap, []string{
			"anchors", "attachments", "body", "branch", "commit", "filed", "for", "from", "id", "kind", "linked_from", "links",
			"other_branch", "retention", "seat", "shelf", "status", "status_change", "title", "topics", "when",
		}},
		{"a rule with a moment, a proof and a forbidden pattern", rule, []string{
			"body", "branch", "commit", "filed", "forbids", "from", "id", "kind", "on", "proof", "retention", "seat", "shelf",
			"status", "title", "topics",
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, via := range []string{"cli", "mcp"} {
				text := h.must("", "show", tc.id, "--json", "--again", "--no-heat")
				if via == "mcp" {
					text = mcpText(t, h.mcp(`{"name":"show","arguments":{"ids":["`+tc.id+`"],"again":true,"no_heat":true}}`))
				}

				entries, _ := decoded(t, text)["entries"].([]any)
				if len(entries) != 1 {
					t.Fatalf("show should answer the entry asked for: %s", text)
				}

				object, _ := entries[0].(map[string]any)
				if !slices.Equal(keysOf(object), tc.keys) {
					t.Errorf("over %s, every key holding a value should stay and only those, got %v", via, keysOf(object))
				}
			}
		})
	}
}

// hitScores is each hit's score of a search answered as JSON, -1 where a hit
// carries none.
func hitScores(t *testing.T, text string) []int {
	t.Helper()

	var answer struct {
		Entries []struct {
			Score *int `json:"score"`
		} `json:"entries"`
	}

	err := json.Unmarshal([]byte(text), &answer)
	if err != nil {
		t.Fatalf("a search should answer one JSON object: %v\n%s", err, text)
	}

	scores := make([]int, 0, len(answer.Entries))
	for _, hit := range answer.Entries {
		score := -1
		if hit.Score != nil {
			score = *hit.Score
		}

		scores = append(scores, score)
	}

	return scores
}

func TestRankedHitsScoreFromTheBestDown(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("Waiter waits once", "the waiter polls")
	h.file("Waiter waits twice, waiter again", "waiter waiter")
	h.file("Gate reads only Bash", "the gate skips the waiter")
	h.file("Unrelated finding about paging", "no match here")

	searches := []struct {
		name string
		args []string
		call string
	}{
		{"by term", []string{"--term", "waiter"}, `{"term":["waiter"]}`},
		{"by query", []string{"--query", "waiter polls"}, `{"query":"waiter polls"}`},
		{"hybrid", []string{"--term", "waiter", "--query", "gate reads"}, `{"term":["waiter"],"query":"gate reads"}`},
	}

	for _, search := range searches {
		t.Run(search.name, func(t *testing.T) {
			for _, text := range []string{
				h.must("", append(append([]string{"search"}, search.args...), "--json", "--again", "--no-heat")...),
				mcpText(t, h.mcp(`{"name":"search","arguments":`+strings.TrimSuffix(search.call, "}")+`,"again":true,"no_heat":true}}`)),
			} {
				scores := hitScores(t, text)
				if len(scores) < 2 || scores[0] != 100 {
					t.Fatalf("the best hit should score full marks, got %v", scores)
				}

				for index, score := range scores[1:] {
					if score < 0 || score > scores[index] {
						t.Errorf("every hit should carry a score no higher than the hit above it, got %v", scores)
					}
				}

				if scores[len(scores)-1] == 100 {
					t.Errorf("a hit ranked below the best for a weaker match should score below it, got %v", scores)
				}
			}
		})
	}

	t.Run("relative to the best match", func(t *testing.T) {
		scores := hitScores(t, h.must("", "search", "--term", "waiter", "--json", "--again", "--no-heat"))
		if !slices.Equal(scores, []int{100, 50, 25}) {
			t.Errorf("a hit matching a share of the best hit's matches should score that share in whole percent, got %v", scores)
		}
	})

	t.Run("a later page keeps the result's best", func(t *testing.T) {
		whole := hitScores(t, h.must("", "search", "--term", "waiter", "--json", "--again", "--no-heat"))
		later := hitScores(t, h.must("", "search", "--term", "waiter", "--skip", "1", "--json", "--again", "--no-heat"))

		if !slices.Equal(later, whole[1:]) {
			t.Errorf("a later page should score each hit as the whole result does, got %v of %v", later, whole)
		}
	})

	t.Run("a listing carries none", func(t *testing.T) {
		scores := hitScores(t, h.must("", "search", "--status", "live", "--json", "--again"))
		if len(scores) == 0 || slices.ContainsFunc(scores, func(score int) bool { return score >= 0 }) {
			t.Errorf("a listing ranks by time, not by a match, and should carry no score, got %v", scores)
		}

		if text := h.must("", "search", "--status", "live", "--again"); strings.Contains(text, "%") {
			t.Errorf("a listing's text should print no score:\n%s", text)
		}
	})
}

func TestAnEmptyBodyLeavesTheAnswer(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	id := idPattern.FindString(h.must("", "new", "--title", "Gate note without a body", "--topic", "gate"))

	entries, _ := decoded(t, h.must("", "show", id, "--json"))["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("show should answer the entry asked for: %v", entries)
	}

	if object, _ := entries[0].(map[string]any); object["body"] != nil {
		t.Errorf("an empty body is an empty string and should leave the answer: %v", object)
	}
}

var scoredLine = regexp.MustCompile(`^(m-[a-z0-9]+) (\d+)% (\S.*)$`)

func TestSearchLinesPrintTheScoreAfterTheID(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	twice := h.file("Waiter waits twice, waiter again", "waiter waiter")
	once := h.file("Waiter waits once", "the waiter polls")
	h.must("", "search", "--term", "polls", "--no-heat")

	lines := strings.Split(strings.TrimSpace(h.must("", "search", "--term", "waiter", "--no-heat")), "\n")
	if len(lines) != 2 {
		t.Fatalf("both hits should print, one line each: %q", lines)
	}

	first, second := scoredLine.FindStringSubmatch(lines[0]), scoredLine.FindStringSubmatch(lines[1])

	if first == nil || first[1] != twice || first[2] != "100" || !strings.HasPrefix(first[3], "Waiter waits twice, waiter again  [") {
		t.Errorf("a hit's line should print its id, its score and then its line: %q", lines[0])
	}

	if second == nil || second[1] != once || second[2] != "50" || second[3] != "Waiter waits once (seen)" {
		t.Errorf("a seen hit's pointer should print its score after the id too: %q", lines[1])
	}
}
