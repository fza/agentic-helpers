package tools_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestTheFirstWriteClaimsTheStore(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")

	if strings.TrimSpace(h.read("writers.json")) != `["`+filepath.Join(h.repo, ".git")+`"]` {
		t.Errorf("the first write should list its checkout, got %s", h.read("writers.json"))
	}

	worktree := *h
	worktree.repo = filepath.Join(filepath.Dir(h.repo), "app-worktree")
	worktree.git = &test.Git{Top: worktree.repo, Commit: "15af1a8c", Branch: "feature"}
	h.worktrees[worktree.repo] = h.repo
	test.WriteFile(t, filepath.Join(worktree.repo, ".sdd", "config.yaml"), "repo_id: "+ownRepo+"\n")

	worktree.tool("word-split", "Put each word on its own line")
}

func TestACheckoutNotListedWritesNothing(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")
	h.tool("word-split", "Put each word on its own line")

	impostor := h.elsewhere(ownRepo)
	from := impostor.file("evil.py", "evil\n")
	before := test.Tree(t, h.store())

	writes := [][]string{
		{"add", "evil-tool", "--from", from, "--summary", "A tool from elsewhere", "--usage", "u"},
		{"update", "word-count", "--summary", "Count words, rewritten elsewhere", "--why", "x"},
		{"merge", "word-count", "word-split", "--from", from, "--why", "x"},
		{"reindex"},
	}

	for _, args := range writes {
		impostor.refused("this library belongs to other checkouts: "+h.repo+"; run `agentic-tools trust "+impostor.repo+
			"` from one of them to let this one write", args...)
	}

	if test.Tree(t, h.store()) != before {
		t.Error("a refused write should leave the store byte-identical")
	}

	impostor.refused("takes only into its own .tmp/", "take", "word-count")

	impostor.file(".tmp/keep", "")
	before = test.Tree(t, h.store())

	if !strings.Contains(impostor.must("take", "word-count"), filepath.Join(impostor.repo, ".tmp", "tools")) {
		t.Error("a checkout not listed should still take a copy into its own .tmp/")
	}

	if test.Tree(t, h.store()) != before {
		t.Error("a take from a checkout not listed should record nothing in the store")
	}

	_ = os.Remove(filepath.Join(h.store(), "index.json"))
	before = test.Tree(t, h.store())

	impostor.must("list")

	if test.Tree(t, h.store()) != before {
		t.Error("a checkout not listed should write no index back, nor anything else")
	}
}

func TestHooksStaySilentForACheckoutNotListed(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")

	impostor := h.elsewhere(ownRepo)
	impostor.file(".tmp/claude/"+impostor.session+"/check.py", "print('check')\n")
	before := test.Tree(t, h.store())

	for _, verb := range []string{"prompt", "notice", "stop", "end"} {
		if impostor.hook(verb, map[string]any{"prompt": "count the words of standard input"}) != "" {
			t.Errorf("%s should stay silent for a checkout not listed", verb)
		}
	}

	if test.Tree(t, h.store()) != before {
		t.Error("no hook should write the store for a checkout not listed")
	}
}

func TestTrustLetsAnotherCheckoutWrite(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")

	clone := h.elsewhere(ownRepo)

	clone.refused("this library belongs to other checkouts", "trust", clone.repo)
	h.refused("is no git checkout", "trust", h.home)

	out := h.must("trust", clone.repo)
	if !strings.Contains(out, "trusted "+clone.repo+": that checkout writes this library now") {
		t.Errorf("trust should say which checkout writes now, got %q", out)
	}

	clone.tool("word-split", "Put each word on its own line")
}

func TestAnUnreadableWritersListLetsNoOneWrite(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")

	writers := filepath.Join(h.store(), "writers.json")
	test.WriteFile(t, writers, "not a list\n")

	h.refused("no write lands until writers.json is mended", "update", "word-count", "--summary", "Count words again",
		"--why", "x")

	data, _ := os.ReadFile(writers)
	if string(data) != "not a list\n" {
		t.Error("a damaged list should never be claimed over")
	}

	test.WriteFile(t, writers, `["app/.git"]`+"\n")
	h.refused("which is no clean absolute path", "update", "word-count", "--summary", "Count words again", "--why", "x")
}

func TestARenamedLibraryIsClaimedByItsProject(t *testing.T) {
	h := newHarness(t)
	fdbox := h.elsewhere(otherRepo)
	test.FixtureTree(t, "tools/fdbox-library", fdbox.store())

	h.must("find", "--term", "markdown")

	if test.Exists(t, filepath.Join(fdbox.store(), "writers.json")) {
		t.Error("another project's read should never claim the library")
	}

	fdbox.tool("line-count", "Count the lines of a file quickly")

	if !strings.Contains(fdbox.read("writers.json"), filepath.Join(fdbox.repo, ".git")) {
		t.Error("the library's first write from its own project should claim it")
	}
}
