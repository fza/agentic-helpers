package memory_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

// touchBy runs the PostToolUse hook on a Read of the path the agent made, the
// main thread where agentID is empty, and answers what it was told.
func (h *harness) touchBy(agentID string, path string) string {
	h.t.Helper()

	payload := map[string]any{
		"session_id": h.session, "transcript_path": h.sessionTranscript(), "tool_name": "Read",
		"tool_input": map[string]string{"file_path": filepath.Join(h.repo, path)},
	}
	if agentID != "" {
		payload["agent_id"] = agentID
	}

	data, _ := json.Marshal(payload)

	return test.AddedContext(h.must(string(data), "tool"), "PostToolUse")
}

// notices is a trap and a note, each filed about internal/gate/gate.go, with
// the line the tool hook tells it by.
var notices = map[string]struct {
	file func(h *harness) string
	told string
}{
	"trap": {
		file: func(h *harness) string {
			return h.file("Gate edits break the hook", "editing internal/gate/gate.go breaks the hook", "--kind", "trap")
		},
		told: "trap for internal/gate/gate.go",
	},
	"note": {
		file: func(h *harness) string {
			return h.file("Gate reads only Bash today", "the gate skips MCP calls", "--about", "internal/gate/gate.go")
		},
		told: "about internal/gate/gate.go",
	},
}

func TestASubagentsNoticeLeavesItForItsParent(t *testing.T) {
	for kind, notice := range notices {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")
			notice.file(h)
			h.toolBy("")
			h.spawned("arules000000001", "agentic-helpers: rules")

			if out := h.touchBy("arules000000001", "internal/gate/gate.go"); !strings.Contains(out, notice.told) {
				t.Errorf("a subagent touching the file should be told, got %q", out)
			}

			if out := h.touchBy("arules000000001", "internal/gate/gate.go"); strings.Contains(out, notice.told) {
				t.Errorf("a subagent should be told once, got %q", out)
			}

			if out := h.touchBy("", "internal/gate/gate.go"); !strings.Contains(out, notice.told) {
				t.Errorf("the parent touching the file after its subagent should still be told, got %q", out)
			}
		})
	}
}

func TestAFreshSubagentHearsWhatItsParentHeardAndAForkDoesNot(t *testing.T) {
	for kind, notice := range notices {
		for _, marker := range []string{"agentic-helpers: rules", "agentic-helpers: silent"} {
			t.Run(kind+" at "+marker, func(t *testing.T) {
				h := newHarness(t)
				h.must("", "claim", "main")
				notice.file(h)

				if out := h.touchBy("", "internal/gate/gate.go"); !strings.Contains(out, notice.told) {
					t.Fatalf("the parent touching the file should be told, got %q", out)
				}

				h.spawned("afresh000000003", marker)
				h.forked("", "afork0000000003", marker, "toolu_01ForkThree")

				if out := h.touchBy("afresh000000003", "internal/gate/gate.go"); !strings.Contains(out, notice.told) {
					t.Errorf("a fresh subagent should be told what only its parent's context holds, got %q", out)
				}

				if out := h.touchBy("afork0000000003", "internal/gate/gate.go"); strings.Contains(out, notice.told) {
					t.Errorf("a fork should not be told again what its spawner heard before the spawn, got %q", out)
				}
			})
		}
	}
}
