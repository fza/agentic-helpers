package memory_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

// preToolAnswer is what the PreToolUse hook printed: its decision, and the
// input it hands on in place of the one sent.
type preToolAnswer struct {
	HookSpecificOutput struct {
		HookEventName      string          `json:"hookEventName"`
		PermissionDecision string          `json:"permissionDecision"`
		Reason             string          `json:"permissionDecisionReason"`
		UpdatedInput       json.RawMessage `json:"updatedInput"`
	} `json:"hookSpecificOutput"`
}

func readPreTool(t *testing.T, out string) preToolAnswer {
	t.Helper()

	var answer preToolAnswer
	if strings.TrimSpace(out) == "" {
		return answer
	}

	err := json.Unmarshal([]byte(out), &answer)
	if err != nil {
		t.Fatalf("the PreToolUse answer should be JSON: %v\n%s", err, out)
	}

	return answer
}

// sessionTranscript is the harness session's transcript, beside which Claude
// Code writes each subagent's own.
func (h *harness) sessionTranscript() string {
	return filepath.Join(filepath.Dir(h.home), "projects", h.session+".jsonl")
}

// preTool runs the PreToolUse hook for a call of tool with input, made by the
// subagent agentID, or the main thread where it is empty.
func (h *harness) preTool(agentID string, tool string, input map[string]any, callID string) preToolAnswer {
	h.t.Helper()

	payload := map[string]any{
		"session_id": h.session, "transcript_path": h.sessionTranscript(), "hook_event_name": "PreToolUse",
		"tool_name": tool, "tool_input": input, "tool_use_id": callID,
	}
	if agentID != "" {
		payload["agent_id"], payload["agent_type"] = agentID, "general-purpose"
	}

	data, _ := json.Marshal(payload)

	return readPreTool(h.t, h.must(string(data), "pre-tool"))
}

// spawn runs the hook on an Agent call with the prompt and answers the
// prompt the child receives, failing where the hook answered a permission
// decision.
func (h *harness) spawn(spawner string, prompt string) string {
	h.t.Helper()

	input := map[string]any{"description": "a task", "prompt": prompt, "subagent_type": "general-purpose"}
	answer := h.preTool(spawner, "Agent", input, "toolu_spawn")

	if answer.HookSpecificOutput.PermissionDecision != "" {
		h.t.Fatalf("the prompt rewrite should never answer the permission decision, got %q",
			answer.HookSpecificOutput.PermissionDecision)
	}

	if answer.HookSpecificOutput.UpdatedInput == nil {
		return prompt
	}

	var updated map[string]any

	err := json.Unmarshal(answer.HookSpecificOutput.UpdatedInput, &updated)
	if err != nil {
		h.t.Fatalf("the updated input should be an object: %v", err)
	}

	if updated["description"] != "a task" || updated["subagent_type"] != "general-purpose" {
		h.t.Errorf("the updated input should keep every key it does not change, got %v", updated)
	}

	child, _ := updated["prompt"].(string)

	return child
}

// start writes the subagent's own transcript, which opens with the prompt its
// Agent call handed it, as Claude Code writes it before its first tool call.
func (h *harness) start(agentID string, prompt string) {
	h.t.Helper()

	record, _ := json.Marshal(map[string]any{
		"type": "user", "agentId": agentID, "message": map[string]any{"role": "user", "content": prompt},
	})
	path := filepath.Join(strings.TrimSuffix(h.sessionTranscript(), ".jsonl"), "subagents", "agent-"+agentID+".jsonl")
	test.WriteFile(h.t, path, string(record)+"\n")
}

// spawned is a subagent spawned by the main thread with the marker line, its
// prompt written where its hooks read it.
func (h *harness) spawned(agentID string, marker string) string {
	h.t.Helper()

	child := h.spawn("", marker+"\nDo the task.")
	h.start(agentID, child)

	return child
}

func (h *harness) bashBy(agentID string, command string) preToolAnswer {
	h.t.Helper()

	return h.preTool(agentID, "Bash", map[string]any{"command": command, "description": "a call"}, "toolu_bash")
}

func (answer preToolAnswer) command(t *testing.T) string {
	t.Helper()

	var updated struct {
		Command string `json:"command"`
	}

	_ = json.Unmarshal(answer.HookSpecificOutput.UpdatedInput, &updated)

	return updated.Command
}

