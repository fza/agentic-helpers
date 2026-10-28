package memory_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func (h *harness) callTool(tool string, input map[string]string) string {
	h.t.Helper()

	data, _ := json.Marshal(map[string]any{"session_id": h.session, "tool_name": tool, "tool_input": input})

	return toolContext(h.must(string(data), "tool"))
}

func TestWhenTakesEachTrigger(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	for _, when := range []string{"path:.autopilot/plan/parts/jobs/**", "command:pilot board upgrade", "tool:Agent"} {
		out := h.must("body", "new", "--title", "Trap that already cost time", "--kind", "trap", "--when", when)
		if !strings.Contains(out, "when: "+when) {
			t.Errorf("the filed line should carry the trigger %s:\n%s", when, out)
		}
	}

	for _, malformed := range []string{"path:", "file:x", "command:(", "path:[", "tool:two words"} {
		got := h.run("body", "new", "--title", "Trap with a bad trigger", "--kind", "trap", "--when", malformed)
		if got.code != 1 || !strings.Contains(got.err, "--when") {
			t.Errorf("a malformed trigger should be refused naming --when, got %d %q", got.code, got.err)
		}
	}

	if got := h.run("body", "new", "--title", "Note with a trigger", "--when", "tool:Agent"); got.code != 1 ||
		!strings.Contains(got.err, "--when is for a trap") {
		t.Errorf("--when on another kind should be refused, got %d %q", got.code, got.err)
	}

	answer := h.mcp(`{"name":"new","arguments":{"title":"Trap over MCP here","body":"b","kind":"trap","when":["tool:Agent"]}}`)
	if strings.Contains(answer, `"isError":true`) || !strings.Contains(answer, "when: tool:Agent") {
		t.Errorf("the MCP key when should declare the trigger: %s", answer)
	}

	answer = h.mcp(`{"name":"new","arguments":{"title":"Note over MCP here","body":"b","when":["tool:Agent"]}}`)
	if !strings.Contains(answer, `"isError":true`) || !strings.Contains(answer, "--when is for a trap") {
		t.Errorf("--when on a note over MCP should be refused: %s", answer)
	}
}

func TestATrapIsToldOncePerSession(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	trap := h.file("Gate edits break the hook", "editing internal/gate/gate.go breaks the hook", "--kind", "trap")

	first := h.callTool("Read", map[string]string{"file_path": filepath.Join(h.repo, "internal", "gate", "gate.go")})
	if !strings.Contains(first, "agentic-memory: trap for internal/gate/gate.go:\n  "+trap+" Gate edits break the hook  [") {
		t.Errorf("a call touching a trap's anchor should tell it:\n%s", first)
	}

	if again := h.callTool("Read", map[string]string{"file_path": filepath.Join(h.repo, "internal", "gate", "gate.go")}); strings.Contains(again, "trap for") {
		t.Errorf("a trap should be told once per session:\n%s", again)
	}

	h.must(hookPayload("compact", "", ""), "session-start")

	if after := h.callTool("Bash", map[string]string{"command": "cat internal/gate/gate.go"}); !strings.Contains(after, "trap for internal/gate/gate.go") {
		t.Errorf("a compaction should let the trap be told again:\n%s", after)
	}
}

func TestAReceivedTrapTriggers(t *testing.T) {
	h, other := twoSeats(t)
	trap := other.file("Spawning a builder loses the plan", "body", "--kind", "trap", "--when", "tool:Agent", "--for", "main")

	if out := h.callTool("Agent", map[string]string{"prompt": "build"}); !strings.Contains(out, "trap for Agent:\n  "+trap) {
		t.Errorf("a trap received through --for should trigger as the seat's own:\n%s", out)
	}
}

func TestASeatWithoutTrapsReadsNoTrapState(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("Gate note about internal/gate/gate.go", "note")

	if out := h.callTool("Read", map[string]string{"file_path": filepath.Join(h.repo, "internal", "gate", "gate.go")}); out != "" {
		t.Errorf("a seat holding no trap should hear nothing:\n%s", out)
	}

	for _, dir := range []string{"traps", "shown"} {
		entries, _ := os.ReadDir(filepath.Join(h.cacheDir(), dir))
		if len(entries) > 0 {
			t.Errorf("a seat holding no trap should write no %s state", dir)
		}
	}
}

func (h *harness) cacheDir() string {
	return filepath.Join(h.cache, "agentic-memory", filepath.FromSlash(repoID))
}

