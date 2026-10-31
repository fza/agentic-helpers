package memory_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestAFilingKeepsTheDotOfADotFolder(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	test.WriteFile(t, filepath.Join(h.repo, ".tmp", "x"), "scratch\n")

	id := h.file("Gate config lives in the graph folder", "see .sdd/config.yaml.", "--kind", "todo",
		"--about", ".tmp/x", "--until", "gone:.tmp/x", "--until", "changed:.tmp/x")

	joined := strings.Join(storePayloads(t, h), "\n")
	for _, want := range []string{
		`"about":[".tmp/x"]`, `"gone:.tmp/x"`, `"changed:.tmp/x"`, `"until_hashes":{".tmp/x":`, `"path":".sdd/config.yaml"`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("a declared path, an end and an anchor should keep the dot of a dot folder, missing %s in:\n%s", want, joined)
		}
	}

	h.must("", "claim", "main")

	if out := h.must("", "show", id); strings.Contains(out, "end reached") {
		t.Errorf("an entry ending when a file that exists is gone should stay live:\n%s", out)
	}
}