// toolBy runs the PostToolUse hook on a Read call the subagent made, and
// answers what it was told.
func (h *harness) toolBy(agentID string) string {
	h.t.Helper()

	payload := map[string]any{
		"session_id": h.session, "transcript_path": h.sessionTranscript(), "tool_name": "Read",
		"tool_input": map[string]string{"file_path": filepath.Join(h.repo, "README.md")},
	}
	if agentID != "" {
		payload["agent_id"] = agentID
	}

	data, _ := json.Marshal(payload)

	return test.AddedContext(h.must(string(data), "tool"), "PostToolUse")
}

func (h *harness) subagentStops(agentID string) {
	h.t.Helper()

	data, _ := json.Marshal(map[string]any{
		"session_id": h.session, "transcript_path": h.sessionTranscript(), "agent_id": agentID,
		"agent_transcript_path": filepath.Join(strings.TrimSuffix(h.sessionTranscript(), ".jsonl"), "subagents",
			"agent-"+agentID+".jsonl"),
	})
	h.must(string(data), "subagent-stop")
}

func TestAPromptWithoutMarkerGainsTheScratchFolderAlone(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("Commit only when asked", "ask first", "--kind", "rule", "--from", "user")

	child := h.spawn("", "Find where the gate reads its config.")

	if !strings.HasPrefix(child, "scratch: .tmp/seats/main/\n") || !strings.HasSuffix(child, "\nFind where the gate reads its config.") {
		t.Errorf("a prompt without a marker should gain the scratch folder above it, got:\n%s", child)
	}

	for _, unwanted := range []string{"agentic-helpers level:", "Commit only when asked", "agentic-memory:"} {
		if strings.Contains(child, unwanted) {
			t.Errorf("a prompt without a marker should gain no level, rules or memory line, got:\n%s", child)
		}
	}
}

func TestAPromptOutsideAnyScratchFolderGainsTheMachineLineAlone(t *testing.T) {
	h := newHarness(t)

	child := h.spawn("", "Find where the gate reads its config.")
	if child != "agentic-helpers: this machine: timeout -> agentic-helpers bound\n\nFind where the gate reads its config." {
		t.Errorf("a prompt in a project without .tmp/ should gain the machine line alone, got:\n%s", child)
	}
}

func TestEachLevelsBlock(t *testing.T) {
	cases := []struct {
		level    string
		wanted   []string
		unwanted []string
	}{
		{"none", []string{"scratch: .tmp/seats/main/"}, []string{"agentic-helpers level:", "Commit only when asked", "agentic-memory:"}},
		{"rules", []string{"agentic-helpers level: rules", "rules (from the user", "  ", "Commit only when asked", "    ask first"},
			[]string{"agentic-memory:"}},
		{"silent", []string{"agentic-helpers level: silent", "Commit only when asked", "agentic-memory: read-only on seat main",
			"agentic-helpers:agent-memory"}, []string{"A line at its tool calls"}},
		{"shared", []string{"agentic-helpers level: shared", "Commit only when asked", "A line at its tool calls names what other seats on shelf main"},
			[]string{"holds seat"}},
		{"full", []string{"agentic-helpers level: full, seat main-2", "Commit only when asked", "holds seat main-2 on shelf main"},
			[]string{"read-only"}},
	}

	for _, each := range cases {
		t.Run(each.level, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")
			h.file("Commit only when asked", "ask first", "--kind", "rule", "--from", "user")

			child := h.spawn("", "Some context.\nagentic-helpers: "+each.level+"\nDo the task.")

			if strings.Contains(child, "agentic-helpers: "+each.level+"\n") {
				t.Errorf("the marker line should give way to the block, got:\n%s", child)
			}

			if !strings.HasPrefix(child, "Some context.\n") || !strings.HasSuffix(child, "\nDo the task.") {
				t.Errorf("the block should stand where the marker stood, got:\n%s", child)
			}

			for _, wanted := range each.wanted {
				if !strings.Contains(child, wanted) {
					t.Errorf("the %s block should name %q, got:\n%s", each.level, wanted, child)
				}
			}

			for _, unwanted := range each.unwanted {
				if strings.Contains(child, unwanted) {
					t.Errorf("the %s block should not name %q, got:\n%s", each.level, unwanted, child)
				}
			}
		})
	}
}