func TestOnAndProofBelongToARule(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	out := h.must("body", "new", "--title", "Arm the monitor on claim", "--kind", "rule", "--from", "user",
		"--on", "claim", "--proof", "tool:Monitor")
	if !strings.Contains(out, "on: claim; proof: tool:Monitor") {
		t.Errorf("the filed line should carry the moment and the proof:\n%s", out)
	}

	for _, tc := range []struct {
		args    []string
		refusal string
	}{
		{args: []string{"--on", "claim"}, refusal: "--on and --proof are for a rule"},
		{args: []string{"--kind", "trap", "--proof", "tool:Monitor"}, refusal: "--on and --proof are for a rule"},
		{args: []string{"--kind", "rule", "--from", "user", "--on", "commit"}, refusal: "--on takes claim or compact"},
		{args: []string{"--kind", "rule", "--from", "user", "--on", "claim", "--proof", "command:("}, refusal: "--proof"},
		{args: []string{"--kind", "rule", "--from", "user", "--proof", "tool:Monitor"}, refusal: "--proof needs --on"},
	} {
		got := h.run("body", append([]string{"new", "--title", "Rule owed at a moment"}, tc.args...)...)
		if got.code != 1 || !strings.Contains(got.err, tc.refusal) {
			t.Errorf("%v should be refused, got %d %q", tc.args, got.code, got.err)
		}
	}

	answer := h.mcp(`{"name":"new","arguments":{"title":"Rule over MCP here","body":"b","kind":"rule","from":"user",` +
		`"on":["compact"],"proof":["command:make test"]}}`)
	if strings.Contains(answer, `"isError":true`) || !strings.Contains(answer, "on: compact; proof: command:make test") {
		t.Errorf("the MCP keys on and proof should declare them: %s", answer)
	}

	answer = h.mcp(`{"name":"new","arguments":{"title":"Note over MCP here","body":"b","on":["claim"]}}`)
	if !strings.Contains(answer, `"isError":true`) {
		t.Errorf("--on on a note over MCP should be refused: %s", answer)
	}
}

func TestDoNowAtClaim(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	monitor := h.file("Arm the monitor on claim", "body", "--kind", "rule", "--from", "user", "--on", "claim",
		"--proof", "tool:Monitor")
	board := h.file("Read the board on claim", "body", "--kind", "rule", "--from", "user", "--on", "claim")
	h.file("Run the tests after compaction", "body", "--kind", "rule", "--from", "user", "--on", "compact")

	claimed := h.must("", "claim", "main")
	_, list, _ := strings.Cut(claimed, "do now (claim):\n")
	list, _, briefed := strings.Cut(list, "agentic-memory: seat main")

	if !briefed || !strings.Contains(list, monitor+" Arm the monitor on claim  proof: tool:Monitor\n") ||
		!strings.Contains(list, board+" Read the board on claim  ack when done\n") ||
		!strings.HasPrefix(list, "1. ") || !strings.Contains(list, "\n2. ") {
		t.Errorf("a claim should print its do-now list ahead of the brief, numbered, for that trigger only:\n%s", claimed)
	}

	if strings.Contains(claimed, "Run the tests after compaction  ack") {
		t.Errorf("a claim should leave out items owed at compaction:\n%s", claimed)
	}

	h.callTool("Monitor", map[string]string{"command": "tail -f x"})

	if shown := h.must("", "show", monitor); !strings.Contains(shown, "done by proof tool:Monitor by main") {
		t.Errorf("a proving tool call should settle the item, as an event on the rule:\n%s", shown)
	}

	got := h.run(hookPayload("", "", ""), "stop")
	held := test.AddedContext(got.out, "Stop")
	if got.code != 0 || !strings.Contains(held, "agentic-memory: do-now items pending:\n1. "+board+" Read the board on claim  ack when done") ||
		strings.Contains(held, monitor) {
		t.Errorf("Stop should hold the turn for the items still pending, got %d %q", got.code, held)
	}
}

