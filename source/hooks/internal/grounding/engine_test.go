package grounding_test

import (
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestACaptureRunsThroughTheEngineWhereTheProjectServesIt(t *testing.T) {
	cases := []struct {
		name     string
		mcp      string
		config   string
		command  string
		toEngine bool
	}{
		{name: "an sdd server in .mcp.json", mcp: "grounding/mcp-sdd.json", config: "grounding/area-prefix.yaml", command: "agentic-capture .tmp/drafts/d.md", toEngine: true},
		{name: "in any spelling", mcp: "grounding/mcp-sdd.json", config: "grounding/area-prefix.yaml", command: "bash -c '~/go/bin/agentic-capture d.md'", toEngine: true},
		{name: "cli_capture lifts it", mcp: "grounding/mcp-sdd.json", config: "grounding/cli-capture.yaml", command: "agentic-capture .tmp/drafts/d.md"},
		{name: "no sdd server", mcp: "grounding/mcp-other.json", config: "grounding/area-prefix.yaml", command: "agentic-capture .tmp/drafts/d.md"},
		{name: "no .mcp.json", config: "grounding/area-prefix.yaml", command: "agentic-capture .tmp/drafts/d.md"},
		{name: "a plain sdd new keeps its rule", mcp: "grounding/mcp-sdd.json", config: "grounding/area-prefix.yaml", command: "sdd new d tac body"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := bare(t)
			onDisk(t, env, ".tmp/drafts/d.md", "d.md")
			test.WriteFile(t, filepath.Join(env.ProjectDir, ".sdd", "grounding.yaml"), test.Fixture(t, tc.config))

			if tc.mcp != "" {
				test.WriteFile(t, filepath.Join(env.ProjectDir, ".mcp.json"), test.Fixture(t, tc.mcp))
			}

			record(t, env, bash(literalRead), bash(semanticRead), bash(areaRead), bash(`sdd view --layout "active:as-counts"`))

			got := run(t, env, "gate", bash(tc.command))
			if !tc.toEngine {
				passes(t, got)

				return
			}

			refuses(t, got, "mcp__sdd__start_procedure")

			if got.code != 2 {
				t.Errorf("a capture past the engine should be refused outright, whatever reads the session holds, got exit %d", got.code)
			}
		})
	}
}

func TestHelpPassesBesideTheEngine(t *testing.T) {
	env := bare(t)
	test.WriteFile(t, filepath.Join(env.ProjectDir, ".mcp.json"), test.Fixture(t, "grounding/mcp-sdd.json"))

	for _, command := range []string{"agentic-capture --help", "grep -rn agentic-capture skills"} {
		passes(t, run(t, env, "gate", bash(command)))
	}
}
