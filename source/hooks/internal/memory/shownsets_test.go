package memory_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

// subagentFile is a file Claude Code writes beside a subagent's transcript.
func (h *harness) subagentFile(agentID string, suffix string) string {
	return filepath.Join(strings.TrimSuffix(h.sessionTranscript(), ".jsonl"), "subagents", "agent-"+agentID+suffix)
}

// forked spawns a fork of the spawner through the Agent call callID with the
// marker line, and writes what Claude Code writes for it: a transcript opening
// with the reference to its parent's context, and the meta file naming the
// call it came from.
func (h *harness) forked(spawner string, agentID string, marker string, callID string) {
	h.t.Helper()

	input := map[string]any{"description": "a fork", "prompt": marker + "\nDo the task.", "subagent_type": "fork"}
	h.preTool(spawner, "Agent", input, callID)

	reference, _ := json.Marshal(map[string]any{
		"type": "fork-context-ref", "agentId": agentID, "parentSessionId": h.session, "contextLength": 12,
	})
	test.WriteFile(h.t, h.subagentFile(agentID, ".jsonl"), string(reference)+"\n")

	meta, _ := json.Marshal(map[string]any{"agentType": "fork", "isFork": true, "toolUseId": callID})
	test.WriteFile(h.t, h.subagentFile(agentID, ".meta.json"), string(meta))
}

// shellAs runs the agentic-memory call the subagent's Bash command makes, as
// the PreToolUse hook hands it on; the main thread where agentID is empty.
func (h *harness) shellAs(agentID string, command string) ran {
	h.t.Helper()

	answer := h.bashBy(agentID, command)
	if answer.HookSpecificOutput.PermissionDecision != "" {
		h.t.Fatalf("the hook should let %q through, got %+v", command, answer)
	}

	if rewritten := answer.command(h.t); rewritten != "" {
		command = rewritten
	}

	words := strings.Fields(command)
	if words[0] != "agentic-memory" {
		h.t.Fatalf("the command should be a plain agentic-memory call, got %q", command)
	}

	return h.run("", words[1:]...)
}

// mcpAs runs the memory tool call the subagent makes with the arguments, as
// the PreToolUse hook hands them on.
func (h *harness) mcpAs(agentID string, tool string, arguments map[string]any) string {
	h.t.Helper()

	answer := h.preTool(agentID, "mcp__plugin_agentic-helpers_memory__"+tool, arguments, "toolu_mcp_read")
	if answer.HookSpecificOutput.PermissionDecision != "" {
		h.t.Fatalf("the hook should let the %s call through, got %+v", tool, answer)
	}

	sent, _ := json.Marshal(arguments)
	if answer.HookSpecificOutput.UpdatedInput != nil {
		sent = answer.HookSpecificOutput.UpdatedInput
	}

	call, _ := json.Marshal(map[string]any{"name": tool, "arguments": json.RawMessage(sent)})

	return h.mcp(string(call))
}

func seenOverMCP(answer string) bool {
	return strings.Contains(answer, `\"seen\":true`) || strings.Contains(answer, `"seen":true`)
}

func TestAFreshSubagentReadsAnEntryInFullOnceWithoutAgain(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	id := h.file("Gate reads Bash only", "the gate body")
	h.must("", "show", id)
	h.spawned("afresh000000001", "agentic-helpers: silent")

	first := h.shellAs("afresh000000001", "agentic-memory show "+id)
	if first.code != 0 || !strings.Contains(first.out, "the gate body") {
		t.Errorf("a fresh subagent's first show should print the body in full, got %d %q %q", first.code, first.out, first.err)
	}

	second := h.shellAs("afresh000000001", "agentic-memory show "+id)
	if !strings.Contains(second.out, h.seenLine(id, "Gate reads Bash only")) {
		t.Errorf("a fresh subagent's second show should print a pointer, got %q %q", second.out, second.err)
	}
}

