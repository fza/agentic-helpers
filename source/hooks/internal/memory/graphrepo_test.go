package memory_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/sddcaps"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const graphRepo = "gitlab.com/acme/app-sdd"

// graphRepoLayout turns the harness checkout into sdd's graph_repo layout: its
// `.sdd/` names graph_repo, keeps a `project_dir` for older sdd builds and the
// GRAPH-ELSEWHERE guard. The checkout `project_dir` names holds a graph in
// French, sdd's own copy one in German, so a claim says which graph was read.
// It returns the copy's folder; the fake sdd answers `sdd repo path` with it.
func graphRepoLayout(t *testing.T, h *harness) string {
	t.Helper()

	test.WriteFile(t, filepath.Join(h.repo, ".sdd", "config.yaml"),
		"graph_repo: "+graphRepo+"\nproject_dir: ../app-sdd\ngraph_dir: .sdd/GRAPH-ELSEWHERE.md\n")
	test.WriteFile(t, filepath.Join(h.repo, ".sdd", "GRAPH-ELSEWHERE.md"), "# The graph lives in "+graphRepo+"\n")

	stale := filepath.Join(filepath.Dir(h.repo), "app-sdd")
	test.WriteFile(t, filepath.Join(stale, ".sdd", "config.yaml"), "graph_dir: graph\nrepo_id: "+graphRepo+"\nlanguage: fr\n")
	test.WriteFile(t, filepath.Join(stale, "graph", "2026", "10", "25-100000-d-tac-old.md"), "---\n---\n")

	copied := filepath.Join(h.home, ".local", "share", "sdd", "repos", filepath.FromSlash(graphRepo))
	test.WriteFile(t, filepath.Join(copied, ".sdd", "config.yaml"), "graph_dir: graph\nrepo_id: "+graphRepo+"\nlanguage: de\n")
	test.WriteFile(t, filepath.Join(copied, "graph", "2026", "10", "25-100000-d-tac-old.md"), "---\n---\n")

	h.graph.Clones = map[string]string{graphRepo: copied}

	return copied
}

// cachedProbe keeps its answers, as the installed probe does, so a test counts
// only the sdd processes memory itself starts.
func cachedProbe(t *testing.T, runner *test.Runner) *sddcaps.Probe {
	t.Helper()

	binary := filepath.Join(t.TempDir(), "sdd")
	test.WriteFile(t, binary, "#!/bin/sh\n")

	return &sddcaps.Probe{Runner: runner, CacheDir: t.TempDir(), LookPath: func(string) (string, error) { return binary, nil }}
}

func TestAGraphRepoCheckoutReadsSddsCopy(t *testing.T) {
	h := newHarness(t)
	copied := graphRepoLayout(t, h)
	h.sdd = cachedProbe(t, &test.Runner{})

	if out := h.must("", "claim", "main"); !strings.Contains(out, ", entries in de, ") {
		t.Errorf("the graph's language should come from sdd's copy, not the project_dir checkout, got:\n%s", out)
	}

	waiting := h.file("Owner decision waiting for capture", "body", "--kind", "todo", "--item", "settle it", "--until", "captured")
	h.must(h.toolCall(), "tool")

	test.WriteFile(t, filepath.Join(copied, "graph", "2026", "10", "25-130000-d-tac-two.md"), "---\n---\n")

	out := toolContext(h.must(h.toolCall(), "tool"))
	if !strings.Contains(out, "graph gained 20261025-130000-d-tac-two; open todos waiting for a capture: "+h.label(waiting)) {
		t.Errorf("an entry sdd writes into its copy should reach the capture notice, the project_dir checkout unchanged:\n%s", out)
	}
}

