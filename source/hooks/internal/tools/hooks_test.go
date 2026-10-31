package tools_test

import (
	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/test"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheOfferNamesUpToThreeNearOwnToolsOncePerSession(t *testing.T) {
	h := newHarness(t)
	for _, name := range []string{"markdown-words", "markdown-lines", "markdown-links", "markdown-tables"} {
		h.tool(name, "Count markdown section words lines links tables", "--distinct", "counts "+name)
	}

	h.tool("zebra-sort", "Sort zebras by stripes")

	other := h.elsewhere(otherRepo)
	other.tool("markdown-other", "Count markdown section words lines links tables", "--distinct", "elsewhere")

	prompt := map[string]any{"prompt": "count markdown section words lines links tables"}

	first := h.hook("prompt", prompt)
	if !strings.HasPrefix(first, "tool library: near this prompt: ") || strings.Count(first, "markdown-") != 3 ||
		!strings.HasSuffix(strings.TrimSpace(first), ". `agentic-tools take <name>` copies one") {
		t.Errorf("the offer should name three near own tools and the take command, got %q", first)
	}

	if strings.Contains(first, "markdown-other") || strings.Contains(first, "zebra") || strings.Contains(first, "Count") {
		t.Errorf("the offer should name near own tools only, and no summary, got %q", first)
	}

	second := h.hook("prompt", prompt)
	if strings.Count(second, "markdown-") != 1 || strings.Contains(second, "zebra") || anyOf(second, strings.Split(strings.TrimSpace(first), " ")[5:8]) {
		t.Errorf("each tool should be offered once per session, got %q", second)
	}

	if h.hook("prompt", prompt) != "" {
		t.Error("nothing should be added once every near tool was offered")
	}

	if h.hook("prompt", map[string]any{"prompt": "unrelated weather forecast"}) != "" {
		t.Error("nothing should be added when no tool is near")
	}
}

func anyOf(text string, words []string) bool {
	for _, word := range words {
		if strings.Contains(text, strings.Trim(word, ",.")) {
			return true
		}
	}

	return false
}

func TestNoticesNameNewerVersionsOnce(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")
	h.must("take", "word-count")

	if h.hook("notice", nil) != "" {
		t.Error("a holding at the current version should hear nothing")
	}

	h.must("update", "word-count", "--summary", "Count the words of standard input quickly", "--why", "faster")

	got := h.hook("notice", nil)
	if !strings.Contains(got, "tool library: newer versions of `word-count`. `agentic-tools take word-count` copies them") ||
		strings.Contains(got, "faster") || strings.Contains(got, "quickly") {
		t.Errorf("a notice should name the newer tool and the command only, got %q", got)
	}

	if h.hook("notice", nil) != "" {
		t.Error("a version should be noticed once")
	}
}

func TestTheNoticeHookReadsNoStoreWhenNothingMoved(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")
	h.must("take", "word-count")
	h.hook("notice", nil)

	folder := filepath.Join(h.store(), "word-count")
	index := filepath.Join(h.store(), "index.json")
	info, _ := os.Stat(index)
	before, _ := os.ReadFile(index)

	_ = os.MkdirAll(filepath.Join(folder, "v2"), 0o700)
	_ = os.WriteFile(filepath.Join(folder, "v2", "tool.json"), []byte(`{"name":"word-count","summary":"s","usage":"u"}`), 0o600)
	_ = os.WriteFile(filepath.Join(folder, "v2", "w.py"), []byte("w"), 0o600)
	_ = os.WriteFile(filepath.Join(folder, "current"), []byte("2\n"), 0o600)

	// The index now says v2, at the size and time it had: only a hook that
	// reads it would know.
	moved := strings.Replace(string(before), `"version":1`, `"version":2`, 1)
	_ = os.WriteFile(index, []byte(moved), 0o600)
	_ = os.Chtimes(index, info.ModTime(), info.ModTime())

	if h.hook("notice", nil) != "" {
		t.Error("with the index unmoved by size and time the hook should read no further")
	}

	h.must("reindex")

	if !strings.Contains(h.hook("notice", nil), "newer versions of `word-count`") {
		t.Error("once the index moves the newer version should be noticed")
	}
}