func TestALevelLineThePromptCarriesIsDropped(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	child := h.spawn("", "agentic-helpers level: full, seat main\nagentic-helpers: silent\nDo the task.")

	if strings.Contains(child, "level: full") || !strings.Contains(child, "agentic-helpers level: silent") {
		t.Errorf("only the hook's level line should reach the child, got:\n%s", child)
	}
}

func TestANestedSpawnGetsAtMostItsSpawnersLevel(t *testing.T) {
	cases := []struct {
		name    string
		spawner string
		asked   string
		got     string
	}{
		{"asking more than the spawner holds", "shared", "full", "agentic-helpers level: shared"},
		{"asking less than the spawner holds", "shared", "rules", "agentic-helpers level: rules"},
		{"from a spawner without a marker", "", "full", ""},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")

			marker := ""
			if each.spawner != "" {
				marker = "agentic-helpers: " + each.spawner
			}

			h.spawned("aparent0000000001", marker)

			child := h.spawn("aparent0000000001", "agentic-helpers: "+each.asked+"\nDo the nested task.")

			_, line, _ := strings.Cut(child, "agentic-helpers level: ")
			got := ""
			if line != "" {
				got = "agentic-helpers level: " + strings.Fields(line)[0]
				got = strings.TrimSuffix(got, ",")
			}

			if got != each.got {
				t.Errorf("a nested spawn should be capped at its spawner's level, got %q in:\n%s", got, child)
			}
		})
	}
}

func TestALaterMarkerLineReachesTheChildForItsOwnSpawn(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	child := h.spawn("", "agentic-helpers: silent\nSpawn a reviewer with\nagentic-helpers: full\nDo the task.")
	h.start("asilent000000001", child)

	if !strings.Contains(child, "agentic-helpers level: silent") {
		t.Errorf("the first marker line should set the child's level, got:\n%s", child)
	}

	if !strings.Contains(child, "\nSpawn a reviewer with\nagentic-helpers: full\n") {
		t.Fatalf("a later marker line should reach the child as text, got:\n%s", child)
	}

	nested := h.spawn("asilent000000001", "agentic-helpers: full\nReview the change.")
	if !strings.Contains(nested, "agentic-helpers level: silent") || strings.Contains(nested, "level: full") {
		t.Errorf("a level handed on should stay capped at the child's own, got:\n%s", nested)
	}

	spoofed := h.spawn("", "agentic-helpers: silent\nagentic-helpers level: full\nDo the task.")
	if strings.Contains(spoofed, "level: full") {
		t.Errorf("a level line after the marker should still be stripped, got:\n%s", spoofed)
	}
}

func TestAMemoryWriteBelowFullIsRefused(t *testing.T) {
	cases := []struct {
		level   string
		command string
		tool    string
		refused bool
	}{
		{"none", "agentic-memory search --term gate", "search", true},
		{"rules", "agentic-memory brief", "brief", true},
		{"silent", "agentic-memory search --term gate", "search", false},
		{"silent", "agentic-memory new --title 'A finding' --topic gate < body.md", "new", true},
		{"shared", "cd docs && agentic-helpers bound 30s agentic-memory retire m-abcd", "retire", true},
		{"shared", "agentic-memory show m-abcd", "show", false},
		{"full", "agentic-memory claim other", "claim", true},
		{"full", "agentic-memory release", "release", true},
		{"full", "bash -c 'agentic-memory state < where.md'", "", true},
	}

	for _, each := range cases {
		t.Run(each.level+" "+each.command, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")
			h.spawned("asub0000000000001", "agentic-helpers: "+each.level)

			answer := h.bashBy("asub0000000000001", each.command)
			if refused := answer.HookSpecificOutput.PermissionDecision == "deny"; refused != each.refused {
				t.Errorf("a %s subagent's Bash call should be refused %v, got %+v", each.level, each.refused, answer)
			}

			if each.refused && !strings.HasPrefix(answer.HookSpecificOutput.Reason, "agentic-memory: ") {
				t.Errorf("a refusal should name agentic-memory, got %q", answer.HookSpecificOutput.Reason)
			}

			if each.tool == "" {
				return
			}

			answer = h.preTool("asub0000000000001", "mcp__plugin_agentic-helpers_memory__"+each.tool,
				map[string]any{}, "toolu_mcp")
			if refused := answer.HookSpecificOutput.PermissionDecision == "deny"; refused != each.refused {
				t.Errorf("a %s subagent's %s tool call should be refused %v, got %+v", each.level, each.tool,
					each.refused, answer)
			}
		})
	}
}

