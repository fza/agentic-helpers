package grounding_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/grounding"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const shownEntry = "sdd show 20260907-164635-d-tac-vkm"

// configured is a project carrying a graph and the named grounding.yaml
// fixture, outside any repository, so its seats live in the project itself.
func configured(t *testing.T, fixture string) grounding.Env {
	t.Helper()

	env := bare(t)
	env.Checkout = test.CommonDir{Err: errors.New("not a git repository")}
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
		{name: "a deeper down refuses the default", fixture: "show-depth-down.yaml", command: " --down 2 --up 1", says: "reads short of `--down 3 --up 1`"},
		{name: "the refusal reruns at the depth owed", fixture: "show-depth-down.yaml", command: " --down 2 --up 1", says: "rerun w/ `--down 3 --up 1`"},
		{name: "both directions set", fixture: "show-depth.yaml", command: " --down 3 --up 1", says: "`--up 1` reads short of `--down 3 --up 2`"},
		{name: "the default stands without a block", fixture: "area-prefix.yaml", command: " --down 1 --up 1", says: "reads short of `--down 2 --up 1`"},
		{name: "the default stands under an empty block", fixture: "show-depth-empty.yaml", command: "", says: "reads short of `--down 2 --up 1`"},
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
	test.ClaimSeat(t, env.ProjectDir, "reviewer", test.Session)
	test.ClaimSeat(t, env.ProjectDir, "scout", test.OtherSession)

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
		refuses(t, run(t, env, "gate", ask(test.Session)), "search")
		refuses(t, run(t, env, "gate", as(test.Session, edit(filepath.Join(env.ProjectDir, ".sdd", "tmp", "drafts", "d.md")))), "search")
		refuses(t, run(t, env, "gate", as(test.Session, map[string]any{"tool_name": "NotebookEdit", "tool_input": map[string]any{"file_path": filepath.Join(env.ProjectDir, "drafts", "n.ipynb")}})), "search")
		refuses(t, run(t, env, "gate", as(test.Session, bash("sdd new d tac body"))), "search")
		refuses(t, run(t, env, "gate", as(test.Session, bash("agentic-capture .sdd/tmp/drafts/d.md"))), "search")
	})

	t.Run("a seat not named owes the depth", func(t *testing.T) {
		refuses(t, run(t, env, "gate", as(test.OtherSession, bash(shownEntry))), "reads short of `--down 2 --up 1`")
	})

	t.Run("a session holding no seat owes the depth", func(t *testing.T) {
		refuses(t, run(t, env, "gate", bash(shownEntry)), "reads short of `--down 2 --up 1`")
	})
}

func TestSeatsLiveInTheMainCheckout(t *testing.T) {
	main := t.TempDir()
	test.ClaimSeat(t, main, "reviewer", test.Session)

	env := configured(t, "show-depth-exempt.yaml")
	env.Checkout = test.CommonDir{Dir: filepath.Join(main, ".git")}

	passes(t, run(t, env, "gate", as(test.Session, bash(shownEntry))))

	test.ClaimSeat(t, env.ProjectDir, "reviewer", test.OtherSession)
	refuses(t, run(t, env, "gate", as(test.OtherSession, bash(shownEntry))), "reads short")
}
