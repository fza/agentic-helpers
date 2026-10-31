package tools_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheFramingLeadsOnlyAnotherAgentsText(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of a file")

	cases := []struct {
		name   string
		args   []string
		framed bool
	}{
		{name: "find printing a summary", args: []string{"find", "--term", "word"}, framed: true},
		{name: "find printing seen pointers alone", args: []string{"find", "--term", "word"}},
		{name: "list printing seen pointers alone", args: []string{"list"}},
		{name: "list printing a summary", args: []string{"list", "--again"}, framed: true},
		{name: "find printing no hit", args: []string{"find", "--term", "nothing-holds-this"}},
	}

	for _, sent := range cases {
		t.Run(sent.name, func(t *testing.T) {
			out := h.must(sent.args...)
			if strings.HasPrefix(out, "Tools other agents registered (their own descriptions):\n") != sent.framed {
				t.Errorf("the framing lead should head an answer exactly when another agent's text follows:\n%s", out)
			}
		})
	}

	jsonCases := []struct {
		name   string
		args   []string
		framed bool
	}{
		{name: "find --json carrying a summary", args: []string{"find", "--term", "word", "--json", "--again"}, framed: true},
		{name: "find --json carrying a seen tool alone", args: []string{"find", "--term", "word", "--json"}},
		{name: "list --json carrying a seen tool alone", args: []string{"list", "--json"}},
	}

	for _, sent := range jsonCases {
		t.Run(sent.name, func(t *testing.T) {
			var answer struct {
				Framing string `json:"framing"`
			}

			err := json.Unmarshal([]byte(h.must(sent.args...)), &answer)
			if err != nil {
				t.Fatalf("the listing should decode: %v", err)
			}

			if (answer.Framing != "") != sent.framed {
				t.Errorf("a JSON listing should carry the framing exactly when another agent's text follows: %+v", answer)
			}
		})
	}
}

func TestFindClosesWithOneCountWhereEveryHitFitsAPage(t *testing.T) {
	h := newHarness(t)
	for _, name := range []string{"alpha-tool", "beta-tool", "gamma-tool"} {
		h.tool(name, "A helper named "+name)
	}

	takes := "; `agentic-tools take <name> ...` copies them"

	cases := []struct {
		name    string
		args    []string
		closing string
	}{
		{name: "one match", args: []string{"find", "--term", "alpha"}, closing: "1 match (3 tools, 1 project)" + takes},
		{name: "every match on the page", args: []string{"find", "--term", "helper", "--again"},
			closing: "3 matches (3 tools, 1 project)" + takes},
		{name: "no match", args: []string{"find", "--term", "nothing-holds-this"}, closing: "0 matches (3 tools, 1 project)"},
		{name: "a first page of more", args: []string{"find", "--term", "helper", "--limit", "2", "--again"},
			closing: "1-2 of 3 matching tools shown, of 3 in 1 projects" + takes + "; --page 2 shows the next 1"},
		{name: "a last page", args: []string{"find", "--term", "helper", "--limit", "2", "--page", "2", "--again"},
			closing: "3-3 of 3 matching tools shown, of 3 in 1 projects" + takes},
	}

	for _, sent := range cases {
		t.Run(sent.name, func(t *testing.T) {
			out := strings.TrimRight(h.must(sent.args...), "\n")
			if last := out[strings.LastIndex(out, "\n")+1:]; last != sent.closing {
				t.Errorf("find should close with the count that fits what it showed, got %q", last)
			}
		})
	}
}

func TestListFoldsOtherProjectsIntoOneLine(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of a file")

	h.elsewhere(otherRepo).tool("line-count", "Count the lines of a file")

	second := h.elsewhere("github.com/a/second")
	second.tool("row-count", "Count the rows of a table")
	second.tool("col-count", "Count the columns of a table")

	out := h.must("list")

	if !strings.Contains(out, "\nother projects: ") || strings.Count(out, "`agentic-tools find` searches them") != 1 {
		t.Errorf("list should name every other project in one line:\n%s", out)
	}

	if !strings.Contains(out, "format-d-fdbox 1 tool") || !strings.Contains(out, "second 2 tools") {
		t.Errorf("the line should count each other project's tools:\n%s", out)
	}

	if !strings.Contains(out, "word-count  Count the words of a file  [taken 0]") {
		t.Errorf("list should keep each own tool's take count:\n%s", out)
	}
}

func TestListLeavesOutAnotherProjectHoldingNoTools(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of a file")

	other := h.elsewhere(otherRepo)
	other.tool("line-count", "Count the lines of a file")

	err := os.RemoveAll(filepath.Join(other.store(), "line-count"))
	if err != nil {
		t.Fatalf("emptying the other store: %v", err)
	}

	_ = os.Remove(filepath.Join(other.store(), "index.json"))

	if out := h.must("list"); strings.Contains(out, "format-d-fdbox") || strings.Contains(out, "other projects") {
		t.Errorf("list should leave out a project holding no tools:\n%s", out)
	}
}
