package memory_test

import (
	"context"
	"encoding/json"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

// sessionPurposes are the key prefixes of a session's own state.
var sessionPurposes = []string{"shown/", "ladder/", "graph-seen/", "fork/", "waiter/", "instructions/", "agents/", "spawn/"}

// sessionState is every per-session slot the store holds, key to value.
func (h *harness) sessionState() map[string]string {
	h.t.Helper()

	held, err := slots.Open(h.roots().Cache, filepath.Join(h.cacheDir(), "sessions.slots")).All(context.Background())
	if err != nil {
		h.t.Fatalf("the session slots should read: %v", err)
	}

	state := map[string]string{}

	for _, slot := range held {
		if slices.ContainsFunc(sessionPurposes, func(purpose string) bool { return strings.HasPrefix(slot.Key, purpose) }) {
			state[slot.Key] = string(slot.Value)
		}
	}

	return state
}

// hookRound runs every memory hook that keeps per-session state with the
// session id given, the way a session's turn reaches them.
func (h *harness) hookRound(session string) map[string]ran {
	h.t.Helper()

	payload := func(fields map[string]any) string {
		fields["session_id"] = session
		data, _ := json.Marshal(fields)

		return string(data)
	}

	ran := map[string]ran{}
	ran["session-start"] = h.run(payload(map[string]any{"source": "startup"}), "session-start")
	ran["instructions"] = h.run(payload(map[string]any{"file_path": h.repo + "/CLAUDE.md"}), "instructions")
	ran["prompt"] = h.run(payload(map[string]any{"prompt": "go on"}), "prompt")
	ran["pre-tool"] = h.run(payload(map[string]any{
		"tool_name": "Agent", "tool_use_id": "toolu_1", "tool_input": map[string]string{"prompt": "agentic-helpers: shared\nlook"},
	}), "pre-tool")
	ran["tool"] = h.run(payload(map[string]any{"tool_name": "Write", "tool_input": map[string]string{"file_path": h.repo + "/x"}}), "tool")
	ran["subagent tool"] = h.run(payload(map[string]any{
		"agent_id": "asub0001", "tool_name": "Read", "tool_input": map[string]string{"file_path": "x"},
	}), "tool")
	ran["stop"] = h.run(payload(map[string]any{}), "stop")

	return ran
}

func TestAHookSessionIDOfAnotherShapeKeepsNoSessionState(t *testing.T) {
	cases := []struct {
		name    string
		session string
	}{
		{name: "an id holding a slash", session: test.Session + "/toolu_1"},
		{name: "an id of 129 characters", session: strings.Repeat("a", 129)},
		{name: "an empty id", session: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")
			before := h.sessionState()

			for hook, got := range h.hookRound(tc.session) {
				if got.code != 0 {
					t.Errorf("%s should exit as it would without memory state, got %d %q", hook, got.code, got.err)
				}
			}

			if after := h.sessionState(); !maps.Equal(before, after) {
				t.Errorf("a session id outside the shape should write no per-session key, before %v, after %v", before, after)
			}

			if brief := h.must("", "brief"); !strings.Contains(brief, "seat main") {
				t.Errorf("the seat's own state should still work, got %q", brief)
			}
		})
	}
}

func TestAUUIDSessionIDKeepsItsSessionState(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.hookRound(test.Session)

	held := h.sessionState()
	for _, key := range []string{"ladder/" + test.Session, "instructions/" + test.Session, "graph-seen/" + test.Session} {
		if _, kept := held[key]; !kept {
			t.Errorf("a UUID session id should keep its per-session keys as before, kept %v", slices.Sorted(maps.Keys(held)))
		}
	}
}
