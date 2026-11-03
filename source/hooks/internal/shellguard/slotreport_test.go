package shellguard_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/shellguard"
)

type hookRun struct {
	code int
	out  string
	err  string
}

// run runs the hook args name on the call, keeping its exit code and both
// streams as written.
func (c countdown) run(args []string, call map[string]any) hookRun {
	c.guard.t.Helper()

	input, err := json.Marshal(call)
	if err != nil {
		c.guard.t.Fatalf("encoding the call: %v", err)
	}

	var out, errs bytes.Buffer

	code := shellguard.Run(context.Background(), args, c.guard.env, shellguard.Streams{In: bytes.NewReader(input), Out: &out, Err: &errs})

	return hookRun{code: code, out: out.String(), err: errs.String()}
}

// TestASlotWriteTheFileRefusesIsReported runs a hook twice on the same kept
// state, once with the slot file writable and once read-only: the refused
// run says on stderr that reclaiming and keeping failed, and answers the call
// byte for byte as the run whose writes landed.
func TestASlotWriteTheFileRefusesIsReported(t *testing.T) {
	cases := []struct {
		name string
		args []string
		call func(c countdown) map[string]any
	}{
		{name: "a refusal counted before a rewrite", call: func(c countdown) map[string]any {
			return map[string]any{
				"session_id": c.session, "cwd": c.guard.tree.Project,
				"tool_input": map[string]string{"command": c.guard.placeholders.Replace(askedName)},
			}
		}},
		{name: "a countdown that ran out", args: []string{"permission"}, call: func(c countdown) map[string]any {
			return map[string]any{
				"session_id": c.session, "transcript_path": c.transcript, "permission_mode": "auto",
				"hook_event_name": "PermissionRequest", "tool_name": "Bash", "cwd": c.guard.tree.Project,
				"tool_input": map[string]string{"command": c.guard.placeholders.Replace(askedFind)},
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newCountdown(t, "")
			c.run(tc.args, tc.call(c))

			path := filepath.Join(c.guard.env.StateDir, "shell-guard", "sessions.slots")

			kept, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading the kept state: %v", err)
			}

			landed := c.run(tc.args, tc.call(c))

			err = os.WriteFile(path, kept, 0o600)
			if err != nil {
				t.Fatalf("restoring the kept state: %v", err)
			}

			err = os.Chmod(path, 0o400)
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

			refused := c.run(tc.args, tc.call(c))

			if landed.err != "" {
				t.Errorf("a run whose writes land should say nothing on stderr, got:\n%s", landed.err)
			}

			if refused.code != landed.code || refused.out != landed.out {
				t.Errorf("a refused write should leave the answer as it was, exit %d against %d, answer:\n%s\nagainst:\n%s",
					refused.code, landed.code, refused.out, landed.out)
			}

			if !strings.Contains(refused.err, "agentic-shell-guard: reclaiming sessions.slots: ") {
				t.Errorf("a reclaim the slot file refuses should be reported, stderr:\n%s", refused.err)
			}

			if !strings.Contains(refused.err, "agentic-shell-guard: keeping "+c.session+": ") {
				t.Errorf("a write the slot file refuses should be reported, stderr:\n%s", refused.err)
			}
		})
	}
}
