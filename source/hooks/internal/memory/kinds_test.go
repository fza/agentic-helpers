package memory_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestNewFilesEachKind(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	for _, tc := range []struct {
		kind  string
		extra []string
		shows string
	}{
		{kind: "", shows: "[main, note, from agent, "},
		{kind: "note", shows: "[main, note, from agent, "},
		{kind: "todo", shows: "[main, todo, from agent, "},
		{kind: "trap", shows: "[main, trap, from agent, "},
		{kind: "rule", extra: []string{"--from", "user"}, shows: "[main, rule, from user, "},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			args := []string{"new", "--title", "Gate entry of one kind", "--topic", "gate"}
			if tc.kind != "" {
				args = append(args, "--kind", tc.kind)
			}

			out := h.must("body", append(args, tc.extra...)...)
			if !strings.Contains(out, tc.shows) {
				t.Errorf("the filed line should name the entry's kind:\n%s", out)
			}
		})
	}

	if got := h.run("body", "new", "--title", "Gate entry of no kind", "--kind", "finding"); got.code != 1 ||
		!strings.Contains(got.err, "--kind takes rule, trap, todo, note") {
		t.Errorf("an unknown kind should be refused naming the kinds, got %d %q", got.code, got.err)
	}
}

func TestRetentionIsRefusedNamingKind(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	got := h.run("body", "new", "--title", "Gate entry filed the old way", "--retention", "long")
	if got.code != 1 || !strings.Contains(got.err, "new takes no --retention: --kind rule|trap|todo|note") {
		t.Errorf("--retention should be refused naming --kind, got %d %q", got.code, got.err)
	}

	answer := h.mcp(`{"name":"new","arguments":{"title":"Gate entry filed the old way","body":"b","retention":"long"}}`)
	if !strings.Contains(answer, `"isError":true`) || !strings.Contains(answer, `new takes no key \"retention\": --kind`) {
		t.Errorf("the MCP key retention should be refused naming kind: %s", answer)
	}

	answer = h.mcp(`{"name":"new","arguments":{"title":"Gate entry filed as a trap","body":"b","kind":"trap"}}`)
	if strings.Contains(answer, `"isError":true`) || !strings.Contains(answer, "[main, trap, from agent, ") {
		t.Errorf("the MCP key kind should file the entry with its kind: %s", answer)
	}

	answer = h.mcp(`{"name":"new","arguments":{"title":"Gate rule filed by the agent","body":"b","kind":"rule"}}`)
	if !strings.Contains(answer, `"isError":true`) || !strings.Contains(answer, "--kind rule needs --from user") {
		t.Errorf("a rule not from the user should be refused over MCP too: %s", answer)
	}
}

func TestRekind(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	agents := h.file("Gate finding filed by the agent", "finding")
	users := h.file("Gate instruction the user gave", "instruction", "--from", "user")

	if out := h.must("", "rekind", agents, "todo"); !strings.Contains(out, agents+" is now a todo") {
		t.Errorf("rekind should say the entry's new kind, got %q", out)
	}

	if out := h.must("", "show", agents); !strings.Contains(out, "[main, todo, ") || !strings.Contains(out, "rekinded to todo by main") {
		t.Errorf("a rekinded entry should show its new kind and the event:\n%s", out)
	}

	if got := h.run("", "rekind", agents, "finding"); got.code != 1 || !strings.Contains(got.err, "rekind takes rule, trap, todo, note") {
		t.Errorf("rekind to no kind should be refused naming the kinds, got %d %q", got.code, got.err)
	}

	got := h.run("", "rekind", agents, "rule")
	if got.code != 1 || !strings.Contains(got.err, "only an entry filed --from user becomes a rule") {
		t.Errorf("rekinding an agent's entry to a rule should be refused, got %d %q", got.code, got.err)
	}

	h.must("", "rekind", users, "rule")

	if out := h.must("", "brief"); !strings.Contains(out, "rules (from the user") || !strings.Contains(out, users) {
		t.Errorf("an entry rekinded to a rule should move to the rules:\n%s", out)
	}

	other := newHarnessSharing(h)
	other.must("", "claim", "drive")

	if got := other.run("", "rekind", agents, "note"); got.code != 1 || !strings.Contains(got.err, "is on shelf main") {
		t.Errorf("a seat on another shelf should be refused a rekind, got %d %q", got.code, got.err)
	}
}

