package grounding_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestTheGateHoldsOnStockSdd(t *testing.T) {
	env := areaProject(t)
	env.Runner = &test.Runner{Stock: true}

	started := run(t, env, "start", map[string]any{"session_id": "s1", "source": "startup"})
	if started.stdout != "" || started.stderr != "" {
		t.Errorf("a session on stock sdd should start without a notice, got: %+v", started)
	}

	ledgers, _ := os.ReadDir(filepath.Join(env.ProjectDir, ".sdd", "tmp", "grounding-gate"))
	for _, ledger := range ledgers {
		if strings.Contains(ledger.Name(), "unforked") {
			t.Errorf("no session should be marked as stepped aside, found %s", ledger.Name())
		}
	}

	refuses(t, run(t, env, "gate", bash("agentic-capture .tmp/drafts/d.md")), "neither read")

	ground(t, env)
	passes(t, run(t, env, "gate", bash("agentic-capture .tmp/drafts/d.md")))
}

func TestTheEngineCountsWhereSddFollowsProjectDir(t *testing.T) {
	cases := []struct {
		name     string
		stock    bool
		toEngine bool
	}{
		{name: "an sdd with --project-dir serves the graph repository", toEngine: true},
		{name: "an sdd without it serves the code repository's own .sdd", stock: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, _ := splitProject(t)
			env.Runner = &test.Runner{Stock: tc.stock}
			test.WriteFile(t, filepath.Join(env.ProjectDir, ".mcp.json"), test.Fixture(t, "grounding/mcp-sdd.json"))
			ground(t, env)

			got := run(t, env, "gate", bash("agentic-capture .tmp/drafts/d.md"))
			if !tc.toEngine {
				passes(t, got)

				return
			}

			refuses(t, got, "mcp__sdd__start_procedure")
		})
	}
}
