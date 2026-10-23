package grounding_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/grounding"
	"github.com/fza/agentic-helpers/source/hooks/internal/process"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func start(session string) map[string]any {
	return map[string]any{"session_id": session, "hook_event_name": "SessionStart"}
}

func TestTheGateStepsAsideWithoutTheSddFork(t *testing.T) {
	cases := []struct {
		name    string
		version process.Result
		fails   bool
	}{
		{name: "upstream", version: process.Result{Stdout: "sdd version 0.17.0\n"}},
		{name: "a dev build", version: process.Result{Stdout: "sdd version dev\n"}},
		{name: "no sdd at all", fails: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := areaProject(t)
			env.Runner = &test.Runner{Version: tc.version, VersionFails: tc.fails}
			session := "11111111-2222-3333-4444-555555555555"

			began := run(t, env, "start", start(session))
			if began.code != 0 || strings.Count(began.stdout, "the grounding gate is off for this session") != 1 ||
				!strings.Contains(began.stdout, "brew install fza/tap/sdd") {
				t.Errorf("the session start should say once that the gate steps aside and how to fix it, got: %+v", began)
			}

			passes(t, run(t, env, "gate", ask(session)))

			refuses(t, run(t, env, "gate", ask("66666666-7777-8888-9999-000000000000")), "search")

			run(t, env, "end", map[string]any{"session_id": session})
			refuses(t, run(t, env, "gate", ask(session)), "search")
		})
	}
}

func TestTheForkKeepsTheGate(t *testing.T) {
	for _, version := range []string{"sdd version 0.18.0-alpha.2.fza.22\n", "sdd version 0.18.0-alpha.2+fza21\n"} {
		env := areaProject(t)
		env.Runner = &test.Runner{Version: process.Result{Stdout: version}}
		session := "11111111-2222-3333-4444-555555555555"

		began := run(t, env, "start", start(session))
		if began.code != 0 || began.stdout != "" {
			t.Errorf("the fork should raise no notice, got: %+v", began)
		}

		refuses(t, run(t, env, "gate", ask(session)), "search")
	}
}

func TestAProjectWithoutAGraphNeverAsksSdd(t *testing.T) {
	root := t.TempDir()
	runner := &test.Runner{Version: process.Result{Stdout: "sdd version 0.17.0\n"}}
	env := grounding.Env{ProjectDir: root, WorkingDir: root, Home: t.TempDir(), Runner: runner}

	began := run(t, env, "start", start("11111111-2222-3333-4444-555555555555"))
	if began.code != 0 || began.stdout != "" || len(runner.Calls) != 0 {
		t.Errorf("a project without a graph should never run sdd, got: %+v, calls: %v", began, runner.Calls)
	}

	if test.Exists(t, filepath.Join(root, ".sdd")) {
		t.Error("a project without a graph should be left untouched")
	}
}
