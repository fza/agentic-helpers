package tools_test

import (
	"context"
	"encoding/json"
	"fmt"
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

	h.must("add", "row-count", "--from", h.sized("rows-one", 150), "--summary", "Count the rows of a table",
		"--usage", "python3 the script < file")
	h.must("add", "word-count", "--from", h.sized("words-one", 150), "--summary", "Count the words of standard input",
		"--usage", "python3 the script < file", "--distinct", "words, not rows")
	h.must("add", "line-count", "--from", h.sized("lines-one", 150), "--summary", "Count the lines of a file",
		"--usage", "python3 the script < file", "--distinct", "lines, not words")
	h.must("update", "line-count", "--from", h.sized("lines-two", 150), "--why", "faster")
	h.must("update", "word-count", "--from", h.sized("words-two", 150), "--why", "faster")

	added := h.run("", "add", "row-tally", "--from", h.sized("rows", 300), "--summary", "Tally the rows of a table",
		"--usage", "python3 the script < file", "--distinct", "tally, not count")

	switch {
	case !h.stored("row-count/v1"):
		t.Fatal("a current version should never be pruned, the oldest one included")
	case added.code != 0:
		t.Fatalf("an add needing room should make it, got %d %q", added.code, added.err)
	case h.stored("word-count/v1") || !h.stored("line-count/v1"):
		t.Error("an add needing room for one version should prune the oldest prunable one, and only it")
	}

	h.must("update", "line-count", "--from", h.sized("lines-three", 200), "--why", "faster still")

	if h.stored("line-count/v1") || !h.stored("line-count/v2") {
		t.Error("an update needing room should prune the oldest prunable version, its own tool's current one never")
	}
}

func TestAForkMakesRoomForBothItsVersions(t *testing.T) {
	h := newHarness(t)
	h.limitTools("1")

	other := h.elsewhere(otherRepo)
	other.limitTools("1")
	other.must("add", "row-tally", "--from", other.sized("tally", 300), "--summary", "Tally the rows of a table",
		"--usage", "python3 the script < file")

	h.must("add", "word-count", "--from", h.sized("words-one", 300), "--summary", "Count the words of standard input",
		"--usage", "python3 the script < file", "--distinct", "words, not rows")
	h.must("update", "word-count", "--from", h.sized("words-two", 300), "--why", "faster")

	h.must("update", "row-tally", "--from", h.sized("tally-fork", 100), "--why", "forked to count blank rows")

	if h.stored("word-count/v1") || !h.stored("row-tally/v2") {
		t.Error("a fork should make room for the copy it takes and its own version, pruning the oldest prunable one")
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

// tamperIndex rewrites the store's index.json the way a sandboxed process
// writing the store could.
func (h *harness) tamperIndex(change func(tools map[string]map[string]any)) {
	h.t.Helper()

	var held map[string]any

	err := json.Unmarshal([]byte(h.read("index.json")), &held)
	if err != nil {
		h.t.Fatalf("index.json should decode: %v", err)
	}

	listed := map[string]map[string]any{}
	for name, row := range held["tools"].(map[string]any) {
		listed[name] = row.(map[string]any)
	}

	change(listed)
	held["tools"] = listed

	data, err := json.Marshal(held)
	if err != nil {
		h.t.Fatal(err)
	}

	test.WriteFile(h.t, filepath.Join(h.store(), "index.json"), string(data))
}

func sizeEvery(row map[string]any, size float64) {
	for _, hashes := range row["hashes"].(map[string]any) {
		for _, hash := range hashes.([]any) {
			hash.(map[string]any)["size"] = size
		}
	}
}

func TestAForeignIndexCannotTriggerPruning(t *testing.T) {
	cases := map[string]func(tools map[string]map[string]any){
		"a size past the cap": func(tools map[string]map[string]any) { sizeEvery(tools["row-tally"], 1<<40) },
		"a size within the cap the disk does not back": func(tools map[string]map[string]any) {
			sizeEvery(tools["row-tally"], 900<<10)
		},
		"a version with no folder": func(tools map[string]map[string]any) {
			tools["ghost"] = map[string]any{
				"version": 9, "name": "ghost", "summary": "s", "usage": "u", "files": []string{"g.py"},
				"described": "x", "takers": []string{}, "roles": []string{}, "written": map[string]string{},
				"hashes": map[string]any{"v9": []map[string]any{{"name": "g.py", "sum": "00", "size": 1 << 20}}},
			}
		},
	}

	for name, tamper := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.limitTools("1")

			h.must("add", "word-count", "--from", h.sized("words-one", 200), "--summary", "Count the words of standard input",
				"--usage", "python3 the script < file")
			h.must("update", "word-count", "--from", h.sized("words-two", 200), "--why", "faster")

			other := h.elsewhere(otherRepo)
			other.must("add", "row-tally", "--from", other.sized("tally", 200), "--summary", "Tally the rows of a table",
				"--usage", "python3 the script < file", "--distinct", "rows, not words")
			other.tamperIndex(tamper)

			got := h.run("", "add", "line-count", "--from", h.sized("lines", 100), "--summary", "Count the lines of a file",
				"--usage", "python3 the script < file", "--distinct", "lines, not words")
			if got.code != 0 || !h.stored("word-count/v1") {
				t.Errorf("a write with room on disk should prune nothing, whatever another store's index claims, got %d %q",
					got.code, got.err)
			}
		})
	}
}

