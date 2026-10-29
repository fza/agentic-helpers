package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/tools"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

// sized writes a one-file tool source of about kilobytes into the checkout,
// its first line naming it so no two are byte-identical.
func (h *harness) sized(name string, kilobytes int) string {
	h.t.Helper()

	return h.file("scripts/"+name+".py", "# "+name+"\n"+strings.Repeat("x", kilobytes<<10))
}

func (h *harness) limitTools(megabytes string) {
	h.t.Helper()

	test.WriteFile(h.t, h.settings,
		`{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"tools_total_mb":`+megabytes+`}}}}`)
}

func (h *harness) stored(relative string) bool {
	_, err := os.Stat(filepath.Join(h.store(), filepath.FromSlash(relative)))

	return err == nil
}

func TestAToolPastTheSizeCapIsRefused(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")

	big := h.file("scripts/big.py", strings.Repeat("x", 1<<20+1))

	h.refused("holds more than the 1 MB a tool may hold", "add", "big-tool", "--from", big, "--summary",
		"Probe a reader", "--usage", "python3 big.py")
	h.refused("holds more than the 1 MB a tool may hold", "update", "word-count", "--from", big, "--why", "grew")
}

func TestTheSizeCapIsStatedBeforeAWrite(t *testing.T) {
	h := newHarness(t)

	if help := h.must("--help"); !strings.Contains(help, "A tool holds at most 1 MB") || !strings.Contains(help, "tools_total_mb") {
		t.Errorf("--help should state the per-tool cap and the machine-wide limit:\n%s", help)
	}

	lines := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"

	var listing struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		} `json:"result"`
	}

	stated := map[string]bool{}

	for _, line := range strings.Split(h.must2(lines, "mcp"), "\n") {
		if !strings.Contains(line, `"id":2`) || json.Unmarshal([]byte(line), &listing) != nil {
			continue
		}

		for _, tool := range listing.Result.Tools {
			stated[tool.Name] = strings.Contains(tool.Description, "at most 1 MB")
		}
	}

	for _, name := range []string{"add", "update"} {
		if !stated[name] {
			t.Errorf("the MCP %s tool should state the per-tool cap", name)
		}
	}
}

func TestEveryToolWriteReplyEndsWithTheToolsSpace(t *testing.T) {
	h := newHarness(t)
	other := h.elsewhere(otherRepo)
	other.tool("line-count", "Count the lines of a file quickly")

	from := h.file("scripts/wc.py", "import sys\n")
	added := h.must("add", "word-count", "--from", from, "--summary", "Count the words of standard input",
		"--usage", "python3 wc.py < file")
	updated := h.must("update", "word-count", "--summary", "Count the words of a text on standard input",
		"--why", "clearer")
	forked := h.must("update", "line-count", "--summary", "Count the lines of a file, blank ones too",
		"--why", "blank lines")

	for verb, out := range map[string]string{"add": added, "update": updated, "a forking update": forked} {
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if last := lines[len(lines)-1]; !strings.HasPrefix(last, "space left: tools ") || !strings.HasSuffix(last, " of 500.0 MB") {
			t.Errorf("the %s reply should end with the tools space against tools_total_mb, got %q", verb, out)
		}
	}
}

func TestAFullLibraryPrunesItsOldestPrunableVersion(t *testing.T) {
	h := newHarness(t)
	h.limitTools("1")

	other := h.elsewhere(otherRepo)
	other.limitTools("1")

	other.must("add", "row-tally", "--from", other.sized("tally-one", 200), "--summary", "Tally the rows of a table",
		"--usage", "python3 the script < file")
	h.must("update", "row-tally", "--from", h.sized("tally-fork", 200), "--why", "forked to count blank rows")
	other.must("update", "row-tally", "--from", other.sized("tally-two", 200), "--why", "counts faster")

	h.must("add", "word-count", "--from", h.sized("words", 300), "--summary", "Count the words of standard input",
		"--usage", "python3 the script < file", "--distinct", "words, not rows")

	switch {
	case h.stored("row-tally/v1"):
		t.Error("the oldest prunable version should be pruned to make room")
	case !h.stored("row-tally/v2") || !h.stored("word-count/v1"):
		t.Error("a current version should never be pruned")
	case !other.stored("row-tally/v1"):
		t.Error("a fork's origin should never be pruned, even when its tool moved on")
	}

	h.refused("no version is left to prune", "add", "line-count", "--from", h.sized("lines", 300),
		"--summary", "Count the lines of a file", "--usage", "python3 the script < file", "--distinct", "lines, not words")
}