func TestAProofBeforeTheMomentProvesNothing(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	tests := h.file("Run the tests after compaction", "body", "--kind", "rule", "--from", "user", "--on", "compact",
		"--proof", "command:make test")

	h.callTool("Bash", map[string]string{"command": "make test"})

	compacted := h.must(hookPayload("compact", "", ""), "session-start")
	if !strings.Contains(compacted, "do now (compact):\n1. "+tests) {
		t.Fatalf("the brief after a compaction should open with its do-now list:\n%s", compacted)
	}

	if got := h.run(hookPayload("", "", ""), "stop"); test.AddedContext(got.out, "Stop") == "" {
		t.Errorf("a proving call before the moment should leave the item pending, got %d %q", got.code, got.out)
	}

	h.callTool("Bash", map[string]string{"command": "ls"})

	if got := h.run(hookPayload("", "", ""), "stop"); test.AddedContext(got.out, "Stop") == "" {
		t.Errorf("a call proving nothing should leave the item pending, got %d %q", got.code, got.out)
	}

	h.callTool("Bash", map[string]string{"command": "make test"})

	if got := h.run(hookPayload("", "", ""), "stop"); got.code != 0 || got.out != "" {
		t.Errorf("a proving call after the moment should settle the item, got %d %q", got.code, got.out)
	}
}

func TestAckSettlesPendingItems(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	board := h.file("Read the board on claim", "body", "--kind", "rule", "--from", "user", "--on", "claim")
	mail := h.file("Answer the mail on claim", "body", "--kind", "rule", "--from", "user", "--on", "claim")
	other := h.file("Plain note not owed", "body")
	h.must("", "claim", "main")

	got := h.run("", "ack", board, other, "--done")
	if got.code != 1 || !strings.Contains(got.err, "agentic-memory: nothing acked:\n  "+other+" is not pending in this session") {
		t.Errorf("an id not pending should refuse the whole ack, got %d %q", got.code, got.err)
	}

	if shown := h.must("", "show", board); strings.Contains(shown, "done by ack") {
		t.Errorf("a refused ack should settle nothing:\n%s", shown)
	}

	if got := h.run("", "ack", board); got.code != 1 || !strings.Contains(got.err, "exactly one of --done or --decline") {
		t.Errorf("ack should need --done or --decline, got %d %q", got.code, got.err)
	}

	if out := h.must("", "ack", board, "--done"); !strings.Contains(out, "done "+board) {
		t.Errorf("ack --done should settle the item, got %q", out)
	}

	answer := h.mcp(`{"name":"ack","arguments":{"ids":["` + mail + `"],"decline":"no mail access here"}}`)
	if !strings.Contains(answer, "declined "+mail+": no mail access here") {
		t.Errorf("ack over MCP should decline with its reason: %s", answer)
	}

	if shown := h.must("", "show", mail); !strings.Contains(shown, "declined: no mail access here by main") {
		t.Errorf("a decline should be an event on the rule with its reason:\n%s", shown)
	}

	if got := h.run(hookPayload("", "", ""), "stop"); got.code != 0 || got.out != "" {
		t.Errorf("settled items should no longer hold Stop, got %d %q", got.code, got.out)
	}
}

func TestASessionEndSkipsWhatIsPending(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	board := h.file("Read the board on claim", "body", "--kind", "rule", "--from", "user", "--on", "claim")
	h.must("", "claim", "main")

	h.must(h.sessionEnd("other"), "session-end")

	if claimed := h.must("", "claim", "main"); !strings.Contains(claimed, "skipped last session:\n  "+board+" Read the board on claim (claim)") {
		t.Errorf("items pending as the session ended should open the next do-now list as skipped:\n%s", claimed)
	}
}

func TestStopHoldsTwiceThenSkips(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	board := h.file("Read the board on claim", "body", "--kind", "rule", "--from", "user", "--on", "claim")
	h.must("", "claim", "main")
	h.sensor(90)

	for range 2 {
		h.must(hookPayload("", "", ""), "prompt")
	}

	for round := range 2 {
		got := h.run(hookPayload("", "", ""), "stop")
		if held := test.AddedContext(got.out, "Stop"); got.code != 0 || !strings.Contains(held, "do-now items pending") ||
			strings.Contains(held, "Memory overdue") {
			t.Errorf("Stop %d should hold for the do-now items ahead of the backstop, got %d %q", round+1, got.code, held)
		}
	}

	if got := h.run(hookPayload("", "", ""), "stop"); strings.Contains(test.AddedContext(got.out, "Stop"), "do-now items pending") ||
		!strings.Contains(test.AddedContext(got.out, "Stop"), "Memory overdue") {
		t.Errorf("a third Stop should no longer hold for the items, and the backstop should follow, got %d %q", got.code, got.out)
	}

	h.must(h.sessionEnd("other"), "session-end")

	claimed := h.must("", "claim", "main")
	if !strings.Contains(claimed, "skipped last session:\n  "+board+" Read the board on claim (claim)") {
		t.Errorf("items left pending should open the next do-now list as skipped:\n%s", claimed)
	}

}
