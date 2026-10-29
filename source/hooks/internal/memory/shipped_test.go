package memory_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

const (
	shippedHold = "Work shipped since your last state write. Write where work stands: agentic-memory state < body. " +
		"Then end the turn without commentary."
	shippedAsk = "Work shipped since your last state write. Write where work stands first: agentic-memory state < body."
)

func (h *harness) stopHeld() bool {
	h.t.Helper()

	got := h.run(h.stop(), "stop")
	if got.code == 0 && got.err == "" && test.AddedContext(got.out, "Stop") == shippedHold {
		return true
	}

	if got.code != 0 {
		h.t.Fatalf("stop should pass or hold for a state write, exit %d: %s%s", got.code, got.out, got.err)
	}

	if got.out != "" || got.err != "" {
		h.t.Fatalf("a stop that holds nothing should print nothing: %s%s", got.out, got.err)
	}

	return false
}

// asked runs a turn's end and the next prompt, and tells whether that prompt
// carried the shipped-work request.
func (h *harness) asked() bool {
	h.t.Helper()

	if h.stopHeld() {
		h.t.Fatal("shipped work away from compaction should never hold Stop")
	}

	return strings.Contains(h.must(h.promptSaying("go on"), "prompt"), shippedAsk)
}

func (h *harness) writeFiles(count int) {
	h.t.Helper()

	for range count {
		h.callTool("Edit", map[string]string{"file_path": filepath.Join(h.repo, "README.md")})
	}
}

func TestShippedWorkAsksAtTheNextPromptOnce(t *testing.T) {
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

			if h.asked() {
				t.Fatal("a session that shipped nothing should not be asked")
			}

			tc.ship(h)

			if !h.asked() {
				t.Error("shipped work should ask for a state write at the next prompt")
			}

			if h.asked() {
				t.Error("an ignored request should never repeat before a state write")
			}
		})
	}
}

func TestShippedWorkNearCompactionHoldsStopOnce(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.sensor(70)
	h.writeFiles(5)

	if !h.stopHeld() {
		t.Fatal("shipped work near compaction should hold Stop, since the next prompt may come after the compaction")
	}

	if h.stopHeld() {
		t.Error("an ignored hold should never repeat before a state write")
	}

	if strings.Contains(h.must(h.promptSaying("go on"), "prompt"), shippedAsk) {
		t.Error("a request Stop already delivered should not repeat at the next prompt")
	}

	h.must("the gate reads Bash only; the MCP half is next", "state")

	if h.stopHeld() {
		t.Error("a state write should answer the hold")
	}

	h.writeFiles(5)
	h.sensor(95)

	if !h.stopHeld() {
		t.Error("work shipped after a state write should hold again once the context climbs past the rearmed rung")
	}
}

func TestTheCompactionNudgeStandsInForTheShippedRequest(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.writeFiles(5)
	h.sensor(70)

	out := h.must(h.promptSaying("go on"), "prompt")
	if !strings.Contains(out, "Context near compaction") || strings.Contains(out, shippedAsk) {
		t.Errorf("a prompt carrying the nudge should not ask for the same state write twice, got:\n%s", out)
	}

	if h.stopHeld() {
		t.Error("the nudge should spend the shipped-work request, so Stop holds nothing")
	}
}

func TestFewerWritesThanTheThresholdNeverAsk(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.writeFiles(4)

	if h.asked() {
		t.Error("writes short of state_stale_after_writes should not ask")
	}
}

func TestAStateWriteClearsTheRequest(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.writeFiles(5)

	if !h.asked() {
		t.Fatal("five writes should ask for a state write")
	}

	h.must("the gate reads Bash only; the MCP half is next", "state")

	if h.asked() {
		t.Error("a state write should answer the request")
	}

	h.writeFiles(4)

	if h.asked() {
		t.Error("writes before a state write should no longer count after it")
	}

	h.writeFiles(1)

	if !h.asked() {
		t.Error("work shipped after a state write should ask again")
	}
}

func TestAStateWriteCountsFromTheCommitItStamps(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.callTool("Bash", map[string]string{"command": "agentic-memory claim main"})
	h.git.Commit = "9c0ffee1"
	h.must("where it stands at 9c0ffee1", "state")

	if h.asked() {
		t.Error("a commit the state write already stamps should not ask")
	}

	h.must("where it stands, again", "state")
	h.git.Commit = "d00dfeed"

	if !h.asked() {
		t.Error("a commit made after the state write should ask, though no hook ran between them")
	}
}

func TestTheWriteThresholdIsAnOption(t *testing.T) {
	h := newHarness(t)
	test.WriteFile(t, h.settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"state_stale_after_writes":2}}}}`)
	h.must("", "claim", "main")
	h.writeFiles(2)

	if !h.asked() {
		t.Error("state_stale_after_writes should set how many writes ask")
	}
}

func TestSessionsThatShipNothingOrHoldNoSeatAreNeverAsked(t *testing.T) {
	t.Run("a chat-only session", func(t *testing.T) {
		h := newHarness(t)
		h.must("", "claim", "main")
		h.callTool("Read", map[string]string{"file_path": filepath.Join(h.repo, "README.md")})
		h.callTool("Grep", map[string]string{"pattern": "gate"})

		if h.asked() || h.asked() {
			t.Error("a session reading and talking only should never be asked")
		}
	})

	t.Run("a seatless session", func(t *testing.T) {
		h := newHarness(t)
		h.must("", "claim", "main")

		seatless := newHarnessSharing(h)
		seatless.writeFiles(6)
		seatless.git.Commit = "9c0ffee1"

		if seatless.asked() {
			t.Error("a session holding no seat should never be asked")
		}
	})
}

func TestACommitBeforeTheFirstPromptCounts(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.callTool("Bash", map[string]string{"command": "agentic-memory claim main"})
	h.git.Commit = "9c0ffee1"

	if !h.asked() {
		t.Error("work is measured from the session's first tool call, so a commit in its first turn should ask")
	}
}