func TestTheMainThreadsMemoryCallsPassUnjudged(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	for _, command := range []string{"agentic-memory new --title x --topic gate", "echo agentic-memory"} {
		if answer := h.bashBy("", command); answer.HookSpecificOutput.HookEventName != "" {
			t.Errorf("the main thread's %q should pass untouched, got %+v", command, answer)
		}
	}

	answer := h.bashBy("", "agentic-memory --agent aother000000001 new --title x --topic gate")
	if answer.HookSpecificOutput.PermissionDecision != "deny" ||
		!strings.Contains(answer.HookSpecificOutput.Reason, "--agent is set by the hook") {
		t.Errorf("an --agent the main thread types should be refused, got %+v", answer)
	}
}

func TestAFullSubagentFilesOnItsOwnSeatThroughBash(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.spawned("afull000000000001", "agentic-helpers: full")

	answer := h.bashBy("afull000000000001",
		"agentic-memory --agent asibling00000001 show m-abcd && agentic-helpers bound 30s agentic-memory new --title x")

	want := "agentic-memory --agent afull000000000001 show m-abcd && agentic-helpers bound 30s agentic-memory " +
		"--agent afull000000000001 new --title x"
	if got := answer.command(t); got != want || answer.HookSpecificOutput.PermissionDecision != "" {
		t.Errorf("every call should name the calling agent and nothing else, got %q (%q)", got,
			answer.HookSpecificOutput.PermissionDecision)
	}

	filed := h.must("a body", "--agent", "afull000000000001", "new", "--title", "Subagent finding on the gate", "--topic", "gate")
	id := idPattern.FindString(filed)

	if out := h.must("", "show", id); !strings.Contains(out, "main-2") {
		t.Errorf("a full subagent's entry should name its own seat, got:\n%s", out)
	}

	h.subagentStops("afull000000000001")

	if test.Exists(t, filepath.Join(h.store(), "seats", seatID(t, h.store(), "main-2")+".json")) {
		t.Error("SubagentStop should release the subagent's seat")
	}

	got := h.run("another body", "--agent", "afull000000000001", "new", "--title", "Late finding", "--topic", "gate")
	if got.code == 0 {
		t.Error("a call naming a stopped subagent should be refused")
	}
}

func TestATypedAgentFlagIsRefused(t *testing.T) {
	cases := []struct {
		name    string
		level   string
		refusal string
	}{
		{"no such subagent", "", "--agent names no subagent of the calling session reading memory"},
		{"a subagent below full", "shared", "which reads memory but never writes it"},
		{"a subagent without memory", "rules", "which gives it no memory access"},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")

			if each.level == "shared" {
				h.spawned("ashared000000001", "agentic-helpers: "+each.level)
				answer := h.bashBy("ashared000000001", "agentic-memory --agent=afull0000000001 search --term gate")

				if got := answer.command(t); got != "agentic-memory --agent ashared000000001 search --term gate" {
					t.Errorf("a typed --agent should be replaced by the calling agent, got %q", got)
				}
			}

			if each.level == "rules" {
				h.spawned("ashared000000001", "agentic-helpers: "+each.level)
				h.toolBy("ashared000000001")
			}

			got := h.run("body", "--agent", "ashared000000001", "new", "--title", "Typed flag", "--topic", "gate")
			if got.code == 0 || !strings.Contains(got.err, each.refusal) {
				t.Errorf("an --agent should be refused where its subagent may not make the call, got %d %q", got.code,
					got.err)
			}
		})
	}
}