func TestAForkStartsWithItsSpawnersShownSetAtTheSpawn(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	before := h.file("Gate reads Bash only", "read before the spawn")
	after := h.file("Guard parses every command", "read after the spawn")
	h.must("", "show", before)
	h.forked("", "afork0000000001", "agentic-helpers: silent", "toolu_01ForkSpawn")
	h.must("", "show", after)

	if out := h.shellAs("afork0000000001", "agentic-memory show "+before).out; !strings.Contains(out,
		h.seenLine(before, "Gate reads Bash only")) {
		t.Errorf("a fork should get a pointer for an entry its spawner read before the spawn, got %q", out)
	}

	if out := h.shellAs("afork0000000001", "agentic-memory show "+after).out; !strings.Contains(out, "read after the spawn") {
		t.Errorf("a fork should get the body of an entry its spawner read after the spawn, got %q", out)
	}
}

func TestTheHookTellsAForkFromAFreshSubagent(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	id := h.file("Gate reads Bash only", "the gate body")
	h.must("", "show", id)

	h.forked("", "afork0000000002", "agentic-helpers: silent", "toolu_01ForkTwo")
	h.spawned("afresh000000002", "agentic-helpers: silent")

	if out := h.shellAs("afork0000000002", "agentic-memory show "+id).out; strings.Contains(out, "the gate body") {
		t.Errorf("a fork should hold what its spawner was shown, got %q", out)
	}

	if out := h.shellAs("afresh000000002", "agentic-memory show "+id).out; !strings.Contains(out, "the gate body") {
		t.Errorf("a fresh subagent should hold nothing its spawner was shown, got %q", out)
	}
}

func TestASubagentsReadLeavesItsSpawnersSetAlone(t *testing.T) {
	reads := map[string]func(h *harness, agentID string, id string) bool{
		"Bash": func(h *harness, agentID string, id string) bool {
			return !strings.Contains(h.shellAs(agentID, "agentic-memory show "+id).out, "(seen)")
		},
		"MCP": func(h *harness, agentID string, id string) bool {
			return !seenOverMCP(h.mcpAs(agentID, "show", map[string]any{"ids": []string{id}}))
		},
	}

	for subagentBy, subagentReads := range reads {
		for parentBy, parentReads := range reads {
			t.Run("subagent by "+subagentBy+", parent by "+parentBy, func(t *testing.T) {
				h := newHarness(t)
				h.must("", "claim", "main")
				id := h.file("Gate reads Bash only", "the gate body")
				h.spawned("ashared00000003", "agentic-helpers: shared")

				if !subagentReads(h, "ashared00000003", id) {
					t.Fatal("the subagent's first read should print the entry in full")
				}

				if !parentReads(h, "", id) {
					t.Error("the spawner's read should print in full an entry only its subagent read")
				}
			})
		}
	}
}

func TestAMemoryToolCallNamesItsSubagent(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.spawned("asilent00000005", "agentic-helpers: silent")

	answer := h.preTool("asilent00000005", "mcp__plugin_agentic-helpers_memory__search",
		map[string]any{"term": []string{"gate"}, "agent": "aother000000005"}, "toolu_search")

	var updated map[string]any

	_ = json.Unmarshal(answer.HookSpecificOutput.UpdatedInput, &updated)
	if updated["agent"] != "asilent00000005" || updated["term"] == nil {
		t.Errorf("a subagent's memory tool call should carry its own id and keep its keys, got %+v", answer)
	}

	command := h.bashBy("asilent00000005", "agentic-memory search --term gate").command(t)
	if command != "agentic-memory --agent asilent00000005 search --term gate" {
		t.Errorf("a silent subagent's Bash call should carry its own id, got %q", command)
	}
}

