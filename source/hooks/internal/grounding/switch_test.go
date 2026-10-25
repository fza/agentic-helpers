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

func withOptions(t *testing.T, env grounding.Env, options string) grounding.Env {
	t.Helper()

	settings := filepath.Join(t.TempDir(), "settings.json")
	test.WriteFile(t, settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{`+options+`}}}}`)
	env.Settings = []string{settings}

	return env
}

func TestTheGateOffOnTheMachine(t *testing.T) {
	env := withOptions(t, bare(t), `"grounding_gate_enabled":false`)

	for name, payload := range unread(env, "s1") {
		t.Run(name+" passes", func(t *testing.T) {
			got := run(t, env, "gate", payload)

			passes(t, got)

			if got.stderr != "" {
				t.Error("a gate off on the machine should say nothing")
			}
		})
	}

	run(t, env, "start", map[string]any{"session_id": "s1", "source": "compact"})
	ground(t, env)

	_, err := os.Stat(filepath.Join(env.ProjectDir, ".sdd", "tmp", "grounding-gate"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a gate off on the machine should keep no evidence, got: %v", err)
	}
}

func TestExemptSeatsRestOnMemory(t *testing.T) {
	env := configured(t, "exempt-seats.yaml")
	env.Processes, env.OwnerName = test.NewProcesses(), "claude"
	test.ClaimSeat(t, test.MemoryStore(env.ProjectDir, env.Home, env.Checkout), "reviewer", test.Session)

	passes(t, run(t, env, "gate", ask(test.Session)))

	off := withOptions(t, env, `"memory_enabled":false`)
	refuses(t, run(t, off, "gate", ask(test.Session)), "search")
}