func TestTheCopysPathIsAskedOnce(t *testing.T) {
	h := newHarness(t)
	graphRepoLayout(t, h)
	runner := &test.Runner{}
	h.sdd = cachedProbe(t, runner)
	h.must("", "claim", "main")

	asked, probed := h.graph.PathCalls(), len(runner.Calls)
	if asked != 1 {
		t.Fatalf("the first process should ask sdd where its copy lives, asked %d times", asked)
	}

	h.must("", "who")
	h.must(h.toolCall(), "tool")

	if h.graph.PathCalls() != asked || len(runner.Calls) != probed {
		t.Errorf("a process finding graph-clone.json and its folder should start no sdd process, asked %d, probed %d more",
			h.graph.PathCalls()-asked, len(runner.Calls)-probed)
	}
}

func TestTheCopysPathIsAskedAgain(t *testing.T) {
	cases := []struct {
		name   string
		change func(t *testing.T, h *harness, copied string)
	}{
		{name: "graph_repo changes", change: func(t *testing.T, h *harness, copied string) {
			t.Helper()

			moved := "gitlab.com/acme/app-graph"
			test.WriteFile(t, filepath.Join(h.repo, ".sdd", "config.yaml"),
				"graph_repo: "+moved+"\nproject_dir: ../app-sdd\ngraph_dir: .sdd/GRAPH-ELSEWHERE.md\n")
			h.graph.Clones[moved] = copied
		}},
		{name: "the folder is gone", change: func(t *testing.T, h *harness, copied string) {
			t.Helper()

			again := filepath.Join(filepath.Dir(copied), "app-sdd-again")
			test.WriteFile(t, filepath.Join(again, ".sdd", "config.yaml"), "graph_dir: graph\nrepo_id: "+graphRepo+"\nlanguage: de\n")
			h.graph.Clones[graphRepo] = again

			err := os.RemoveAll(copied)
			if err != nil {
				t.Fatal(err)
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			copied := graphRepoLayout(t, h)
			h.sdd = cachedProbe(t, &test.Runner{})
			h.must("", "claim", "main")
			asked := h.graph.PathCalls()

			tc.change(t, h, copied)
			h.must(h.toolCall(), "tool")

			if h.graph.PathCalls() != asked+1 {
				t.Errorf("a kept answer no longer fitting the checkout should be asked again, asked %d more times",
					h.graph.PathCalls()-asked)
			}
		})
	}
}

func TestWithoutAnAnswerMemoryFollowsProjectDir(t *testing.T) {
	cases := []struct {
		name   string
		runner *test.Runner
		answer func(copied string) map[string]string
		asks   int
	}{
		{name: "an sdd lacking repo path", runner: &test.Runner{Stock: true}, asks: 0},
		{name: "repo path failing", runner: &test.Runner{}, answer: func(string) map[string]string { return nil }, asks: 2},
		{name: "repo path naming no folder", runner: &test.Runner{}, answer: func(copied string) map[string]string {
			return map[string]string{graphRepo: copied + "-missing"}
		}, asks: 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			copied := graphRepoLayout(t, h)
			h.sdd = cachedProbe(t, tc.runner)

			if tc.answer != nil {
				h.graph.Clones = tc.answer(copied)
			}

			if out := h.must("", "claim", "main"); !strings.Contains(out, ", entries in fr, ") {
				t.Errorf("without sdd's answer the graph should be read where project_dir points, got:\n%s", out)
			}

			h.must(h.toolCall(), "tool")

			if h.graph.PathCalls() != tc.asks {
				t.Errorf("sdd repo path should run only where sdd carries it, and a failed answer is kept by nobody, ran %d times",
					h.graph.PathCalls())
			}
		})
	}
}

func TestACheckoutNamingNoGraphRepoNeverAsks(t *testing.T) {
	h := newHarness(t)
	withGraph(t, h)
	runner := &test.Runner{}
	h.sdd = cachedProbe(t, runner)
	h.must("", "claim", "main")
	h.must(h.toolCall(), "tool")

	if h.graph.PathCalls() != 0 {
		t.Errorf("a checkout keeping its own graph should never ask sdd where a copy lives, asked %d times", h.graph.PathCalls())
	}

	for _, call := range runner.Calls {
		if len(call) > 1 && call[1] == "repo" {
			t.Errorf("a checkout keeping its own graph should not probe for repo path, ran: %v", call)
		}
	}
}
