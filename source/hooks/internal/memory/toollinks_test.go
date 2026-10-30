package memory_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

// ownTools is the tool library of the store the harness's checkout works on,
// seeded with the fixture library.
func (h *harness) ownTools() string {
	h.t.Helper()

	library := filepath.Join(filepath.Dir(h.store()), home.Tools)
	test.FixtureTree(h.t, "tools/fdbox-library", library)

	return library
}

// takeCount is the takes the library's index counts for word-count.
func takeCount(t *testing.T, library string) int {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(library, "index.json"))
	if err != nil {
		t.Fatalf("a read of a tool link should leave the library indexed: %v", err)
	}

	var index struct {
		Tools map[string]struct {
			Takes int `json:"takes"`
		} `json:"tools"`
	}

	err = json.Unmarshal(data, &index)
	if err != nil || index.Tools["word-count"].Takes == 0 {
		t.Fatalf("the index should count the fixture's takes: %v", err)
	}

	return index.Tools["word-count"].Takes
}

func TestAnEntryNamingAToolCarriesItAndShowTakesIt(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	library := h.ownTools()

	first := h.file("Count words with the library", "the library counts them", "--tool", "word-count")
	second := h.file("Word counts for the changelog", "counted the same way", "--tool", "word-count")

	line := h.must("", "search", "--term", "Count words")
	if !strings.Contains(line, "→ tool word-count") || strings.Contains(line, "tool:") {
		t.Errorf("a line should name the tool its entry links, and only as a tool, got:\n%s", line)
	}

	if !strings.Contains(h.must("", "brief"), "→ tool word-count") {
		t.Error("a brief line should keep the tool its entry links, never folding it among ended links")
	}

	takes := takeCount(t, library)
	shown := h.must("", "show", first)
	copied := filepath.Join(".tmp", "tools", "word-count", "v2")

	switch {
	case !strings.Contains(shown, "tool (registered by other agents):\n  word-count v2  taken: "+copied):
		t.Errorf("show should take the tool's current version under the banner, got:\n%s", shown)
	case !strings.Contains(shown, "    usage: "):
		t.Errorf("show should print the taken tool's usage, got:\n%s", shown)
	case !strings.Contains(shown, "linked to tool word-count") && !strings.Contains(shown, "→ tool word-count"):
		t.Errorf("show should name the linked tool, got:\n%s", shown)
	}

	_, err := os.Stat(filepath.Join(h.repo, copied, "word_count.py"))
	if err != nil {
		t.Errorf("show should copy the tool's files into the checkout: %v", err)
	}

	if takeCount(t, library) != takes {
		t.Error("a take on show should leave the tool's takers as they were")
	}

	again := h.must("", "show", second)
	if !strings.Contains(again, "  word-count v2 (seen)  taken: "+copied) || strings.Contains(again, "usage: ") {
		t.Errorf("a tool whose usage the context holds should come back as a pointer with its path, got:\n%s", again)
	}

	var answer struct {
		Entries []struct {
			Tools []struct {
				Name    string `json:"name"`
				Version int    `json:"version"`
				Path    string `json:"path"`
				Usage   string `json:"usage"`
				Seen    bool   `json:"seen"`
			} `json:"tools"`
		} `json:"entries"`
	}

	err = json.Unmarshal([]byte(h.must("", "show", first, "--json", "--again")), &answer)
	if err != nil || len(answer.Entries) != 1 || len(answer.Entries[0].Tools) != 1 {
		t.Fatalf("JSON show should carry the tool section, got %+v, %v", answer, err)
	}

	tool := answer.Entries[0].Tools[0]
	if tool.Name != "word-count" || tool.Version != 2 || tool.Path != copied || tool.Usage == "" || tool.Seen {
		t.Errorf("JSON show should carry the tool's name, version, path and usage, got %+v", tool)
	}
}

func TestAToolLinkResolvesOrRefuses(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.ownTools()

	got := h.run("body", "new", "--title", "Names a missing tool", "--topic", "gate", "--tool", "nosuch")
	if got.code == 0 || !strings.Contains(got.err, "no tool nosuch") {
		t.Errorf("a tool resolving to nothing should refuse the filing, got %d %q", got.code, got.err)
	}

	id := h.file("Counting words by hand", "slow")

	if got := h.run("", "link", id, "--tool", "nosuch"); got.code == 0 {
		t.Error("linking a tool resolving to nothing should be refused")
	}

	if out := h.must("", "link", id, "--tool", "word-count"); strings.TrimSpace(out) != "linked" {
		t.Errorf("link should say the tool link landed without echoing it, got %q", out)
	}

	if !strings.Contains(h.must("", "search", "--term", "Counting words"), "→ tool word-count") {
		t.Error("a tool linked after filing should show on the entry's line")
	}

	if got := h.run("", "link", id, "--tool", "word-count", "--tool", "moved-sentences"); got.code == 0 {
		t.Error("link should take one tool at a time")
	}

	other := newHarnessSharing(h)
	other.must("", "claim", "second")
	other.must("", "link", id, "--tool", "moved-sentences")

	if !strings.Contains(h.must("", "brief"), "linked to tool moved-sentences") {
		t.Error("the filing seat's brief should list a tool link another seat made")
	}

	h.must("", "unlink", id, "--tool", "moved-sentences")

	if got := h.run("", "unlink", id, "--tool", "moved-sentences"); got.code == 0 {
		t.Error("unlinking a tool the entry does not name should be refused")
	}

	h.must("", "unlink", id, "--tool", "word-count")

	if strings.Contains(h.must("", "search", "--term", "Counting words"), "→ tool") {
		t.Error("an unlinked tool should leave the entry's line")
	}

	h.limitSpace(`"tools_enabled":false`)

	got = h.run("body", "new", "--title", "Names a tool while off", "--topic", "gate", "--tool", "word-count")
	if got.code == 0 || !strings.Contains(got.err, "tools_enabled") {
		t.Errorf("naming a tool while agentic-tools is off should be refused naming the option, got %d %q", got.code, got.err)
	}
}

func TestAToolLinkFollowsAMergeAndSaysWhenTheToolIsGone(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	library := h.ownTools()

	err := os.CopyFS(filepath.Join(library, "tally"), os.DirFS(filepath.Join(library, "word-count")))
	if err != nil {
		t.Fatalf("copying a tool: %v", err)
	}

	id := h.file("Sentences and tallies", "both tools", "--tool", "moved-sentences", "--tool", "tally")

	for _, path := range []string{filepath.Join(library, "tally"), filepath.Join(library, "moved-sentences"),
		filepath.Join(library, "index.json")} {
		err = os.RemoveAll(path)
		if err != nil {
			t.Fatalf("removing %s: %v", path, err)
		}
	}

	test.WriteFile(t, filepath.Join(library, "moved-sentences", "merged"), "word-count\n")

	line := h.must("", "search", "--term", "Sentences and tallies")
	if !strings.Contains(line, "→ tool moved-sentences merged into word-count") || !strings.Contains(line, "→ tool tally gone") {
		t.Errorf("a line should say a linked tool was merged or is gone, got:\n%s", line)
	}

	shown := h.must("", "show", id)
	if !strings.Contains(shown, "  word-count v2  taken: ") || !strings.Contains(shown, "  tally: not taken: gone from the library") {
		t.Errorf("show should take a merged tool's survivor and say a gone one was not taken, got:\n%s", shown)
	}
}
