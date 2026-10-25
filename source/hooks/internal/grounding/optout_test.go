package grounding_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/grounding"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

// unread is every call the gate refuses a session carrying no reads, from the
// session given.
func unread(env grounding.Env, session string) map[string]map[string]any {
	return map[string]map[string]any{
		"a question":         ask(session),
		"a draft edit":       as(session, edit(filepath.Join(env.ProjectDir, ".sdd", "tmp", "drafts", "d.md"))),
		"a notebook edit":    as(session, map[string]any{"tool_name": "NotebookEdit", "tool_input": map[string]any{"file_path": filepath.Join(env.ProjectDir, "drafts", "n.ipynb")}}),
		"a capture":          as(session, bash("agentic-capture .sdd/tmp/drafts/d.md")),
		"a plain new":        as(session, bash("sdd new d tac body")),
		"a discarded stream": as(session, bash(shownEntry+" --down 2 --up 1 2>/dev/null")),
		"a shallow show":     as(session, bash(shownEntry)),
	}
}

func TestTheGateSwitchedOff(t *testing.T) {
	env := configured(t, "disabled.yaml")

	for name, payload := range unread(env, "s1") {
		t.Run(name+" passes", func(t *testing.T) {
			got := run(t, env, "gate", payload)

			passes(t, got)

			if got.stderr != "" {
				t.Error("a gate switched off should stay silent")
			}
		})
	}

	t.Run("no ledger is written", func(t *testing.T) {
		run(t, env, "turn", ask("s1"))
		ground(t, env)

		_, err := os.Stat(filepath.Join(env.ProjectDir, ".sdd", "tmp", "grounding-gate"))
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("a gate switched off should keep no evidence, got: %v", err)
		}
	})
}

func TestTheGateSwitchedOn(t *testing.T) {
	refuses(t, run(t, configured(t, "enabled.yaml"), "gate", ask("s1")), "search")
}

func TestASwitchThatIsNoBooleanIsAConfigError(t *testing.T) {
	env := configured(t, "enabled-invalid.yaml")

	refuses(t, run(t, env, "gate", ask("s1")), "Fix `.sdd/grounding.yaml`")
}

func TestASeatExemptFromTheWholeGate(t *testing.T) {
	env := configured(t, "exempt-seats.yaml")
	processes := test.NewProcesses()
	env.Processes, env.OwnerName = processes, "claude"
	store := test.MemoryStore(env.ProjectDir, env.Home, env.Checkout)
	test.ClaimSeat(t, store, "reviewer", test.Session)

	for name, payload := range unread(env, test.Session) {
		t.Run(name+" passes", func(t *testing.T) {
			passes(t, run(t, env, "gate", payload))
		})
	}

	t.Run("covers a subagent of its session", func(t *testing.T) {
		passes(t, run(t, env, "gate", with(ask(test.Session), "agent_id", "a1")))
	})

	other := env
	other.Processes = processes.Other()

	t.Run("a session holding no seat owes the reads", func(t *testing.T) {
		refuses(t, run(t, other, "gate", ask(test.OtherSession)), "search")
	})

	t.Run("a seat exempt from the depth alone still owes the reads", func(t *testing.T) {
		test.ClaimOtherSeat(t, store, "scout", test.OtherSession)
		passes(t, run(t, other, "gate", as(test.OtherSession, bash(shownEntry))))
		refuses(t, run(t, other, "gate", ask(test.OtherSession)), "search")
	})
}

func TestOutsideGitNoSeatIsExempt(t *testing.T) {
	env := configured(t, "exempt-seats.yaml")
	processes := test.NewProcesses()
	env.Processes, env.OwnerName = processes, "claude"
	test.ClaimSeat(t, test.MemoryStore(env.ProjectDir, env.Home, env.Checkout), "reviewer", test.Session)
	env.Checkout = test.CommonDir{Err: errors.New("not a git repository")}

	refuses(t, run(t, env, "gate", ask(test.Session)), "neither read")
}

func TestAnExemptSeatIsNamedInAnySpelling(t *testing.T) {
	env := configured(t, "exempt-seats.yaml")
	test.WriteFile(t, filepath.Join(env.ProjectDir, ".sdd", "grounding.yaml"), "exempt_seats: [Café]\n")
	processes := test.NewProcesses()
	env.Processes, env.OwnerName = processes, "claude"
	test.ClaimSeat(t, test.MemoryStore(env.ProjectDir, env.Home, env.Checkout), "café", test.Session)

	passes(t, run(t, env, "gate", ask(test.Session)))
}
