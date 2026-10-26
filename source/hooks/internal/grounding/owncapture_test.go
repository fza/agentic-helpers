package grounding_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const ownEntry = "20260926-090000-d-tac-new"

// captureRan is the PostToolUse an `agentic-capture` call sends, printing
// stdout as `sdd new` printed it.
func captureRan(stdout string) map[string]any {
	return with(bash("agentic-capture .sdd/tmp/drafts/decision.md"), "tool_response", map[string]any{"stdout": stdout, "stderr": "", "interrupted": false})
}

// nextEnded is the PostToolUse sdd's MCP server sends for the `next` call
// ending a capture, its result JSON quoted inside a text block.
func nextEnded(t *testing.T, id string) map[string]any {
	t.Helper()

	result, err := json.Marshal(map[string]any{"procedure": "capture", "status": "completed", "produced": map[string]any{"entryId": id}})
	if err != nil {
		t.Fatalf("encoding the result: %v", err)
	}

	return map[string]any{
		"session_id":    "s1",
		"tool_name":     "mcp__sdd__next",
		"tool_input":    map[string]any{"instance": "i_2"},
		"tool_response": []any{map[string]any{"type": "text", "text": string(result)}},
	}
}

func TestOwnCaptureKeepsTheReads(t *testing.T) {
	cases := []struct {
		name  string
		lands []string
		after map[string]any
		keeps bool
	}{
		{name: "agentic-capture of the one new entry", lands: []string{"26-090000-d-tac-new.md"}, after: captureRan(ownEntry + ".md\n  → .sdd/graph/2026/09/26-090000-d-tac-new.md\n"), keeps: true},
		{name: "another entry landed beside it", lands: []string{"26-085900-d-tac-oth.md", "26-090000-d-tac-new.md"}, after: captureRan(ownEntry + ".md\n")},
		{name: "another session's entry is the newest", lands: []string{"26-090000-d-tac-new.md", "26-090100-d-tac-oth.md"}, after: captureRan(ownEntry + ".md\n")},
		{name: "the one new entry is another session's", lands: []string{"26-090100-d-tac-oth.md"}, after: captureRan(ownEntry + ".md\n")},
		{name: "a capture printing no id", lands: []string{"26-090000-d-tac-new.md"}, after: captureRan("captured\n")},
		{name: "the MCP capture of the one new entry", lands: []string{"26-090000-d-tac-new.md"}, keeps: true},
		{name: "an MCP capture naming an entry that is not the newest", lands: []string{"26-090000-d-tac-new.md", "26-090100-d-tac-oth.md"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := areaProject(t)
			ground(t, env)

			for _, name := range tc.lands {
				entryIn(t, env, name)
			}

			after := tc.after
			if after == nil {
				after = nextEnded(t, ownEntry)
			}

			record(t, env, after)

			got := run(t, env, "gate", ask("s1"))
			if tc.keeps {
				passes(t, got)

				return
			}

			refuses(t, got, "neither read")
		})
	}
}

func TestTheNextCallIsRecorded(t *testing.T) {
	wiring, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "plugin", "hooks", "hooks.json"))
	if err != nil {
		t.Fatalf("reading the wiring: %v", err)
	}

	if !strings.Contains(string(wiring), "|mcp__sdd__next\"") {
		t.Error("PostToolUse should run the record on sdd's next call, where an MCP capture ends")
	}
}
