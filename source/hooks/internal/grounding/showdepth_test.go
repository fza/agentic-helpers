package grounding_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/grounding"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const shownEntry = "sdd show 20260907-164635-d-tac-vkm"

// configured is a project carrying a graph and the named grounding.yaml
// fixture, the main checkout of its own repository.
func configured(t *testing.T, fixture string) grounding.Env {
	t.Helper()

	env := bare(t)
	env.Checkout = test.CommonDir{Dir: filepath.Join(env.ProjectDir, ".git")}
	test.WriteFile(t, filepath.Join(env.ProjectDir, ".sdd", "grounding.yaml"), test.Fixture(t, "grounding/"+fixture))

	return env
}

func as(session string, payload map[string]any) map[string]any {
	return with(payload, "session_id", session)
}

func TestConfiguredShowDepth(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
		command string
		says    string
	}{
		{name: "a deeper down refuses the default", fixture: "show-depth-down.yaml", command: " --down 2 --up 1", says: "`--down 2` reads short of the depth owed; rerun as:"},
		{name: "the refusal reruns at the depth owed", fixture: "show-depth-down.yaml", command: " --down 2 --up 1", says: "rerun as:\n\n  " + shownEntry + " --down 3 --up 1\n"},
		{name: "both directions set", fixture: "show-depth.yaml", command: " --down 3 --up 1", says: "`--up 1` reads short of the depth owed; rerun as:\n\n  " + shownEntry + " --down 3 --up 2\n"},
		{name: "the default stands without a block", fixture: "area-prefix.yaml", command: " --down 1 --up 1", says: "  " + shownEntry + " --down 2 --up 1\n"},
		{name: "the default stands under an empty block", fixture: "show-depth-empty.yaml", command: "", says: "  " + shownEntry + " --down 2 --up 1\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			refuses(t, run(t, configured(t, tc.fixture), "gate", bash(shownEntry+tc.command)), tc.says)
		})
	}

	passing := []struct {
		name    string
		fixture string
		command string
	}{
		{name: "the depth owed", fixture: "show-depth-down.yaml", command: " --down 3 --up 1"},
		{name: "deeper than owed", fixture: "show-depth.yaml", command: " --down 4 --up 2"},
		{name: "nothing owed", fixture: "show-depth-none.yaml"},
	}

	for _, tc := range passing {
		t.Run(tc.name, func(t *testing.T) {
			passes(t, run(t, configured(t, tc.fixture), "gate", bash(shownEntry+tc.command)))
		})
	}
}

func TestANegativeDepthIsAConfigError(t *testing.T) {
	env := configured(t, "show-depth-negative.yaml")

	refuses(t, run(t, env, "gate", ask("s1")), "below 0")
	refuses(t, run(t, env, "gate", bash(shownEntry+" --down 2 --up 1")), "below 0")

	passes(t, run(t, env, "gate", bash("ls")))
}

