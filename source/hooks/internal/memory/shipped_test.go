package memory_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

const shippedHold = "Work shipped since your last state write. Write where work stands: agentic-memory state < body. " +
	"Then end the turn without commentary."

func (h *harness) stopHeld() bool {
	h.t.Helper()

	got := h.run(h.stop(), "stop")
	if got.code == 2 && strings.TrimSpace(got.err) == shippedHold {
		return true
	}

	if got.code != 0 {
		h.t.Fatalf("stop should pass or hold for a state write, exit %d: %s%s", got.code, got.out, got.err)
	}

	return false
}

func (h *harness) writeFiles(count int) {
	h.t.Helper()

	for range count {
		h.callTool("Edit", map[string]string{"file_path": filepath.Join(h.repo, "README.md")})
	}
}

func TestShippedWorkHoldsStopOnce(t *testing.T) {
	cases := []struct {
		name string
		ship func(h *harness)
	}{
		{name: "HEAD moved", ship: func(h *harness) { h.git.Commit = "9c0ffee1" }},
		{name: "the graph gained an entry", ship: func(h *harness) {
			test.WriteFile(t, filepath.Join(h.repo, ".sdd", "graph", "2026", "10", "25-120000-d-tac-one.md"), "---\n---\n")
			h.callTool("Bash", map[string]string{"command": "sdd new"})
		}},
		{name: "file writes reached the threshold", ship: func(h *harness) { h.writeFiles(5) }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			withGraph(t, h)
			h.must("", "claim", "main")
			h.callTool("Bash", map[string]string{"command": "agentic-memory claim main"})

			if h.stopHeld() {
				t.Fatal("a session that shipped nothing should not be held")
			}

			tc.ship(h)

			if !h.stopHeld() {
				t.Error("shipped work should hold Stop for a state write")
			}

			if h.stopHeld() {
				t.Error("an ignored hold should never repeat before a state write")
			}
		})
	}
}

func TestFewerWritesThanTheThresholdNeverHold(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.writeFiles(4)

	if h.stopHeld() {
		t.Error("writes short of state_stale_after_writes should not hold")
	}
}

func TestAStateWriteClearsTheHold(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.writeFiles(5)

	if !h.stopHeld() {
		t.Fatal("five writes should hold Stop")
	}

	h.must("the gate reads Bash only; the MCP half is next", "state")

	if h.stopHeld() {
		t.Error("a state write should answer the hold")
	}

	h.writeFiles(4)

	if h.stopHeld() {
		t.Error("writes before a state write should no longer count after it")
	}

	h.writeFiles(1)

	if !h.stopHeld() {
		t.Error("work shipped after a state write should hold again")
	}
}

func TestAStateWriteCountsFromTheCommitItStamps(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.callTool("Bash", map[string]string{"command": "agentic-memory claim main"})
	h.git.Commit = "9c0ffee1"
	h.must("where it stands at 9c0ffee1", "state")

	if h.stopHeld() {
		t.Error("a commit the state write already stamps should not hold")
	}

	h.must("where it stands, again", "state")
	h.git.Commit = "d00dfeed"

	if !h.stopHeld() {
		t.Error("a commit made after the state write should hold, though no hook ran between them")
	}
}

func TestTheWriteThresholdIsAnOption(t *testing.T) {
	h := newHarness(t)
	test.WriteFile(t, h.settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"state_stale_after_writes":2}}}}`)
	h.must("", "claim", "main")
	h.writeFiles(2)

	if !h.stopHeld() {
		t.Error("state_stale_after_writes should set how many writes hold Stop")
	}
}

func TestSessionsThatShipNothingOrHoldNoSeatAreNeverHeld(t *testing.T) {
	t.Run("a chat-only session", func(t *testing.T) {
		h := newHarness(t)
		h.must("", "claim", "main")
		h.callTool("Read", map[string]string{"file_path": filepath.Join(h.repo, "README.md")})
		h.callTool("Grep", map[string]string{"pattern": "gate"})

		if h.stopHeld() || h.stopHeld() {
			t.Error("a session reading and talking only should never be held")
		}
	})

	t.Run("a seatless session", func(t *testing.T) {
		h := newHarness(t)
		h.must("", "claim", "main")

		seatless := newHarnessSharing(h)
		seatless.writeFiles(6)
		seatless.git.Commit = "9c0ffee1"

		if seatless.stopHeld() {
			t.Error("a session holding no seat should never be held")
		}
	})
}

func TestACommitBeforeTheFirstStopCounts(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.callTool("Bash", map[string]string{"command": "agentic-memory claim main"})
	h.git.Commit = "9c0ffee1"

	if !h.stopHeld() {
		t.Error("work is measured from the session's first tool call, so a commit in its first turn should hold")
	}
}
