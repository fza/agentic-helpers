package memory_test

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

const openHookAsk = ". Answered once each open hook line is cited in a line of yours, ticked or dropped: "

func TestAReminderNamesOpenHookLinesFromBeforeIt(t *testing.T) {
	h := commitHarness(t)
	h.must(h.stop(), "stop")
	line := h.hookLineID()
	h.clock.Advance(time.Minute)
	h.sensor(70)

	reminder := h.said(h.promptSaying("go on"), "prompt")
	if !strings.Contains(reminder, "< body"+openHookAsk+"state#"+line) {
		t.Errorf("a reminder should name a hook line the agent left open before it fired, got %q", reminder)
	}
}

func TestAHookLineFromAnEarlierClaimLeavesTheReminderAlone(t *testing.T) {
	h := commitHarness(t)
	h.must(h.stop(), "stop")
	h.hookLineID()
	h.must("", "release")
	h.clock.Advance(time.Minute)
	h.must("", "claim", "main")
	h.clock.Advance(time.Minute)
	h.sensor(70)

	if reminder := h.said(h.promptSaying("go on"), "prompt"); !strings.HasSuffix(reminder, "< body") {
		t.Errorf("a hook line the next claim ended should leave the reminder naming none, got %q", reminder)
	}
}

func TestWorkLandedBeforeTheReminderKeepsItOwed(t *testing.T) {
	cases := []struct {
		name     string
		answer   func(h *harness, line string)
		answered bool
	}{
		{name: "a throwaway line", answer: func(h *harness, _ string) { h.addLine("x") }},
		{name: "a tick of the landed line", answered: true, answer: func(h *harness, line string) { h.must("", "ack", "state#"+line, "--done") }},
		{name: "a line citing the landed commit", answered: true, answer: func(h *harness, _ string) { h.addLine("9c0ffee1 settles the gate fix") }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := commitHarness(t)
			h.must(h.stop(), "stop")
			line := h.hookLineID()
			h.clock.Advance(time.Minute)
			nudged(t, h)
			h.clock.Advance(time.Minute)

			tc.answer(h, line)
			h.clock.Advance(time.Minute)

			if again := h.nudgedAgain(); again == tc.answered {
				t.Errorf("a landed line older than the reminder should keep it owed until settled, nudged again: %v", again)
			}
		})
	}
}

func TestTheLedgerNudgeCountsAndListsOpenHookLines(t *testing.T) {
	cases := []struct {
		name   string
		settle func(h *harness, line string)
		listed bool
	}{
		{name: "the landed line left open", listed: true, settle: func(*harness, string) {}},
		{name: "the landed line cited", settle: func(h *harness, _ string) { h.addLine("9c0ffee1 settles the gate fix") }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := commitHarness(t)
			h.said(h.promptSaying("fix the gate for MCP reads"), "prompt")
			h.must(h.stop(), "stop")
			line := h.hookLineID()
			tc.settle(h, line)

			reply := ""
			for added := 0; added < 12 && !strings.Contains(reply, pressureOpens); added++ {
				_, reply = h.addLines(1, fmt.Sprintf("step %d", added))
			}

			if !strings.Contains(reply, pressureOpens) {
				t.Fatalf("adds past the cold high mark should draw the nudge, got:\n%s", reply)
			}

			named := strings.Contains(reply, "  #"+line+" (") && strings.Contains(reply, "agentic-memory ack state#"+line+" --done")
			if named != tc.listed {
				t.Errorf("the nudge should list an open, uncited landed line with its tick, and only then, listed %v:\n%s", named, reply)
			}

			if strings.Contains(reply, "asked") {
				t.Errorf("the nudge should never list an asked line, got:\n%s", reply)
			}
		})
	}
}

func TestAnOpenHookLineCountsTowardTheLedgerNudge(t *testing.T) {
	h := commitHarness(t)
	h.must(h.stop(), "stop")
	h.hookLineID()

	_, reply := h.addLines(10, "step")
	if !strings.Contains(reply, "ledger holds 11 "+pressureOpens) {
		t.Errorf("ten agent lines and one open landed line should stand past the cold high mark of ten, got:\n%s", reply)
	}
}
