package tools_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/tools"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestTheLedgerMatchesTheFilesAfterEveryWriteAndPrune(t *testing.T) {
	h := newHarness(t)
	h.limitTools("1")

	store := h.store()
	test.FixtureTree(t, "tools/fdbox-library", store)

	steps := []struct {
		name string
		run  func()
	}{
		{name: "a take", run: func() { h.must("take", "word-count") }},
		{name: "an update", run: func() {
			h.must("update", "word-count", "--from", h.sized("words-three", 600), "--usage", "python3 the script < file",
				"--why", "faster")
		}},
		{name: "a second update", run: func() {
			h.must("update", "word-count", "--from", h.sized("words-four", 300), "--why", "smaller")
		}},
		{name: "an add pruning to make room", run: func() {
			h.must("add", "line-count", "--from", h.sized("lines", 500), "--summary", "Count the lines of a file",
				"--usage", "python3 the script < file", "--distinct", "lines, not words")
		}},
		{name: "the SessionStart pass", run: func() { tools.Prune(context.Background(), h.roots(), 1) }},
	}

	for _, step := range steps {
		step.run()

		mismatches := test.LedgerMismatches(t, store)
		if len(mismatches) > 0 {
			t.Errorf("the ledger should match the store's files after %s: %s", step.name, strings.Join(mismatches, "; "))
		}
	}

	if h.stored("word-count/v3") {
		t.Error("the add should have pruned the prunable versions, which the steps rely on")
	}
}

func TestAnEarlierLibraryMovesToJournalsAndSlots(t *testing.T) {
	h := newHarness(t)
	store := h.store()
	test.FixtureTree(t, "tools/fdbox-library", store)
	test.WriteFile(t, filepath.Join(store, "sessions", h.session+".json"), test.Fixture(t, "tools/legacy-sessions/session.json"))

	seen := filepath.Join(h.roots().SharedCache(home.Tools), "seen", "4242-1790000000.json")
	test.WriteFile(t, seen, test.Fixture(t, "tools/legacy-sessions/seen.json"))

	before := h.dumped("word-count/takes.jnl")
	if before != "" {
		t.Fatal("the fixture should hold the takes as JSON lines")
	}

	out := h.must("list")
	if !strings.Contains(out, "word-count") {
		t.Fatalf("an earlier library should read once moved, got:\n%s", out)
	}

	if !strings.Contains(out, "word-count has a newer version, v2") {
		t.Errorf("a session's pending nudge should survive the move, got:\n%s", out)
	}

	h.must("take", "word-count")

	format, _ := os.ReadFile(filepath.Join(store, "format.json"))
	if !strings.Contains(string(format), `"format":2`) {
		t.Errorf("the library should record its format once moved, got %s", format)
	}

	for _, legacy := range []string{"word-count/history.jsonl", "word-count/takes.jsonl", "moved-sentences/history.jsonl", "sessions"} {
		if h.stored(legacy) {
			t.Errorf("the move should leave no %s", legacy)
		}
	}

	if strings.Count(h.dumped("word-count/takes.jnl"), "\n") != strings.Count(test.Fixture(t, "tools/fdbox-library/word-count/takes.jsonl"), "\n")+1 {
		t.Error("the takes journal should hold every earlier take and the new one")
	}

	if !strings.Contains(h.dumped("word-count/history.jnl"), `"record":"history"`) {
		t.Error("the history should move into a journal")
	}

	h.must("find", "--term", "word")

	_, err := os.Stat(seen)
	if !errors.Is(err, os.ErrNotExist) {
		t.Error("the seen files of an earlier release should be taken in and removed")
	}
}

func TestALibraryOfANewerFormatIsRefused(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")
	test.WriteFile(t, filepath.Join(h.store(), "format.json"), `{"format":9}`)

	got := h.run("", "add", "line-count", "--from", h.file("lines.py", "print(1)\n"), "--summary",
		"Count the lines of a file", "--usage", "python3 the script < file")
	if got.code == 0 || !strings.Contains(got.err, "has format 9; this release reads up to 2") {
		t.Errorf("a write to a library of a newer format should be refused naming both, got %d %q", got.code, got.err)
	}

	if out := h.must("list"); strings.Contains(out, "word-count") {
		t.Errorf("a library of a newer format should not be read, got:\n%s", out)
	}
}

func TestAnotherProjectsLibraryOfANewerFormatIsLeftUnread(t *testing.T) {
	h := newHarness(t)
	other := h.elsewhere("github.com/acme/newer")
	other.tool("only-there", "A tool a newer release keeps")
	test.WriteFile(t, filepath.Join(other.store(), "format.json"), `{"format":9}`)

	if out := h.must("find", "--term", "only-there"); strings.Contains(out, "only-there") {
		t.Errorf("another project's library of a newer format should be left unread, got:\n%s", out)
	}

	h.refused("only-there", "take", "only-there")
}