func TestAFullSubagentFilesOnItsOwnSeatThroughMCP(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.spawned("afull000000000002", "agentic-helpers: full")

	answer := h.preTool("afull000000000002", "mcp__plugin_agentic-helpers_memory__new",
		map[string]any{"title": "x"}, "toolu_01NewFromSubagent")
	if answer.HookSpecificOutput.PermissionDecision != "" ||
		string(answer.HookSpecificOutput.UpdatedInput) != `{"agent":"afull000000000002","title":"x"}` {
		t.Errorf("a full subagent's write should pass its PreToolUse naming the subagent alone, got %+v", answer)
	}

	out := h.mcp(`{"name":"new","arguments":{"title":"Subagent finding over MCP","topic":["gate"],"body":"seen",` +
		`"agent":"afull000000000002"}}`)
	id := idPattern.FindString(out)

	if shown := h.must("", "show", id); !strings.Contains(shown, "main-2") {
		t.Errorf("an MCP write by a full subagent should land on its own seat, got:\n%s", shown)
	}

	out = h.mcp(`{"name":"new","arguments":{"title":"Finding without an agent key","topic":["gate"],"body":"seen"},` +
		`"_meta":{"claudecode/toolUseId":"toolu_01NewFromSubagent"}}`)
	if shown := h.must("", "show", idPattern.FindString(out)); strings.Contains(shown, "main-2") {
		t.Errorf("an MCP call carrying no agent key should stay on the session's seat, whatever its call id, got:\n%s",
			shown)
	}
}

func TestShelfNoticesReachSharedAndFullSubagentsAlone(t *testing.T) {
	cases := []struct {
		level string
		told  bool
	}{
		{"silent", false},
		{"shared", true},
		{"full", true},
	}

	for _, each := range cases {
		t.Run(each.level, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")
			mate := newHarnessSharing(h)
			mate.must("", "claim", "--shelf", "main")

			agent := "asub" + each.level + "00000001"
			h.spawned(agent, "agentic-helpers: "+each.level)
			h.toolBy(agent)
			h.toolBy("")

			mate.file("Gate config moved to gate.yaml", "the old path is gone")

			told := h.toolBy(agent)
			if strings.Contains(told, "Gate config moved") != each.told {
				t.Errorf("a %s subagent should be told of a shelf mate's entry %v, got:\n%s", each.level, each.told, told)
			}

			if again := h.toolBy(agent); strings.Contains(again, "Gate config moved") {
				t.Errorf("a subagent should be told of an entry once, got:\n%s", again)
			}

			if parent := h.toolBy(""); !strings.Contains(parent, "Gate config moved") {
				t.Errorf("the seat should still be told of what its subagent was told, got:\n%s", parent)
			}
		})
	}
}

func TestTheParentIsToldOfItsFullSubagentsEntry(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.spawned("afull000000000003", "agentic-helpers: full")
	h.toolBy("afull000000000003")
	h.toolBy("")

	h.must("found it", "--agent", "afull000000000003", "new", "--title", "Subagent saw the gate", "--topic", "gate")

	if told := h.toolBy(""); !strings.Contains(told, "Subagent saw the gate") {
		t.Errorf("the parent seat should be told of its full subagent's entry, got:\n%s", told)
	}
}

func TestASessionEndReleasesItsSubagentsSeats(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.spawn("", "agentic-helpers: full\nDo the task.")

	claim := filepath.Join(h.store(), "seats", seatID(t, h.store(), "main-2")+".json")
	if !test.Exists(t, claim) {
		t.Fatal("a full spawn should hold a seat for the subagent")
	}

	held, _ := seat.Read(filepath.Dir(claim), seatID(t, h.store(), "main-2"))
	if held.Agent != "pending" {
		t.Errorf("a seat held for a subagent not started yet should wait for its agent id, got %q", held.Agent)
	}

	if out := h.must("", "who"); !strings.Contains(out, "main") {
		t.Errorf("who should still answer, got:\n%s", out)
	}

	h.must(h.sessionEnd("other"), "session-end")

	if test.Exists(t, claim) {
		t.Error("a session end should release the seats its subagents still held")
	}
}

func TestTheSessionsOwnSeatIsNeverTheSubagents(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.spawned("afull000000000004", "agentic-helpers: full")
	h.toolBy("afull000000000004")

	filed := h.file("Main thread finding", "body")
	if out := h.must("", "show", filed); strings.Contains(out, "main-2") {
		t.Errorf("the session's own calls should stay on its seat while a subagent holds one, got:\n%s", out)
	}

	if out := h.must("", "brief"); !strings.Contains(out, "seat main,") {
		t.Errorf("the session's brief should be its own seat's, got:\n%s", out)
	}
}

