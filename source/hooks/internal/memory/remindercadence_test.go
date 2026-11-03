package memory_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

const reminderOpening = "Context near compaction"

func (h *harness) readCall(agentID string) string {
	h.t.Helper()

	if agentID == "" {
		return h.must(fmt.Sprintf(`{"session_id":%q,"tool_name":"Read","tool_input":{"file_path":"x"}}`, h.session), "tool")
	}

	return h.must(fmt.Sprintf(`{"session_id":%q,"agent_id":%q,"tool_name":"Read","tool_input":{"file_path":"x"}}`,
		h.session, agentID), "tool")
}

// sensorNow writes a context reading taken at the harness clock's now.
func (h *harness) sensorNow(percent float64) {
	h.t.Helper()

	test.WriteFile(h.t, h.sensorPath(h.session),
		fmt.Sprintf(`{"used_percentage":%v,"size":200000,"at":%d}`, percent, h.clock.Now().Unix()))
}

func TestTheReminderRepeatsAtEveryPromptUntilAnswered(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("Gate reads Bash only today", "finding")

	payload := hookPayload("", "", "")

	h.sensor(70)

	for prompt := range 4 {
		said := h.said(payload, "prompt")
		if strings.Count(said, reminderOpening) != 1 {
			t.Errorf("prompt %d should carry the reminder once while it goes unanswered, got %q", prompt+1, said)
		}
	}

	if got := h.run(payload, "stop"); got.out != "" {
		t.Errorf("a reminder owed should never reach Stop, which the user sees, got %q", got.out)
	}

	h.clock.Advance(10 * time.Minute)

	if !strings.Contains(h.said(payload, "prompt"), reminderOpening) {
		t.Error("an owed reminder should repeat once the context reading went stale, until answered")
	}

	h.file("Answer to the reminders here", "where work stands")

	if strings.Contains(h.said(payload, "prompt"), reminderOpening) {
		t.Error("an answer should stop the repeats at once")
	}

	h.sensorNow(80)

	if !strings.Contains(h.said(payload, "prompt"), reminderOpening) {
		t.Error("an answer should re-arm the reminder for the next rung")
	}
}

func TestTheReminderRepeatsAtEveryTenthToolCall(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("Gate reads Bash only today", "finding")

	h.sensor(70)

	var reminded []int

	for call := 1; call <= 25; call++ {
		said := toolContext(h.readCall(""))
		if strings.Count(said, reminderOpening) > 1 {
			t.Errorf("call %d should carry the reminder at most once, got %q", call, said)
		}

		if strings.Contains(said, reminderOpening) {
			reminded = append(reminded, call)
		}
	}

	if fmt.Sprint(reminded) != "[10 20]" {
		t.Errorf("the reminder should go out with every tenth tool call while owed, went out at calls %v", reminded)
	}

	h.file("Answer to the reminders here", "where work stands")

	for call := 26; call <= 30; call++ {
		if strings.Contains(toolContext(h.readCall("")), reminderOpening) {
			t.Errorf("an answered reminder should not repeat at call %d", call)
		}
	}
}

func TestASubagentsCallsCountTowardNoReminder(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.sensor(70)

	for call := 1; call <= 10; call++ {
		if strings.Contains(toolContext(h.readCall("asub0001")), reminderOpening) {
			t.Errorf("a subagent's call %d should never carry the seat's reminder", call)
		}
	}

	for call := 1; call <= 9; call++ {
		if strings.Contains(toolContext(h.readCall("")), reminderOpening) {
			t.Errorf("the seat's call %d should not be a tenth one, its subagent's calls counting for nothing", call)
		}
	}

	if !strings.Contains(toolContext(h.readCall("")), reminderOpening) {
		t.Error("the seat's own tenth call should carry the reminder")
	}
}