func TestACallNamingASubagentFromTheParentIsRefused(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	id := h.file("Gate reads Bash only", "the gate body")
	h.spawned("asilent00000006", "agentic-helpers: silent")

	bash := h.bashBy("", "agentic-memory --agent asilent00000006 show "+id)
	if bash.HookSpecificOutput.PermissionDecision != "deny" ||
		!strings.Contains(bash.HookSpecificOutput.Reason, "--agent is set by the hook for a subagent reading memory") {
		t.Errorf("the parent's --agent should be refused, got %+v", bash)
	}

	tool := h.preTool("", "mcp__plugin_agentic-helpers_memory__show",
		map[string]any{"ids": []string{id}, "agent": "asilent00000006"}, "toolu_parent_show")
	if tool.HookSpecificOutput.PermissionDecision != "deny" ||
		!strings.Contains(tool.HookSpecificOutput.Reason, "agent is set by the hook for a subagent reading memory") {
		t.Errorf("the parent's agent key should be refused, got %+v", tool)
	}

	unknown := h.mcp(`{"name":"show","arguments":{"ids":["` + id + `"],"agent":"anobody00000006"}}`)
	if !strings.Contains(unknown, "agent names no subagent of the calling session reading memory") {
		t.Errorf("the server should refuse an agent no subagent record names, got %q", unknown)
	}

	cli := h.run("", "--agent", "anobody00000006", "show", id)
	if cli.code == 0 || !strings.Contains(cli.err, "--agent names no subagent of the calling session reading memory") {
		t.Errorf("the command line should refuse an --agent no subagent record names, got %d %q", cli.code, cli.err)
	}
}

func TestANestedReadOfASubagentIsRefused(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.spawned("asilent00000007", "agentic-helpers: silent")

	answer := h.bashBy("asilent00000007", "bash -c 'agentic-memory search --term gate'")
	if answer.HookSpecificOutput.PermissionDecision != "deny" ||
		!strings.Contains(answer.HookSpecificOutput.Reason, "only a direct call carries the subagent's agent id") {
		t.Errorf("a subagent's read inside code another program runs should be refused, got %+v", answer)
	}
}

// sessionSlots is every key the store's session slot file holds.
func (h *harness) sessionSlots() []string {
	h.t.Helper()

	held, err := slots.Open(h.roots().Cache, filepath.Join(h.cacheDir(), "sessions.slots")).All(context.Background())
	if err != nil {
		h.t.Fatalf("the session slots should read: %v", err)
	}

	var keys []string
	for _, slot := range held {
		keys = append(keys, slot.Key)
	}

	return keys
}

func TestASubagentsSetGoesAtItsStopAndAtTheSessionsEnd(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	id := h.file("Gate reads Bash only", "the gate body")
	h.must("", "show", id)
	h.spawned("asilent00000008", "agentic-helpers: silent")
	h.forked("", "afork0000000009", "agentic-helpers: silent", "toolu_01ForkNine")
	h.forked("", "afork0000000010", "agentic-helpers: silent", "toolu_01ForkTen")

	h.shellAs("asilent00000008", "agentic-memory show "+id)
	h.shellAs("afork0000000009", "agentic-memory show "+id)
	h.subagentStops("asilent00000008")
	h.subagentStops("afork0000000010")

	if out := h.shellAs("asilent00000008", "agentic-memory show "+id).out; !strings.Contains(out, "the gate body") {
		t.Errorf("a subagent's shown set should be gone after its SubagentStop, got %q", out)
	}

	for _, key := range h.sessionSlots() {
		if strings.HasPrefix(key, "spawn/"+h.session+"/") {
			t.Errorf("a fork's spawn copy should go once the fork took it over or stopped, found %s", key)
		}
	}

	h.forked("", "afork0000000011", "agentic-helpers: silent", "toolu_01ForkEleven")

	h.must(h.sessionEnd("other"), "session-end")

	for _, key := range h.sessionSlots() {
		if strings.HasPrefix(key, "shown/"+h.session+"/") || strings.HasPrefix(key, "spawn/"+h.session+"/") {
			t.Errorf("the session's end should remove every subagent's set and every spawn's copy, found %s", key)
		}
	}
}