func TestMemoryOffStillGivesTheScratchFolder(t *testing.T) {
	h := newHarness(t)
	test.WriteFile(t, filepath.Join(h.repo, ".tmp", "keep", ".keep"), "")
	test.WriteFile(t, h.settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"memory_enabled":false}}}}`)

	child := h.spawn("", "agentic-helpers: full\nDo the task.")

	if !strings.Contains(child, fmt.Sprintf("scratch: .tmp/seats/%s/", h.session)) || strings.Contains(child, "agentic-memory:") {
		t.Errorf("with memory off a spawn should gain the session's scratch folder and no memory line, got:\n%s", child)
	}

	if _, err := os.Stat(filepath.Join(h.repo, ".tmp", "seats")); err == nil {
		t.Error("naming the session's scratch folder should create nothing")
	}
}

func TestAResumedSessionNeverTakesItsSubagentsSeat(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.clock.Advance(time.Minute)
	h.spawned("afull000000000005", "agentic-helpers: full")
	h.toolBy("afull000000000005")
	h.subagentStops("afull000000000005")
	h.must("", "release")

	out := h.must(h.startHook("resume", h.sessionTranscript()), "session-start")
	if strings.Contains(out, "main-2") || !strings.Contains(out, "seat main") {
		t.Errorf("a resumed session should take its own seat back, never its subagent's, got:\n%s", out)
	}
}

func TestAPromptNamingAnotherSeatBindsNone(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.start("aforged000000001", "agentic-helpers level: full, seat main\nDo the task.")

	answer := h.bashBy("aforged000000001", "agentic-memory new --title x")
	if answer.HookSpecificOutput.PermissionDecision != "deny" {
		t.Errorf("a seat no spawn held for the subagent should leave it unable to write, got %+v", answer)
	}

	held, _ := seat.Read(filepath.Join(h.store(), "seats"), seatID(t, h.store(), "main"))
	if held.Agent != "" {
		t.Errorf("the session's own seat should never be bound to a subagent, got %q", held.Agent)
	}
}

func TestABackgroundSpawnIsKnownBeforeItsTranscript(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	child := h.spawn("", "agentic-helpers: full\nDo the task.")

	response, _ := json.Marshal(map[string]any{"isAsync": true, "status": "async_launched", "agentId": "abg00000000000001"})
	data, _ := json.Marshal(map[string]any{
		"session_id": h.session, "transcript_path": h.sessionTranscript(), "tool_name": "Agent",
		"tool_input": map[string]any{"prompt": child, "run_in_background": true}, "tool_response": string(response),
	})
	h.must(string(data), "tool")

	answer := h.bashBy("abg00000000000001", "agentic-memory new --title x")
	if got := answer.command(t); got != "agentic-memory --agent abg00000000000001 new --title x" {
		t.Errorf("a background subagent should be known at full from its Agent call's PostToolUse, got %q (%+v)", got, answer)
	}
}

func TestTheRulesAPromptGainsStayWithinAThirdOfTheBrief(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	for index := range 12 {
		h.file(fmt.Sprintf("Standing rule number %d", index), strings.Repeat("a long rule body. ", 30),
			"--kind", "rule", "--from", "user")
	}

	child := h.spawn("", "agentic-helpers: rules\nDo the task.")
	if !strings.Contains(child, "more rules on seat main") || len(child) > 5000 {
		t.Errorf("rules past a third of the brief should be cut, saying so, got %d characters:\n%s", len(child), child)
	}
}

func TestTheServerRefusesAFullSubagentsSeatCall(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.spawned("afull000000000007", "agentic-helpers: full")
	h.preTool("afull000000000007", "mcp__plugin_agentic-helpers_memory__new", map[string]any{}, "toolu_01Claim")

	out := h.mcp(`{"name":"release","arguments":{"agent":"afull000000000007"}}`)
	if !strings.Contains(out, "stays with the session that spawned the subagent") {
		t.Errorf("the server should refuse a seat call a full subagent made, got:\n%s", out)
	}
}