func TestPruningTakesTheOldestVersionFirst(t *testing.T) {
	h := newHarness(t)
	h.limitTools("1")

	h.must("add", "word-count", "--from", h.sized("words-one", 200), "--summary", "Count the words of standard input",
		"--usage", "python3 the script < file")
	h.must("add", "line-count", "--from", h.sized("lines-one", 200), "--summary", "Count the lines of a file",
		"--usage", "python3 the script < file", "--distinct", "lines, not words")
	h.must("update", "line-count", "--from", h.sized("lines-two", 200), "--why", "faster")
	h.must("update", "word-count", "--from", h.sized("words-two", 200), "--why", "faster")

	h.must("add", "row-tally", "--from", h.sized("rows", 250), "--summary", "Tally the rows of a table",
		"--usage", "python3 the script < file", "--distinct", "rows, not words")

	if h.stored("word-count/v1") || !h.stored("line-count/v1") {
		t.Error("a write needing room for one version should prune the oldest prunable one, and only it")
	}
}

func TestAScopedWriteCountsTheStoresItCannotReach(t *testing.T) {
	h := newHarness(t)
	h.limitTools("1")

	other := h.elsewhere(otherRepo)
	other.must("add", "row-tally", "--from", other.sized("rows", 300), "--summary", "Tally the rows of a table",
		"--usage", "python3 the script < file")

	h.variables["AGENTIC_TOOLS_SCOPE"] = "project"
	h.must("add", "word-count", "--from", h.sized("words", 300), "--summary", "Count the words of standard input",
		"--usage", "python3 the script < file")

	if used := tools.Space(h.home, 1).Used; used < 600<<10 {
		t.Errorf("a scoped write should keep the ledger's records of the stores it cannot reach, got %d bytes used", used)
	}

	h.refused("no version is left to prune", "add", "line-count", "--from", h.sized("lines", 500), "--summary",
		"Count the lines of a file", "--usage", "python3 the script < file", "--distinct", "lines, not words")
}

func TestAPrunedVersionStillRefusesItsFilesAsADuplicate(t *testing.T) {
	h := newHarness(t)
	h.limitTools("1")

	first := h.sized("words-one", 400)
	firstContent, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}

	h.must("add", "word-count", "--from", first, "--summary", "Count the words of standard input", "--usage",
		"python3 the script < file")
	h.must("update", "word-count", "--from", h.sized("words-two", 400), "--why", "faster")
	h.must("add", "line-count", "--from", h.sized("lines", 400), "--summary", "Count the lines of a file",
		"--usage", "python3 the script < file", "--distinct", "lines, not words")

	if h.stored("word-count/v1") {
		t.Fatal("the old version should be pruned to make room")
	}

	again := h.file("again/words-one.py", string(firstContent))
	h.refused("is word-count v1 byte for byte", "add", "word-tally", "--from", again, "--summary",
		"Tally the words of a text", "--usage", "python3 the script < file", "--distinct", "tally")

	err = os.Remove(filepath.Join(h.store(), "index.json"))
	if err != nil {
		t.Fatal(err)
	}

	h.refused("is word-count v1 byte for byte", "add", "word-tally", "--from", again, "--summary",
		"Tally the words of a text", "--usage", "python3 the script < file", "--distinct", "tally")
}

func TestTheSessionStartPassPrunesEveryStore(t *testing.T) {
	h := newHarness(t)

	h.must("add", "word-count", "--from", h.sized("words-one", 600), "--summary", "Count the words of standard input",
		"--usage", "python3 the script < file")
	h.must("update", "word-count", "--from", h.sized("words-two", 600), "--why", "faster")

	tools.Prune(context.Background(), h.home, 1)

	if h.stored("word-count/v1") || !h.stored("word-count/v2") {
		t.Error("the pass should prune the oldest prunable version while the machine is past its limit")
	}

	if figure := tools.Space(h.home, 1); figure.Used < 600<<10 || figure.Used > 601<<10 {
		t.Errorf("the pass should record what is left in the ledger, got %d bytes used", figure.Used)
	}
}

func TestTheSpaceFigureIsTheIndexsRecordedSizes(t *testing.T) {
	h := newHarness(t)
	store := h.elsewhere(otherRepo).store()
	test.FixtureTree(t, "tools/fdbox-library", store)

	tools.Prune(context.Background(), h.home, 500)

	if _, err := os.Stat(filepath.Join(store, "word-count", "v1")); err != nil {
		t.Error("a pass with room under the limit should prune nothing")
	}

	// word-count v1 is prunable, so only the current versions count.
	want := int64(len(test.Fixture(t, "tools/fdbox-library/word-count/v2/word_count.py")) +
		len(test.Fixture(t, "tools/fdbox-library/moved-sentences/v1/moved.py")))

	if got := tools.Space(h.home, 500).Used; got != want {
		t.Errorf("space used should be the recorded size of every version neither prunable nor gone, got %d, want %d", got, want)
	}

	grown := filepath.Join(store, "word-count", "v2", "word_count.py")
	test.WriteFile(t, grown, strings.Repeat("y", 64<<10))

	tools.Prune(context.Background(), h.home, 500)

	if got := tools.Space(h.home, 500).Used; got != want {
		t.Errorf("space should come from the sizes the index recorded, never from the files on disk, got %d", got)
	}
}
