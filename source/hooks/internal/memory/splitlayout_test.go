package memory_test

import (
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

	identity, err := h.roots().Identity(filepath.Base(filepath.Dir(h.store())))
	if err != nil || identity.Name() != "gitlab.com/example/mika-sdd" {
		t.Errorf("the store should carry the graph repository's repo_id, got %q: %v", identity.Name(), err)
	}
}

func TestACodeRepositoryIsAskedBeforeJoiningAGraphStoreThatDoesNotAdmitIt(t *testing.T) {
	h := newHarness(t)

	graph := newHarnessSharing(h)
	graph.repo = filepath.Join(filepath.Dir(h.repo), "mika-sdd")
	test.WriteFile(t, filepath.Join(graph.repo, ".sdd", "config.yaml"), test.Fixture(t, "sddconfig/graph-repo.yaml"))
	test.WriteFile(t, filepath.Join(graph.repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	graph.git = &test.Git{Top: graph.repo, Commit: "15af1a8c", Branch: "main"}
	graph.must("", "claim", "main")
	id := graph.file("Gate reads Bash only today", "kept by the graph repository")
	store := graph.storeID()

	test.WriteFile(t, filepath.Join(h.repo, ".sdd", "config.yaml"), test.Fixture(t, "sddconfig/project-dir.yaml"))

	question := "agentic-memory: no store is bound to the checkout " + h.repo + " yet: its `.sdd/` names the graph " +
		"repository " + graph.repo + ", whose store does not admit it. Ask the user which store it belongs to, " +
		"then run `agentic-memory bind <store>` or `agentic-memory bind --new` from this checkout.\n" +
		"  " + store + ": gitlab.com/example/mika-sdd; checkouts " + graph.repo

	if got := h.run(unrelatedStart(), "session-start"); !strings.Contains(got.out, question) {
		t.Errorf("SessionStart should tell the agent to ask the user, naming the graph store, got %q", got.out)
	}

	if got := h.run("", "search", "--all-seats", "--term", "Bash"); got.code != 1 || strings.Contains(got.out, id) {
		t.Errorf("the graph store's entries should stay unread while the question stands, got %d %q", got.code, got.out)
	}

	if test.Exists(t, filepath.Join(h.repo, ".git", "agentic-store")) {
		t.Error("an unanswered question should write no pointer")
	}

	h.must("", "bind", store)

	if got := h.run(unrelatedStart(), "session-start"); strings.Contains(got.out, "no store is bound") {
		t.Errorf("a bound code repository should hear no question again, got %q", got.out)
	}

	if !strings.Contains(h.must("", "search", "--all-seats", "--term", "Bash"), id) {
		t.Error("a code repository bound to the graph store should read its entries")
	}
}
