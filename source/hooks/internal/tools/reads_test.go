package tools_test

import (
	"encoding/json"
	"github.com/fza/agentic-helpers/source/hooks/test"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindRanksTermHitsThenScoreThenName(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of a markdown section")
	h.tool("section-split", "Split markdown into sections by heading")
	h.tool("zebra-sort", "Sort lines like a zebra")

	other := h.elsewhere(otherRepo)
	other.tool("section-words", "Count the words of a markdown section")

	out := h.must("find", "--query", "count the words of a markdown section", "--term", "split")
	lines := strings.Split(strings.TrimSpace(out), "\n")

	if lines[0] != "Tools other agents registered (their own descriptions):" {
		t.Errorf("a listing should open with the framing lead, got %q", lines[0])
	}

	order := []string{}
	for _, line := range lines[1:] {
		if !strings.HasPrefix(line, " ") && strings.Contains(line, "  v") {
			order = append(order, strings.Fields(line)[0])
		}
	}

	want := []string{"section-split", "word-count", "section-words@format-d-fdbox"}
	if strings.Join(order, " ") != strings.Join(want, " ") {
		t.Errorf("term hits should come first, then the own project's lift should break a tie in score, got %v", order)
	}

	if strings.Contains(out, "zebra-sort") {
		t.Error("a tool below the floor and matching no term should not be found")
	}

	if !strings.Contains(out, "1-3 of 3 matching tools shown, of 4 in 2 projects; `agentic-tools take <name> ...` copies them") {
		t.Errorf("find should close with what it showed, got:\n%s", out)
	}
}

func TestFindPagesAndPrintsJSON(t *testing.T) {
	h := newHarness(t)
	for _, name := range []string{"alpha-tool", "beta-tool", "gamma-tool"} {
		h.tool(name, "A helper named "+name)
	}

	out := h.must("find", "--term", "helper", "--limit", "2")
	if !strings.Contains(out, "1-2 of 3 matching tools shown") || !strings.Contains(out, "--page 2 shows the next 1") {
		t.Errorf("a limited find should say what the next page shows, got:\n%s", out)
	}

	if !strings.Contains(h.must("find", "--term", "helper", "--limit", "2", "--page", "2"), "gamma-tool") {
		t.Error("the second page should show the rest")
	}

	var answer struct {
		Framing string `json:"framing"`
		Tools   []struct {
			Name  string   `json:"name"`
			Label *string  `json:"label"`
			Own   bool     `json:"own"`
			Score *float64 `json:"score"`
		} `json:"tools"`
		Shown, Matching, Total, Projects, Page int
	}

	err := json.Unmarshal([]byte(h.must("find", "--term", "helper", "--json", "--again")), &answer)
	if err != nil {
		t.Fatalf("find --json should print one object: %v", err)
	}

	if answer.Framing != "Tools other agents registered (their own descriptions)" || len(answer.Tools) != 3 ||
		answer.Tools[0].Label != nil || !answer.Tools[0].Own || answer.Tools[0].Score != nil || answer.Total != 3 {
		t.Errorf("find --json should carry the framing, own tools with a null label and a term hit with a null score: %+v", answer)
	}
}

func TestFindWithoutAnEmbedder(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")
	h.embedding = false

	h.refused("--query needs an embedder", "find", "--query", "count words")

	if !strings.Contains(h.must("find", "--term", "WORDS"), "word-count") {
		t.Error("a term should still find, case-insensitive")
	}

	if !strings.Contains(h.must("find", "--term", "print('word-count')"), "word-count") {
		t.Error("a term should match the tool's file text")
	}
}

func TestNamesResolveOwnFirst(t *testing.T) {
	h := newHarness(t)
	first := h.elsewhere("github.com/a/shared")
	second := h.elsewhere("github.com/b/shared")
	first.tool("word-count", "Count words the first way")
	second.tool("word-count", "Tally tokens by another method")
	first.tool("only-there", "A tool one other project holds")

	h.refused("word-count names word-count@a/shared and word-count@b/shared; give one of them", "take", "word-count")

	if !strings.Contains(h.must("take", "only-there"), "only-there@a+shared/v1: A tool one other project holds") {
		t.Error("a bare name should reach a unique foreign tool")
	}

	if !strings.Contains(h.must("take", "word-count@b/shared"), "Tally tokens by another method") {
		t.Error("name@label should reach a foreign tool, its label grown by a segment on a clash")
	}

	h.tool("word-count", "Local wordcounter written here")

	if !strings.Contains(h.must("take", "word-count"), "Local wordcounter written here") {
		t.Error("a bare name should reach the own tool first")
	}
}

func TestProjectScopeKeepsReadsToTheOwnStore(t *testing.T) {
	h := newHarness(t)
	other := h.elsewhere(otherRepo)
	other.tool("line-count", "Count the lines of a file quickly")

	h.variables["AGENTIC_TOOLS_SCOPE"] = "project"

	if strings.Contains(h.must("find", "--term", "lines"), "line-count") {
		t.Error("find should keep to the own store")
	}

	if strings.Contains(h.must("list"), "format-d-fdbox") {
		t.Error("list should name no other project")
	}

	h.refused("no tool line-count", "take", "line-count")

	h.must("add", "lines", "--from", h.file("line.py", "# line-count\nprint('line-count')\n# Count the lines of a file quickly\n"),
		"--summary", "Count the lines of a file quickly", "--usage", "u")
}

func TestTake(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")
	h.tool("word-split", "Put each word on its own line")

	h.refused("nothing taken:\n  no tool nope", "take", "word-count", "nope")

	if test.Exists(t, filepath.Join(h.store(), "copies")) {
		t.Error("a refused take should copy nothing")
	}

	out := h.must("take", "word-count", "word-split")
	if !strings.HasPrefix(out, "Tools other agents registered (their own descriptions):\ntaken into ") ||
		!strings.Contains(out, "word-count/v1: Count the words of standard input\n  usage: python3 word-count.py <file>") {
		t.Errorf("take should print under the framing lead, got:\n%s", out)
	}

	copied := filepath.Join(h.store(), "copies", "word-count", "v1", "word-count.py")

	err := os.WriteFile(copied, []byte("# changed\n"), 0o600)
	if err != nil {
		t.Fatalf("editing the copy: %v", err)
	}

	h.must("take", "word-count")

	data, _ := os.ReadFile(copied)
	if string(data) != "# changed\n" {
		t.Error("an existing copy should be reused, never overwritten")
	}

	if strings.Count(h.read("word-count/takes.jsonl"), "\n") != 1 {
		t.Error("a take line should be written per copy written, not per take")
	}

	h.file(".tmp/keep", "")

	if !strings.Contains(h.must("take", "word-split"), filepath.Join(h.repo, ".tmp", "tools")) {
		t.Error("a checkout holding .tmp/ should take into .tmp/tools/")
	}
}

func TestMerge(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")
	h.tool("count-words", "Count words, another take")
	h.must("take", "count-words")

	out := h.must("merge", "word-count", "count-words", "--from", h.file("merged/wc.py", "merged\n"), "--why", "one tool")
	if !strings.Contains(out, "merged 1 into word-count, now current") {
		t.Errorf("merge should say what it folded, got %q", out)
	}

	if strings.TrimSpace(h.read("count-words/merged")) != "word-count" {
		t.Error("a member should leave a tombstone naming the survivor")
	}

	if test.Exists(t, filepath.Join(h.store(), "count-words", "v1")) {
		t.Error("a member's folder should be removed")
	}

	if strings.Count(h.read("word-count/takes.jsonl"), "\n") != 1 {
		t.Error("the member's takes should fold into the survivor's")
	}

	h.refused("count-words was merged into word-count", "add", "count-words", "--from", h.file("x.py", "x"),
		"--summary", "Something new", "--usage", "u")

	if strings.Contains(h.must("find", "--term", "count"), "count-words") {
		t.Error("find should never show a member")
	}

	if !strings.Contains(h.must("take", "count-words"), "(count-words was merged into it)") {
		t.Error("a merged name should take its survivor, saying so")
	}
}

func TestMergeFoldsOwnToolsOnly(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")

	other := h.elsewhere(otherRepo)
	other.tool("line-count", "Count the lines of a file quickly")

	from := h.file("merged/wc.py", "merged\n")

	h.refused("no tool line-count in this project; merge folds tools of the own project only",
		"merge", "line-count", "word-count", "--from", from, "--why", "one tool")
	h.refused("nothing merged:\n  no tool line-count in this project\n  word-count is the survivor",
		"merge", "word-count", "line-count", "word-count", "--from", from, "--why", "one tool")
}
