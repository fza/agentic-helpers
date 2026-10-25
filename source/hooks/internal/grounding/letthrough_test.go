package grounding_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/grounding"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func notebook(path string) map[string]any {
	return map[string]any{"session_id": "s1", "tool_name": "NotebookEdit", "tool_input": map[string]any{"notebook_path": path}}
}

func TestAGatedToolIsLetThroughWithANote(t *testing.T) {
	cases := []struct {
		name    string
		payload func(env grounding.Env) map[string]any
		draft   bool
	}{
		{name: "a question", payload: func(grounding.Env) map[string]any { return ask("s1") }},
		{name: "a draft written", payload: func(env grounding.Env) map[string]any { return save("Write", draftIn(t, env)) }, draft: true},
		{name: "a draft edited", payload: func(env grounding.Env) map[string]any { return edit(draftIn(t, env)) }, draft: true},
		{name: "a notebook edited under drafts", payload: func(env grounding.Env) map[string]any {
			return notebook(filepath.Join(env.ProjectDir, "drafts", "probe.ipynb"))
		}, draft: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := areaProject(t)
			got := run(t, env, "gate", tc.payload(env))

			if got.code != 0 || !got.noted || got.stderr != "" {
				t.Fatalf("a gated tool owing reads should go ahead with a note and no error, got exit %d, stdout %q, stderr %q",
					got.code, got.stdout, got.stderr)
			}

			if !strings.HasPrefix(got.said, "GROUNDING GATE: let through, reads owed.") || !strings.Contains(got.said, "This turn carries:") {
				t.Errorf("the note should say the call went ahead and what the turn still owes, got: %s", got.said)
			}

			if strings.Contains(got.said, "This draft is not grounded") != tc.draft {
				t.Errorf("only a draft's note should say its capture owes the reads, got: %s", got.said)
			}

			ground(t, env)
			passes(t, run(t, env, "gate", tc.payload(env)))
		})
	}
}

func TestAConfigErrorLetsAGatedToolThroughWithoutOwingReads(t *testing.T) {
	env := areaProject(t)
	ground(t, env)
	test.WriteFile(t, filepath.Join(env.ProjectDir, ".sdd", "grounding.yaml"), "listing_prefix: [unclosed")

	got := run(t, env, "gate", ask("s1"))
	if got.code != 0 || !got.noted || !strings.HasPrefix(got.said, "GROUNDING GATE: let through. ") ||
		strings.Contains(got.said, "reads owed") || !strings.Contains(got.said, "grounding.yaml") {
		t.Errorf("a broken config should let a question through naming the file, owing no reads, got exit %d: %s", got.code, got.said)
	}
}

func TestWhatStillRefuses(t *testing.T) {
	cases := []struct {
		name    string
		payload func(env grounding.Env) map[string]any
	}{
		{name: "a capture", payload: func(env grounding.Env) map[string]any { return bash("agentic-capture " + draftIn(t, env)) }},
		{name: "a plain sdd new", payload: func(grounding.Env) map[string]any { return bash("sdd new d tac 'a body'") }},
		{name: "a discarded stream", payload: func(grounding.Env) map[string]any { return bash("sdd search --term x 2>/dev/null") }},
		{name: "a short show", payload: func(grounding.Env) map[string]any { return bash("sdd show 20260925-144721-d-tac-a9x") }},
		{name: "an MCP capture", payload: func(grounding.Env) map[string]any {
			return map[string]any{"session_id": "s1", "tool_name": "mcp__sdd__start_procedure", "tool_input": map[string]any{"canonical": "capture"}}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := areaProject(t)
			got := run(t, env, "gate", tc.payload(env))

			if got.code != 2 || got.noted || !strings.HasPrefix(got.stderr, "GROUNDING GATE: refused.") {
				t.Errorf("a call outside the gated tools should still be refused, got exit %d, stdout %q, stderr %q",
					got.code, got.stdout, got.stderr)
			}
		})
	}
}

func TestADraftWrittenWhileReadsAreOwedIsNotGrounded(t *testing.T) {
	env := areaProject(t)
	draft := draftIn(t, env)

	run(t, env, "gate", save("Write", draft))
	record(t, env, save("Write", draft))
	run(t, env, "turn", map[string]any{"session_id": "s1", "prompt": "capture it"})

	refuses(t, run(t, env, "gate", bash("agentic-capture "+draft)), "neither read")
}

func TestATaskNotificationContinuesTheTurn(t *testing.T) {
	cases := []struct {
		name   string
		prompt string
		owes   bool
	}{
		{name: "a task notification", prompt: "<task-notification>\n<task-id>b1</task-id>\n<status>completed</status>\n</task-notification>"},
		{name: "a typed prompt", prompt: "what did the task find?", owes: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := areaProject(t)
			ground(t, env)
			run(t, env, "turn", map[string]any{"session_id": "s1", "prompt": tc.prompt})

			got := run(t, env, "gate", ask("s1"))
			if got.noted != tc.owes {
				t.Errorf("only a prompt the user typed should start a turn owing fresh reads, got: %+v", got)
			}
		})
	}
}