func TestAnExemptSeat(t *testing.T) {
	env := configured(t, "show-depth-exempt.yaml")
	processes := test.NewProcesses()
	env.Processes, env.OwnerName = processes, "claude"
	store := test.MemoryStore(t, env.Roots, env.ProjectDir, env.Checkout)
	test.ClaimSeat(t, store, "reviewer", test.Session)

	t.Run("reads at any depth", func(t *testing.T) {
		passes(t, run(t, env, "gate", as(test.Session, bash(shownEntry))))
		passes(t, run(t, env, "gate", as(test.Session, bash(shownEntry+" --down 0 --up 0"))))
	})

	t.Run("covers a subagent of its session", func(t *testing.T) {
		passes(t, run(t, env, "gate", with(as(test.Session, bash(shownEntry)), "agent_id", "a1")))
	})

	t.Run("still owes an attached stream", func(t *testing.T) {
		refuses(t, run(t, env, "gate", as(test.Session, bash(shownEntry+" 2>/dev/null"))), "discarded stream")
	})

	t.Run("still owes the reads for everything else", func(t *testing.T) {
		compact := map[string]any{"session_id": test.Session, "source": "compact"}

		refuses(t, run(t, env, "gate", ask(test.Session)), "search")
		run(t, env, "start", compact)
		refuses(t, run(t, env, "gate", as(test.Session, edit(filepath.Join(env.ProjectDir, ".sdd", "tmp", "drafts", "d.md")))), "search")
		run(t, env, "start", compact)
		refuses(t, run(t, env, "gate", as(test.Session, map[string]any{"tool_name": "NotebookEdit", "tool_input": map[string]any{"file_path": filepath.Join(env.ProjectDir, "drafts", "n.ipynb")}})), "search")
		refuses(t, run(t, env, "gate", as(test.Session, bash("sdd new d tac body"))), "search")
		refuses(t, run(t, env, "gate", as(test.Session, bash("agentic-capture .sdd/tmp/drafts/d.md"))), "search")
	})

	other := env
	other.Processes = processes.Other()

	t.Run("a session holding no seat owes the depth", func(t *testing.T) {
		refuses(t, run(t, other, "gate", as(test.OtherSession, bash(shownEntry))), "  "+shownEntry+" --down 2 --up 1\n")
	})

	t.Run("a seat not named owes the depth", func(t *testing.T) {
		test.ClaimOtherSeat(t, store, "scout", test.OtherSession)
		refuses(t, run(t, other, "gate", as(test.OtherSession, bash(shownEntry))), "  "+shownEntry+" --down 2 --up 1\n")
	})

	t.Run("a claim whose holder is gone exempts nobody", func(t *testing.T) {
		processes.Kill(test.OwnerPID)
		refuses(t, run(t, env, "gate", as(test.Session, bash(shownEntry))), "  "+shownEntry+" --down 2 --up 1\n")
	})
}

func TestSeatsLiveInTheMainCheckout(t *testing.T) {
	main := t.TempDir()

	env := configured(t, "show-depth-exempt.yaml")
	env.Checkout = test.CommonDir{Dir: filepath.Join(main, ".git")}
	env.Processes, env.OwnerName = test.NewProcesses(), "claude"

	test.ClaimSeat(t, test.MemoryStore(t, env.Roots, main, env.Checkout), "reviewer", test.Session)

	passes(t, run(t, env, "gate", as(test.Session, bash(shownEntry))))
}

func TestTheShowRefusalNamesTheDepthOnce(t *testing.T) {
	cases := []struct {
		name    string
		payload map[string]any
		fixture string
		depth   string
	}{
		{name: "a Bash show", payload: bash(shownEntry + " --down 1 | head -40"), fixture: "refusals/downstream.txt", depth: "--down 2"},
		{name: "an MCP show", payload: mcp("show", map[string]any{"ids": []string{"20260907-164635-d-tac-vkm"}}),
			fixture: "refusals/downstream-mcp.txt", depth: `"down": 2`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := run(t, bare(t), "gate", tc.payload)
			if got.said != test.Fixture(t, tc.fixture) {
				t.Errorf("the refusal should echo the call at the depth owed above the rule, got: %s", got.said)
			}

			if strings.Count(got.said, tc.depth) != 1 {
				t.Errorf("the refusal should name the depth owed only in the call it echoes, got: %s", got.said)
			}
		})
	}
}

func TestTheShowRefusalCorrectsOnlyWhatFellShort(t *testing.T) {
	cases := []struct {
		name    string
		command string
		rerun   string
	}{
		{name: "a deeper direction stays as given", command: " --down 5 --up 0", rerun: shownEntry + " --down 5 --up 1"},
		{name: "an equals sign", command: " --down=0 --up 1", rerun: shownEntry + " --down 2 --up 1"},
		{name: "a missing direction is appended", command: " --up 3", rerun: shownEntry + " --up 3 --down 2"},
		{name: "a subject flag stays", command: " --down 1 --entry e", rerun: shownEntry + " --down 2 --entry e --up 1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			refuses(t, run(t, bare(t), "gate", bash(shownEntry+tc.command)), "rerun as:\n\n  "+tc.rerun+"\n\n")
		})
	}
}
