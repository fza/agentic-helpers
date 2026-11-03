package memory_test

import (
	"strings"
	"testing"
)

func TestTheNudgeFiresAtMostTwicePerRung(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("Gate reads Bash only today", "finding")

	payload := hookPayload("", "", "")

	h.sensor(70)

	nudges := 0

	for range 4 {
		if strings.Contains(h.said(payload, "prompt"), "Context near compaction") {
			nudges++
		}
	}

	if nudges != 2 {
		t.Errorf("an unanswered nudge should fire on two prompts of a rung and then leave it to the backstop, fired %d times", nudges)
	}

	if owed := h.owedAfterStop(payload); !strings.Contains(owed, "two nudges unanswered") {
		t.Errorf("past the capped nudges Stop should still owe the backstop, got %q", owed)
	}

	h.file("Answer to the nudges here", "where work stands")
	h.said(payload, "prompt")
	h.sensor(80)

	if !strings.Contains(h.said(payload, "prompt"), "Context near compaction") {
		t.Error("an answer should re-arm the nudge for the next rung")
	}
}