func TestAForgedLedgerCannotTriggerPruning(t *testing.T) {
	cases := map[string]func(h *harness) string{
		"a store claiming more than it holds": func(*harness) string { return otherRepo },
		"a key naming no store":               func(*harness) string { return "ghost/store" },
		"a key naming the own store again":    func(h *harness) string { return "./" + h.repoID },
		"a key reaching outside the library": func(h *harness) string {
			test.WriteFile(h.t, filepath.Join(h.home, "outside", "big", "v1", "big.py"), strings.Repeat("x", 2<<20))

			return "../../outside"
		},
	}

	for name, forge := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.limitTools("1")

			h.must("add", "word-count", "--from", h.sized("words-one", 200), "--summary", "Count the words of standard input",
				"--usage", "python3 the script < file")
			h.must("update", "word-count", "--from", h.sized("words-two", 200), "--why", "faster")

			other := h.elsewhere(otherRepo)
			other.must("add", "row-tally", "--from", other.sized("tally", 200), "--summary", "Tally the rows of a table",
				"--usage", "python3 the script < file", "--distinct", "rows, not words")

			key := forge(h)
			test.WriteFile(t, filepath.Join(tools.Root(h.home), ".space.json"),
				fmt.Sprintf(`{%q:{"stored":%d,"held":%d}}`, key, int64(1)<<40, int64(1)<<40))

			h.variables["AGENTIC_TOOLS_SCOPE"] = "project"

			got := h.run("", "add", "line-count", "--from", h.sized("lines", 100), "--summary", "Count the lines of a file",
				"--usage", "python3 the script < file", "--distinct", "lines, not words")
			if got.code != 0 || !h.stored("word-count/v1") {
				t.Errorf("a scoped write with room on disk should prune nothing, whatever the ledger claims, got %d %q",
					got.code, got.err)
			}
		})
	}
}

func TestANegativeSizeCannotDefeatTheLimit(t *testing.T) {
	h := newHarness(t)
	h.limitTools("1")

	other := h.elsewhere(otherRepo)
	other.must("add", "row-tally", "--from", other.sized("tally", 300), "--summary", "Tally the rows of a table",
		"--usage", "python3 the script < file")
	other.tamperIndex(func(tools map[string]map[string]any) { sizeEvery(tools["row-tally"], -(1 << 40)) })

	h.must("add", "word-count", "--from", h.sized("words", 300), "--summary", "Count the words of standard input",
		"--usage", "python3 the script < file", "--distinct", "words, not rows")

	h.refused("no version is left to prune", "add", "line-count", "--from", h.sized("lines", 500), "--summary",
		"Count the lines of a file", "--usage", "python3 the script < file", "--distinct", "lines, not words")
}
