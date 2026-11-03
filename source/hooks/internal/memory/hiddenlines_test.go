package memory_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

// hiddenAnswer is a hook's stdout read as Claude Code reads it: nothing, or
// one JSON answer naming its event.
func hiddenAnswer(t *testing.T, out string) (string, string) {
	t.Helper()

	if out == "" {
		return "", ""
	}

	var answer struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}

	decoder := json.NewDecoder(strings.NewReader(out))

	err := decoder.Decode(&answer)
	if err != nil || decoder.More() {
		t.Fatalf("a memory hook should print one JSON answer or nothing, printed %q", out)
	}

	return answer.HookSpecificOutput.HookEventName, answer.HookSpecificOutput.AdditionalContext
}

// owedAfterStop runs Stop, which must print nothing, and answers what the
// next prompt tells the agent.
func (h *harness) owedAfterStop(stop string) string {
	h.t.Helper()

	got := h.run(stop, "stop")
	if got.code != 0 || got.out != "" {
		h.t.Fatalf("Stop should hold nothing and print nothing, got %d %q", got.code, got.out)
	}

	return h.said(hookPayload("", "", "go on"), "prompt")
}

func TestNoMemoryHookPrintsWhatTheUserSees(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("Read the board on claim", "body", "--kind", "rule", "--from", "user", "--on", "claim")
	h.file("Gate reads Bash only today", "finding")
	h.must("", "claim", "main")

	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	test.AppendTurns(t, path, h.clock.Now().Add(time.Hour), 10, 1000)

	h.sensor(90)

	toolPayload := fmt.Sprintf(`{"session_id":%q,"tool_name":"Read","tool_input":{"file_path":"x"}}`, h.session)

	cases := []struct {
		name  string
		stdin string
		args  []string
		event string
	}{
		{name: "the brief after a compaction", stdin: hookPayload("compact", path, ""), args: []string{"session-start"}, event: "SessionStart"},
		{name: "a replay piece", stdin: hookPayload("compact", path, ""), args: []string{"session-log", "1"}, event: "SessionStart"},
		{name: "a prompt's nudge", stdin: hookPayload("", path, "go on"), args: []string{"prompt"}, event: "UserPromptSubmit"},
		{name: "a second prompt's nudge", stdin: hookPayload("", path, "go on"), args: []string{"prompt"}, event: "UserPromptSubmit"},
		{name: "a Stop owing a line", stdin: hookPayload("", path, ""), args: []string{"stop"}},
		{name: "a tool call carrying the owed line", stdin: toolPayload, args: []string{"tool"}, event: "PostToolUse"},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			got := h.run(each.stdin, each.args...)
			event, said := hiddenAnswer(t, got.out)

			if got.code != 0 {
				t.Errorf("the hook should exit 0, got %d %q", got.code, got.err)
			}

			switch {
			case each.event == "" && got.out != "":
				t.Errorf("Stop should print nothing, since its context always shows the user, printed %q", got.out)
			case each.event != "" && (event != each.event || said == ""):
				t.Errorf("the hook should speak only through its event's hidden context, printed %q", got.out)
			}
		})
	}
}

func TestALineOwedAtStopArrivesOnceWithTheNextHook(t *testing.T) {
	cases := []struct {
		name  string
		stdin func(h *harness) string
		args  []string
		event string
	}{
		{name: "a prompt", stdin: func(h *harness) string { return hookPayload("", "", "go on") }, args: []string{"prompt"}, event: "UserPromptSubmit"},
		{
			name:  "a tool call",
			stdin: func(h *harness) string { return bashCall(h.session, "ls") },
			args:  []string{"tool"}, event: "PostToolUse",
		},
		{
			name:  "a memory call's PreToolUse",
			stdin: func(h *harness) string { return bashCall(h.session, "agentic-memory show state") },
			args:  []string{"pre-tool"}, event: "PreToolUse",
		},
		{name: "a resumed session", stdin: func(h *harness) string { return hookPayload("resume", "", "") }, args: []string{"session-start"}, event: "SessionStart"},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")
			board := h.file("Read the board on claim", "body", "--kind", "rule", "--from", "user", "--on", "claim")
			h.must("", "claim", "main")

			stopped := h.run(h.stop(), "stop")
			if stopped.code != 0 || stopped.out != "" {
				t.Fatalf("Stop should hold nothing and print nothing, got %d %q", stopped.code, stopped.out)
			}

			got := h.run(each.stdin(h), each.args...)
			event, said := hiddenAnswer(t, got.out)

			if event != each.event || !strings.Contains(said, "agentic-memory: do-now items pending:\n1. "+board) {
				t.Errorf("the line Stop owed should reach the agent as the next hook's hidden context, got %q", got.out)
			}

			again := h.run(each.stdin(h), each.args...)
			if _, said := hiddenAnswer(t, again.out); strings.Contains(said, "do-now items pending") {
				t.Errorf("a line Stop owed should go out once, got %q", again.out)
			}
		})
	}
}

func TestASubagentsCallLeavesTheOwedLineToTheSeat(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("Read the board on claim", "body", "--kind", "rule", "--from", "user", "--on", "claim")
	h.must("", "claim", "main")
	h.must(h.stop(), "stop")

	subagent := fmt.Sprintf(`{"session_id":%q,"agent_id":"asub0001","tool_name":"Bash","tool_input":{"command":"echo agentic-memory"}}`,
		h.session)
	if got := h.must(subagent, "pre-tool"); got != "" {
		t.Errorf("a subagent's PreToolUse should never take the line Stop owed the seat, got %q", got)
	}

	if strings.Contains(toolContext(h.must(subagent, "tool")), "do-now items pending") {
		t.Error("a subagent's call should never take the line Stop owed the seat")
	}

	if !strings.Contains(toolContext(h.must(bashCall(h.session, "ls"), "tool")), "do-now items pending") {
		t.Error("the seat's own next call should carry the line Stop owed")
	}
}

func TestAPreToolUseAnsweringTheCallLeavesTheOwedLineToItsPostToolUse(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("Read the board on claim", "body", "--kind", "rule", "--from", "user", "--on", "claim")
	h.must("", "claim", "main")
	h.must(h.stop(), "stop")

	call := bashCall(h.session, "agentic-memory --agent asub0001 show state")

	var refusal struct {
		HookSpecificOutput struct {
			PermissionDecision string `json:"permissionDecision"`
		} `json:"hookSpecificOutput"`
	}

	decoder := json.NewDecoder(strings.NewReader(h.must(call, "pre-tool")))
	if decoder.Decode(&refusal) != nil || decoder.More() || refusal.HookSpecificOutput.PermissionDecision != "deny" {
		t.Error("a PreToolUse refusing the call should answer with the refusal alone")
	}

	if !strings.Contains(toolContext(h.must(call, "tool")), "do-now items pending") {
		t.Error("the call's PostToolUse should carry the line its PreToolUse left")
	}
}
