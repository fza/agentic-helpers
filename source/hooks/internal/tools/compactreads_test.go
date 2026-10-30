package tools_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

// mcpRead is what one read call over MCP answers, as its structuredContent.
func (h *harness) mcpRead(tool string, arguments string) string {
	h.t.Helper()

	lines := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + arguments + `}}` + "\n"

	for _, line := range strings.Split(strings.TrimSpace(h.must2(lines, "mcp")), "\n") {
		if strings.Contains(line, `"id":2`) {
			return structured(line)
		}
	}

	h.t.Fatalf("the server should answer the %s call", tool)

	return ""
}

type listedAnswer struct {
	Tools []map[string]any `json:"tools"`
}

func toolsOf(t *testing.T, text string) []map[string]any {
	t.Helper()

	var answer listedAnswer

	err := json.Unmarshal([]byte(text), &answer)
	if err != nil {
		t.Fatalf("a read should answer one JSON object: %v\n%s", err, text)
	}

	return answer.Tools
}

func TestFindAndListAnswerNoKeyHoldingAnEmptyValue(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of a markdown section")
	h.elsewhere(otherRepo).tool("section-words", "Count the words of a markdown section")

	reads := []struct {
		name string
		args []string
		tool string
		call string
	}{
		{"find by term", []string{"find", "--term", "words"}, "find", `{"term":["words"],"again":true}`},
		{"find by query", []string{"find", "--query", "count the words of a markdown section"}, "find",
			`{"query":"count the words of a markdown section","again":true}`},
		{"find of nothing", []string{"find", "--term", "nothing-matches-this"}, "find", `{"term":["nothing-matches-this"],"again":true}`},
		{"list", []string{"list"}, "list", `{"again":true}`},
	}

	for _, read := range reads {
		t.Run(read.name, func(t *testing.T) {
			for _, text := range []string{h.must(append(read.args, "--json", "--again")...), h.mcpRead(read.tool, read.call)} {
				empty, err := test.EmptyKeys([]byte(text))
				if err != nil || len(empty) > 0 {
					t.Errorf("a read answer should carry no key holding an empty value, got %v %v:\n%s", empty, err, text)
				}
			}
		})
	}
}

func TestAListedToolKeepsEveryKeyHoldingAValue(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of a markdown section", "--inputs", "a markdown file", "--outputs", "a count")
	h.elsewhere(otherRepo).tool("section-words", "Tally the words under one heading", "--distinct", "per heading")

	found := toolsOf(t, h.must("find", "--term", "words", "--json", "--again"))
	if len(found) != 2 {
		t.Fatalf("find should answer both tools: %v", found)
	}

	own, other := keysOf(found[0]), keysOf(found[1])
	if found[0]["name"] != "word-count" {
		own, other = other, own
	}

	if !slices.Equal(own, []string{"name", "score", "summary", "version"}) {
		t.Errorf("an own hit should carry its name, version, summary and score alone, got %v", own)
	}

	if !slices.Equal(other, []string{"label", "name", "score", "summary", "version"}) {
		t.Errorf("another project's hit should add its label and nothing else, got %v", other)
	}
}

func keysOf(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}

// scoresOf is each tool's score, -1 where it carries none.
func scoresOf(tools []map[string]any) []int {
	scores := make([]int, 0, len(tools))
	for _, tool := range tools {
		score, carries := tool["score"].(float64)
		if !carries {
			score = -1
		}

		scores = append(scores, int(score))
	}

	return scores
}

func TestFindScoresEveryHitFromTheBestDown(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of a markdown section")
	h.tool("section-split", "Split markdown into sections by heading")
	h.tool("line-count", "Count the lines of a markdown file")
	h.elsewhere(otherRepo).tool("section-words", "Count the words of a markdown section")

	finds := []struct {
		name string
		args []string
		call string
	}{
		{"by query", []string{"--query", "count the words of a markdown section"}, `{"query":"count the words of a markdown section","again":true}`},
		{"by term and query", []string{"--query", "count the words of a markdown section", "--term", "split"},
			`{"query":"count the words of a markdown section","term":["split"],"again":true}`},
	}

	for _, find := range finds {
		t.Run(find.name, func(t *testing.T) {
			for _, text := range []string{
				h.must(append(append([]string{"find"}, find.args...), "--json", "--again")...),
				h.mcpRead("find", find.call),
			} {
				scores := scoresOf(toolsOf(t, text))
				if len(scores) < 2 || scores[0] != 100 {
					t.Fatalf("the best hit should score full marks, got %v", scores)
				}

				for index, score := range scores[1:] {
					if score < 0 || score > scores[index] {
						t.Errorf("every hit should carry a score no higher than the hit above it, got %v", scores)
					}
				}

				if slices.Equal(scores, slices.Repeat([]int{100}, len(scores))) {
					t.Errorf("hits matching less well than the best should score below it, got %v", scores)
				}
			}
		})
	}

	t.Run("a later page keeps the result's best", func(t *testing.T) {
		whole := scoresOf(toolsOf(t, h.must("find", "--query", "count the words of a markdown section", "--json", "--again")))
		later := scoresOf(toolsOf(t, h.must("find", "--query", "count the words of a markdown section", "--limit", "1", "--page", "2",
			"--json", "--again")))

		if len(whole) < 2 || !slices.Equal(later, whole[1:2]) {
			t.Errorf("a later page should score each hit as the whole result does, got %v of %v", later, whole)
		}
	})

	t.Run("a seen hit keeps its score", func(t *testing.T) {
		h.must("find", "--query", "count the words of a markdown section", "--json", "--again")

		tools := toolsOf(t, h.must("find", "--query", "count the words of a markdown section", "--json"))
		if len(tools) == 0 || tools[0]["seen"] != true || scoresOf(tools)[0] != 100 {
			t.Errorf("a hit the context already holds should answer its pointer with its score: %v", tools)
		}
	})

	t.Run("list carries none", func(t *testing.T) {
		for _, text := range []string{h.must("list", "--json", "--again"), h.mcpRead("list", `{"again":true}`)} {
			scores := scoresOf(toolsOf(t, text))
			if len(scores) == 0 || slices.ContainsFunc(scores, func(score int) bool { return score >= 0 }) {
				t.Errorf("list ranks nothing and should carry no score, got %v", scores)
			}
		}
	})
}
