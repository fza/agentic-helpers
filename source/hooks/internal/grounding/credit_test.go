package grounding_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreditLastsWhileTheGraphStands(t *testing.T) {
	env := areaProject(t)
	draft := draftIn(t, env)
	ground(t, env)

	passes(t, run(t, env, "gate", ask("s1")))
	passes(t, run(t, env, "gate", bash("agentic-capture "+draft)))

	entryIn(t, env, "26-090000-d-tac-new.md")

	owesEveryRead(t, run(t, env, "gate", ask("s1")))
	owesEveryRead(t, run(t, env, "gate", bash("agentic-capture "+draft)))
}

func TestAReadAfterAGraphChangeClearsTheOlderCredit(t *testing.T) {
	env := areaProject(t)
	record(t, env, bash(semanticRead))

	entryIn(t, env, "26-090000-d-tac-new.md")
	record(t, env, bash(literalRead), bash(areaRead))

	got := run(t, env, "gate", ask("s1"))
	refuses(t, got, "\n`--query` search\n")

	if strings.Contains(got.said, "\n`--term` search\n") || strings.Contains(got.said, "` listing\n") {
		t.Errorf("only the read made before the change should be owed, got: %s", got.said)
	}
}

func TestOnlyACompactionForgetsTheReads(t *testing.T) {
	for _, source := range []string{"startup", "resume", "clear"} {
		t.Run(source, func(t *testing.T) {
			env := areaProject(t)
			ground(t, env)
			run(t, env, "start", map[string]any{"session_id": "s1", "source": source})

			passes(t, run(t, env, "gate", ask("s1")))
		})
	}

	t.Run("compact", func(t *testing.T) {
		env := areaProject(t)
		draft := draftIn(t, env)
		ground(t, env)
		record(t, env, save("Write", draft))
		compact(t, env)

		owesEveryRead(t, run(t, env, "gate", ask("s1")))
		passes(t, run(t, env, "gate", bash("agentic-capture "+draft)))
	})
}

func TestTheTurnModeIsGone(t *testing.T) {
	got := run(t, areaProject(t), "turn", map[string]any{"session_id": "s1"})
	if got.code != 1 || !strings.Contains(got.stderr, "usage: agentic-grounding-gate record|gate|start|end") {
		t.Errorf("the gate should no longer take a turn mode, got %+v", got)
	}

	wiring, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "plugin", "hooks", "hooks.json"))
	if err != nil || strings.Contains(string(wiring), "grounding-gate turn") {
		t.Errorf("no hook should run the turn mode, got %v", err)
	}
}

func TestADraftSavedAfterTheGraphMovedIsNotGrounded(t *testing.T) {
	env := areaProject(t)
	draft := draftIn(t, env)
	ground(t, env)

	entryIn(t, env, "26-090000-d-tac-new.md")
	record(t, env, save("Write", draft))

	owesEveryRead(t, run(t, env, "gate", bash("agentic-capture "+draft)))
}
