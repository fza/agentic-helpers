package memory_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestASeatStaysHeldWhateverClaudeCodesExecutableIsNamed(t *testing.T) {
	for _, name := range []string{"2.1.318", "node"} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.processes = test.NewProcessesRunningAs(name)

			h.must("", "claim", "main")
			h.file("Seat stays held", "Filed while the session holds its seat.")

			who := h.must("", "who")
			if strings.Contains(who, "gone") || !strings.Contains(who, fmt.Sprintf("pid %d", test.OwnerPID)) {
				t.Errorf("the seat should stay held by the running Claude Code process: %s", who)
			}

			h.processes.Restart(test.OwnerPID, test.OwnerStart.Add(time.Hour))

			who = h.must("", "who")
			if !strings.Contains(who, "gone") {
				t.Errorf("a pid another process reuses should never hold the seat: %s", who)
			}
		})
	}
}

func TestAReservationTakenOverUnderAVersionNamedClaudeCodeStaysHeld(t *testing.T) {
	board := newHarness(t)
	board.processes = test.NewProcessesRunningAs("2.1.318")
	board.must("", "claim", "impl", "--session", test.OtherSession)

	job := *board
	job.session = test.OtherSession
	job.said(job.promptHook("start"), "prompt")

	view, code := job.holderJSON("--session", job.session, "--json")
	if code != 0 || view["alive"] != true {
		t.Errorf("the session's first hook should take the reservation over and hold it, got %d %v", code, view)
	}
}

func TestAFullSubagentsSeatUnderAVersionNamedClaudeCodeStaysHeld(t *testing.T) {
	h := newHarness(t)
	h.processes = test.NewProcessesRunningAs("2.1.318")
	h.must("", "claim", "main")
	h.spawned("afull000000000001", "agentic-helpers: full")
	h.bashBy("afull000000000001", "agentic-memory new --title x")

	got := h.run("a body", "--agent", "afull000000000001", "new", "--title", "Subagent finding on the gate", "--topic", "gate")
	if got.code != 0 {
		t.Errorf("a full subagent should file on the seat held for it, got %d %s", got.code, got.err)
	}

	if who := h.must("", "who"); strings.Contains(who, "gone") {
		t.Errorf("the seat held for the subagent should stay held by the running Claude Code process: %s", who)
	}
}
