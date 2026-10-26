package grounding_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/grounding"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

// splitProject is a code repository `mika` whose `project_dir` names the graph
// repository `mika-sdd` beside it, which keeps its entries under `graph` and
// sets an `area-` listing prefix. The code repository holds a stray, empty
// `.sdd/graph`. It returns the env and the graph repository's root.
func splitProject(t *testing.T) (grounding.Env, string) {
	t.Helper()

	workspace := t.TempDir()
	code := filepath.Join(workspace, "mika")
	graph := filepath.Join(workspace, "mika-sdd")

	test.WriteFile(t, filepath.Join(code, ".sdd", "config.yaml"), test.Fixture(t, "sddconfig/project-dir.yaml"))
	mkdir(t, filepath.Join(code, ".sdd", "graph"))
	test.WriteFile(t, filepath.Join(graph, ".sdd", "config.yaml"), test.Fixture(t, "sddconfig/graph-repo.yaml"))
	test.WriteFile(t, filepath.Join(graph, ".sdd", "grounding.yaml"), test.Fixture(t, "grounding/area-prefix.yaml"))
	mkdir(t, filepath.Join(graph, "graph"))

	return grounding.Env{ProjectDir: code, WorkingDir: code, Home: t.TempDir(), Runner: &test.Runner{}}, graph
}

func TestASplitLayoutReadsTheGraphRepository(t *testing.T) {
	env, graph := splitProject(t)
	ground(t, env)

	passes(t, run(t, env, "gate", ask("s1")))

	ledgers, err := os.ReadDir(filepath.Join(graph, ".sdd", "tmp", "grounding-gate"))
	if err != nil || len(ledgers) == 0 {
		t.Errorf("the ledger should live in the graph repository's .sdd, got: %v", err)
	}

	test.WriteFile(t, filepath.Join(env.ProjectDir, ".sdd", "graph", "2026", "10", "26-090000-d-tac-new.md"), test.Fixture(t, "drafts/fact.md"))
	passes(t, run(t, env, "gate", ask("s1")))

	test.WriteFile(t, filepath.Join(graph, "graph", "2026", "10", "26-090000-d-tac-new.md"), test.Fixture(t, "drafts/fact.md"))
	refuses(t, run(t, env, "gate", ask("s1")), "neither read")
}
