package tools_test

import (
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/promptvector"
)

func TestEveryVerbNeedsASessionInsideGit(t *testing.T) {
	h := newHarness(t)

	h.session = ""
	h.refused("no session found for this call", "list")

	h.session = "s1"
	h.git.OutsideWorkTree = true
	h.refused("is not inside a git work tree", "list")
}

func TestTheRoleIsRecordedAndOrdersTheOffer(t *testing.T) {
	h := newHarness(t)
	h.variables["AGENTIC_TOOLS_ROLE"] = "reviewer"
	h.tool("markdown-zeta", "Count markdown section words")

	h.variables["AGENTIC_TOOLS_ROLE"] = "writer"
	h.tool("markdown-alpha", "Count markdown section words", "--distinct", "another")
	h.tool("markdown-beta", "Count markdown section words", "--distinct", "a third")
	h.tool("markdown-gamma", "Count markdown section words", "--distinct", "a fourth")

	if !strings.Contains(h.read("markdown-zeta/history.jsonl"), `"role":"reviewer"`) ||
		!strings.Contains(h.read("markdown-zeta/history.jsonl"), `"by":"`+h.session+`"`) {
		t.Error("history should record the session and its role")
	}

	h.variables["AGENTIC_TOOLS_ROLE"] = "reviewer"

	got := h.hook("prompt", map[string]any{"prompt": "count markdown section words"})
	if !strings.HasPrefix(got, "tool library: near this prompt: markdown-zeta, ") {
		t.Errorf("a same-role tool should be offered first, got %q", got)
	}

	if strings.Contains(h.must("list"), "reviewer") || strings.Contains(h.must("find", "--term", "markdown"), "writer") {
		t.Error("no verb should print a role")
	}
}

func TestAnUpdateFromATakenCopyMovesTheHolding(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")
	h.must("take", "word-count")

	copied := h.store() + "/copies/word-count/v1"
	h.must("update", "word-count", "--from", copied, "--summary", "Count words, edited in the copy", "--why", "edited")

	if h.hook("notice", nil) != "" {
		t.Error("the holding should follow the version its own copy wrote, so nothing newer is noticed")
	}
}

func TestThePromptReusesAVectorAgenticMemoryLeft(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")

	prompt := "count the words of standard input"
	vector, _ := h.embedder.Embed(t.Context(), []string{prompt})

	err := promptvector.Save(promptvector.Dir(h.roots().Cache), h.session, prompt, h.embedder.Fingerprint(), vector[0])
	if err != nil {
		t.Fatalf("saving the slot: %v", err)
	}

	before := h.embedder.Calls()

	if !strings.Contains(h.hook("prompt", map[string]any{"prompt": prompt}), "word-count") {
		t.Error("the offer should still find the tool")
	}

	if h.embedder.Calls() != before {
		t.Error("a prompt agentic-memory embedded should not be embedded again")
	}
}
