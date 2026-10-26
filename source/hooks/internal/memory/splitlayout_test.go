package memory_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

// TestASplitLayoutLinksIntoTheGraphRepository runs memory in a code repository
// whose `project_dir` names a graph repository beside it, keeping its entries
// under `graph` and its entries in German.
func TestASplitLayoutLinksIntoTheGraphRepository(t *testing.T) {
	h := newHarness(t)
	graph := filepath.Join(filepath.Dir(h.repo), "mika-sdd")

	test.WriteFile(t, filepath.Join(h.repo, ".sdd", "config.yaml"), test.Fixture(t, "sddconfig/project-dir.yaml"))
	test.WriteFile(t, filepath.Join(graph, ".sdd", "config.yaml"), test.Fixture(t, "sddconfig/graph-repo.yaml"))
	test.WriteFile(t, filepath.Join(graph, "graph", "2026", "10", "25-100000-d-tac-act.md"), "---\ntype: decision\n---\n")

	claimed := h.must("", "claim", "main")
	if !strings.Contains(claimed, "entries in de") {
		t.Errorf("entries should take the graph repository's language, got:\n%s", claimed)
	}

	id := h.file("A finding linking into the graph repository", "body", "--link", activeDecision)
	if shown := h.must("", "show", id); !strings.Contains(shown, "links:\n  → "+activeDecision) {
		t.Errorf("a graph id held by the graph repository should link, got:\n%s", shown)
	}

	store := filepath.Join(h.home, ".claude", "agentic-memory", "gitlab.com", "example", "mika-sdd")

	_, err := os.Stat(store)
	if err != nil {
		t.Errorf("the store should be keyed by the graph repository's repo_id: %v", err)
	}
}
