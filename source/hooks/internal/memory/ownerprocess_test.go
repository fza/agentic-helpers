package memory_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestASeatStaysHeldWhateverClaudeCodesExecutableIsNamed(t *testing.T) {
	for _, name := range []string{"2.1.318", "node", "claude.exe"} {
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

func TestAClaimWithNoClaudeCodeAboveItIsRefused(t *testing.T) {
	h := newHarness(t)
	h.processes = test.NewProcessesOutsideClaude()

	got := h.run("", "claim", "main")

	want := fmt.Sprintf("agentic-memory: no Claude Code process above this call (nearest: zsh %d, login %d); "+
		"a seat needs one to hold it", test.ShellPID, test.LoginPID)
	if got.code != 1 || !strings.Contains(got.err, want) {
		t.Errorf("a claim with no Claude Code process above it should refuse, naming the processes there, got %d %q",
			got.code, got.err)
	}

	if tree := test.Tree(t, h.roots().Data); strings.Contains(tree, "seats/") || strings.Contains(tree, "shelves/") {
		t.Errorf("a refused claim should write no seat and no shelf:\n%s", tree)
	}

	if who := h.must("", "who"); strings.Contains(who, "main") {
		t.Errorf("a refused claim should leave who as it was, got:\n%s", who)
	}
}

func TestALauncherOutsideClaudeCodeStillReservesASeat(t *testing.T) {
	launcher := newHarness(t)
	launcher.processes = test.NewProcessesOutsideClaude()

	out := launcher.must("", "claim", "impl", "--session", test.OtherSession)
	if !strings.Contains(out, "reserved impl for session") {
		t.Errorf("a launcher's claim for another session should reserve the seat, got:\n%s", out)
	}
}

func TestAReservationStaysOneWhereNoClaudeCodeTakesItOver(t *testing.T) {
	board := newHarness(t)
	board.must("", "claim", "impl", "--session", test.OtherSession)

	job := *board
	job.session = test.OtherSession
	job.processes = test.NewProcessesOutsideClaude()
	job.must(job.promptHook("start"), "prompt")

	view, _ := board.holderJSON("impl", "--json")
	if view["reserved"] != true || view["alive"] != nil {
		t.Errorf("a hook with no Claude Code process above it should leave the reservation as it is, got %v", view)
	}
}

// A spawn finds its session's seat through `seat.Owner`, which falls back to
// the immediate parent, so it reaches the subagent hold with no Claude Code
// ancestor where Claude Code itself runs the hook and its command has turned
// unreadable since the claim: the seat still reads alive by name and start,
// while the owner walk no longer takes the process for Claude Code.
func TestNoSubagentSeatIsHeldWhereNoClaudeCodeIsAbove(t *testing.T) {
	h := newHarness(t)
	h.processes = test.NewProcessesRunningAs(test.NativeVersion)
	hook := h.processes.Table[test.HookPID]
	hook.Parent = test.OwnerPID
	h.processes.Table[test.HookPID] = hook
	h.must("", "claim", "main")

	owner := h.processes.Table[test.OwnerPID]
	owner.Hidden = true
	h.processes.Table[test.OwnerPID] = owner
	h.spawn("", "agentic-helpers: full\nDo the task.")

	registry, _, err := seat.LoadRegistry(h.store())
	if err != nil {
		t.Fatalf("reading the seat registry: %v", err)
	}

	if _, known := registry.IDOf("main-2"); known {
		t.Error("a full spawn with no Claude Code process above it should mint no seat for the subagent")
	}
}
