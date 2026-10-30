package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/tools"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestTheFirstWriteBindsTheCheckoutAndItsWorktrees(t *testing.T) {
	h := newHarness(t)

	h.must("list")

	if test.Exists(t, h.roots().Stores()) {
		t.Error("a read should create no store")
	}

	h.tool("word-count", "Count the words of standard input")

	worktree := *h
	worktree.repo = filepath.Join(filepath.Dir(h.repo), "app-worktree")
	worktree.git = &test.Git{Top: worktree.repo, Commit: "15af1a8c", Branch: "feature"}
	h.worktrees[worktree.repo] = h.repo
	test.WriteFile(t, filepath.Join(worktree.repo, ".sdd", "config.yaml"), "repo_id: "+ownRepo+"\n")

	worktree.tool("word-split", "Put each word on its own line")

	if !test.Exists(t, filepath.Join(h.store(), "word-split", "current")) {
		t.Error("a worktree should write the main checkout's library")
	}
}

func TestASecondCloneWritesNothingUntilBound(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")
	h.tool("word-split", "Put each word on its own line")

	clone := h.elsewhere(ownRepo)
	from := clone.file("evil.py", "evil\n")
	before := test.Tree(t, h.store())

	writes := [][]string{
		{"add", "evil-tool", "--from", from, "--summary", "A tool from elsewhere", "--usage", "u"},
		{"update", "word-count", "--summary", "Count words, rewritten elsewhere", "--why", "x"},
		{"reindex"},
	}

	for _, args := range writes {
		clone.refused("this checkout is bound to no store yet: no store is bound to the checkout "+clone.repo+
			" yet: it is another checkout of a repository a store already holds", args...)
	}

	clone.refused("takes only into its own .tmp/", "take", "word-count")

	clone.file(".tmp/keep", "")

	if !strings.Contains(clone.must("take", "word-count"), filepath.Join(clone.repo, ".tmp", "tools")) {
		t.Error("a clone bound to no store should still take a copy into its own .tmp/")
	}

	clone.file(".tmp/claude/"+clone.session+"/line-count.py", "print(1)\n")

	for _, verb := range []string{"prompt", "notice", "stop", "end"} {
		if clone.hook(verb, map[string]any{"prompt": "count the words of standard input"}) != "" {
			t.Errorf("%s should stay silent for a clone bound to no store", verb)
		}
	}

	if test.Tree(t, h.store()) != before {
		t.Error("a clone bound to no store should leave the library byte-identical")
	}

	if listed, _ := os.ReadDir(h.roots().Stores()); len(listed) != 1 {
		t.Errorf("a clone bound to no store should create no folder among the stores, got %d", len(listed))
	}

	checkout, err := home.Inspect(context.Background(), clone.repo, test.GitDirOf{}, nil)
	if err != nil {
		t.Fatalf("inspecting the clone: %v", err)
	}

	_, err = home.Resolver{Roots: h.roots()}.Bind(context.Background(), checkout, filepath.Base(filepath.Dir(h.store())))
	if err != nil {
		t.Fatalf("binding the clone: %v", err)
	}

	clone.tool("line-count", "Count the lines of standard input")

	if !test.Exists(t, filepath.Join(h.store(), "line-count", "current")) {
		t.Error("a bound clone should write the store it is bound to")
	}
}

func TestJoinMovesEveryToolAndRefusesASharedName(t *testing.T) {
	from, into := t.TempDir(), t.TempDir()
	test.WriteFile(t, filepath.Join(from, "word-count", "current"), "1\n")
	test.WriteFile(t, filepath.Join(into, "line-count", "current"), "1\n")
	test.WriteFile(t, filepath.Join(into, "index.json"), "{}")

	err := tools.Join(context.Background(), from, into)
	if err != nil {
		t.Fatalf("joining two libraries: %v", err)
	}

	for _, moved := range []string{"word-count/current", "line-count/current"} {
		if !test.Exists(t, filepath.Join(into, moved)) {
			t.Errorf("the joined library should hold %s", moved)
		}
	}

	if test.Exists(t, filepath.Join(into, "index.json")) {
		t.Error("the joined library's index should go, to be rebuilt from its folders")
	}

	test.WriteFile(t, filepath.Join(from, "line-count", "current"), "2\n")

	err = tools.Join(context.Background(), from, into)
	if err == nil || !strings.Contains(err.Error(), "both libraries hold the tools line-count") {
		t.Errorf("a tool name both libraries hold should refuse the join, got %v", err)
	}

	if !test.Exists(t, filepath.Join(from, "line-count", "current")) {
		t.Error("a refused join should move nothing")
	}
}

func TestNothingLandsUnderTheClaudeHome(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")
	h.hook("prompt", map[string]any{"prompt": "count words"})

	for _, legacy := range []string{".claude", ".cache"} {
		_, err := os.Stat(filepath.Join(h.home, legacy))
		if err == nil {
			t.Errorf("the tool library should keep nothing under ~/%s", legacy)
		}
	}
}

func TestANewerHomeIsRefusedAndKeptAsItIs(t *testing.T) {
	h := newHarness(t)
	test.WriteFile(t, filepath.Join(h.roots().Data, "format.json"), `{"format": 9}`)
	before := test.Tree(t, h.roots().Data)
	from := h.file("word-count.py", "print(1)\n")

	for _, args := range [][]string{
		{"add", "word-count", "--from", from, "--summary", "Count the words of standard input", "--usage", "u"},
		{"list"},
	} {
		h.refused("has format 9; this release reads up to 1", args...)
	}

	h.file(".tmp/claude/"+h.session+"/line-count.py", "print(1)\n")

	for _, verb := range []string{"prompt", "notice", "stop", "end"} {
		if h.hook(verb, map[string]any{"prompt": "count the words of standard input"}) != "" {
			t.Errorf("%s should stay silent on a home of a newer format", verb)
		}
	}

	if test.Tree(t, h.roots().Data) != before {
		t.Error("a home of a newer format should be left byte-identical")
	}
}
