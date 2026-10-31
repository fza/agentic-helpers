package memory_test

import (
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestTheNudgeFiresAtMostTwicePerRung(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("Gate reads Bash only today", "finding")

	payload := hookPayload("", "", "")

	h.sensor(70)

	nudges := 0

	for range 4 {
		if strings.Contains(h.must(payload, "prompt"), "Context near compaction") {
			nudges++
		}
	}

	if nudges != 2 {
		t.Errorf("an unanswered nudge should fire on two prompts of a rung and then leave it to the backstop, fired %d times", nudges)
	}

	got := h.run(payload, "stop")
	if !strings.Contains(test.AddedContext(got.out, "Stop"), "two nudges unanswered") {
		t.Errorf("past the capped nudges the Stop backstop should still hold, got %q %q", got.out, got.err)
	}

	h.file("Answer to the nudges here", "where work stands")
	h.must(payload, "prompt")
	h.sensor(80)

	if !strings.Contains(h.must(payload, "prompt"), "Context near compaction") {
		t.Error("an answer should re-arm the nudge for the next rung")
	}
}