func TestTheStateSlot(t *testing.T) {
	h := newHarness(t)

	if got := h.run("halfway", "state"); got.code != 1 || !strings.Contains(got.err, "claim a seat first") {
		t.Errorf("a state write without a seat should be refused, got %d %q", got.code, got.err)
	}

	h.must("", "claim", "main")

	if got := h.run("  \n", "state"); got.code != 1 || !strings.Contains(got.err, "state needs a body") {
		t.Errorf("an empty state should be refused, got %d %q", got.code, got.err)
	}

	if out := h.must("halfway through the gate fix", "state"); !strings.Contains(out, "state of main written") {
		t.Errorf("a state write should say whose slot it wrote, got %q", out)
	}

	h.clock.Advance(3 * time.Hour)

	brief := strings.Split(h.must("", "brief"), "\n")
	if len(brief) < 2 || !strings.Contains(brief[0], "0 live") ||
		brief[1] != "state (3h old, branch main): halfway through the gate fix" {
		t.Errorf("the brief should open with the state and its age, counting no entry:\n%s", strings.Join(brief, "\n"))
	}

	h.must("the gate fix is done, tests pass", "state")

	if out := h.must("", "brief"); strings.Contains(out, "halfway") || !strings.Contains(out, "the gate fix is done") {
		t.Errorf("a second state write should replace the first:\n%s", out)
	}

	for _, args := range [][]string{{"search", "--term", "gate fix"}, {"search", "--status", "live"}} {
		if out := h.must("", args...); out != "no entry matches\n" {
			t.Errorf("the state slot should never be found as an entry by %v, got %q", args, out)
		}
	}

	answer := h.mcp(`{"name":"state","arguments":{"body":"written over MCP"}}`)
	if strings.Contains(answer, `"isError":true`) || !strings.Contains(h.must("", "brief"), "written over MCP") {
		t.Errorf("the MCP tool state should write the slot: %s", answer)
	}
}

func TestAStateWriteMovesTheReplayAnchor(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("Gate reads Bash only today", "finding")

	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	test.AppendTurns(t, path, h.clock.Now().Add(time.Hour), 10, 100)

	h.clock.Advance(3 * time.Hour)
	h.must("where the gate work stands", "state")
	test.AppendTurns(t, path, h.clock.Now().Add(time.Minute), 2, 100)

	out := h.must(hookPayload("compact", path, ""), "session-log", "1")
	if !strings.Contains(out, "turn 1 ") || strings.Contains(out, "turn 5 ") {
		t.Errorf("the replay should start at the state write:\n%s", out)
	}
}

func TestAStateWriteAnswersTheCompactionNudge(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("Gate reads Bash only today", "finding")

	payload := hookPayload("", "", "")
	h.sensor(70)

	if !strings.Contains(h.must(payload, "prompt"), "Context near compaction. Write where work stands first: "+
		"agentic-memory state < body. File an entry only for a finding, an open question or a judgment call") {
		t.Fatal("the nudge should ask for a state write first")
	}

	h.must("where the gate work stands", "state")

	if strings.Contains(h.must(payload, "prompt"), "Context near compaction") {
		t.Error("a state write after the nudge should answer it")
	}
}

func TestAStateWriteRearmsTheReplayNudge(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("Gate reads Bash only today", "finding")

	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	prompt := hookPayload("", path, "")

	test.AppendTurns(t, path, h.clock.Now().Add(time.Hour), 60, 1000)

	if !strings.Contains(h.must(prompt, "prompt"), "no longer fit the post-compaction replay") {
		t.Fatal("turns outgrowing the replay should nudge")
	}

	test.AppendTurns(t, path, h.clock.Now().Add(2*time.Hour), 20, 1000)

	if strings.Contains(h.must(prompt, "prompt"), "no longer fit the post-compaction replay") {
		t.Fatal("the replay nudge should fire once until the seat writes")
	}

	h.clock.Advance(4 * time.Hour)
	h.must("where the gate work stands", "state")
	test.AppendTurns(t, path, h.clock.Now().Add(time.Minute), 60, 1000)

	if !strings.Contains(h.must(prompt, "prompt"), "no longer fit the post-compaction replay") {
		t.Error("a state write should re-arm the replay nudge")
	}
}

func TestTheBriefOrdersItsSections(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	note := h.file("Gate note of the seat", "note")
	h.file("Gate todo of the seat", "todo", "--kind", "todo")
	h.file("Gate trap of the seat", "trap", "--kind", "trap")
	h.file("Gate rule of the user", "rule", "--kind", "rule", "--from", "user")
	h.must("where the gate work stands", "state")
	h.must("", "brief")

	other := newHarnessSharing(h)
	other.clock.Advance(time.Minute)
	other.must("", "claim", "drive")
	news := other.file("Drive finding after the brief", "news")
	other.must("", "link", note, news)

	brief := h.must("", "brief")

	order := []string{
		"agentic-memory: seat main", "state (", "changed by other seats:", "rules (from the user", "traps:", "todos:",
		"notes:", "new from other seats:", "topics:",
	}

	last := -1

	for _, marker := range order {
		at := strings.Index(brief, marker)
		if at <= last {
			t.Fatalf("the brief should hold %q after the sections before it:\n%s", marker, brief)
		}

		last = at
	}
}

func TestRecallNeverOffersTheStateSlot(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	prompt := hookPayload("", "", "how does the tail seal manifest rename work")
	target := h.file("Tail seal manifest rename", "the tail seal writes the manifest by rename")

	for _, word := range unrelated[:19] {
		h.file("Unrelated finding about "+word, word+" "+word+" "+word)
	}

	h.must("halfway through the tail seal manifest rename", "state")
	h.clock.Advance(time.Hour)

	out := h.must(prompt, "prompt")
	if !strings.Contains(out, target) {
		t.Fatalf("the prompt should recall the entry it touches:\n%s", out)
	}

	if strings.Contains(out, "halfway through") {
		t.Errorf("recall should offer entries only, never the seat's state slot:\n%s", out)
	}
}