func TestAForkHearsOfItsOriginsNewerVersions(t *testing.T) {
	h := newHarness(t)
	other := h.elsewhere(otherRepo)
	other.tool("line-count", "Count the lines of a file quickly")

	h.must("update", "line-count", "--summary", "Count the lines of a file, blank ones too", "--why", "blank lines")
	h.must("take", "line-count")

	other.must("update", "line-count", "--summary", "Count the lines of a file very quickly", "--why", "speed")

	got := h.hook("notice", nil)
	if !strings.Contains(got, "tool library: `line-count` in "+otherRepo+" has v2; yours forked at v1") {
		t.Errorf("a fork's holder should hear of the origin's newer version, got %q", got)
	}

	if h.hook("notice", nil) != "" {
		t.Error("the origin's version should be noticed once")
	}
}

func TestStopRecordsAndThePromptDeliversNudgesOnce(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")
	h.must("take", "word-count")

	scratch := filepath.Join(h.repo, ".tmp", "claude", h.session)
	h.file(".tmp/claude/"+h.session+"/check.py", "print('check')\n")
	h.file(".tmp/claude/"+h.session+"/notes.txt", "not a script\n")
	h.file(".tmp/claude/"+h.session+"/again.sh", "# word-count\nprint('word-count')\n# Count the words of standard input\n")
	h.file(".tmp/claude/"+h.session+"/word-count.py", "# a different script under a tool's file name\n")

	copied := filepath.Join(h.store(), "copies", "word-count", "v1", "word-count.py")
	_ = os.WriteFile(copied, []byte("# changed\n"), 0o600)

	out := h.hook("stop", nil)
	if out != "" {
		t.Errorf("stop should never block or speak, got %q", out)
	}

	got := h.hook("prompt", map[string]any{"prompt": "anything"})

	if !strings.Contains(got, "tool library: your scratch holds scripts the library lacks: check.py. "+
		"`agentic-tools add <name> --from <path> --summary '...' --usage '...'` registers one") {
		t.Errorf("the prompt should deliver the script nudge, got %q", got)
	}

	if strings.Contains(got, "notes.txt") || strings.Contains(got, "again.sh") || strings.Contains(got, "word-count.py") {
		t.Errorf("only scripts no tool holds should be named, got %q", got)
	}

	if !strings.Contains(got, "tool library: you changed your copy of `word-count`: "+
		"`agentic-tools update word-count --from "+filepath.Dir(copied)+" --why '...'`") {
		t.Errorf("the prompt should deliver the changed-copy nudge, got %q", got)
	}

	h.hook("stop", nil)

	again := h.hook("prompt", map[string]any{"prompt": "anything"})
	if strings.Contains(again, "tool library: you") {
		t.Errorf("each nudge should be delivered once, got %q", again)
	}

	_ = scratch
}

func TestANudgeOpensTheNextCall(t *testing.T) {
	h := newHarness(t)
	h.file(".tmp/claude/"+h.session+"/check.py", "print('check')\n")
	h.tool("word-count", "Count the words of standard input")
	h.hook("stop", nil)

	if !strings.HasPrefix(h.must("list"), "tool library: your scratch holds scripts the library lacks: check.py.") {
		t.Error("a pending nudge should open the next call")
	}

	if strings.Contains(h.must("list"), "check.py") {
		t.Error("a nudge should open one call only")
	}
}

func TestAMachineWithoutALibraryHearsNothing(t *testing.T) {
	h := newHarness(t)
	h.file(".tmp/claude/"+h.session+"/check.py", "print('check')\n")

	for _, verb := range []string{"prompt", "notice", "stop", "end"} {
		if h.hook(verb, map[string]any{"prompt": "count words"}) != "" {
			t.Errorf("%s should stay silent on a machine holding no library", verb)
		}
	}

	if test.Exists(t, h.roots().Data) {
		t.Error("no hook should create the library")
	}
}

func TestTheTurnEndReadsTheSeatsScratchFolderToo(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")

	memory := h.roots().Store(test.StoreOf(t, h.roots(), h.repo, test.GitDirOf{}, nil), home.Memory)
	test.ClaimSeatByID(t, memory, "s-abc123", "main", h.session)
	h.file(".tmp/claude/main/probe.py", "print('probe')\n")
	h.file(".tmp/claude/"+h.session+"/check.py", "print('check')\n")

	h.hook("stop", nil)

	got := h.hook("prompt", map[string]any{"prompt": "anything"})
	if !strings.Contains(got, "scripts the library lacks: probe.py, check.py.") {
		t.Errorf("the nudge should name the seat folder's scripts and the session folder's, got %q", got)
	}
}
