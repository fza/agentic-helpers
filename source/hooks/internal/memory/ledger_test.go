package memory_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

var lineAddress = regexp.MustCompile(`state#([a-z0-9]{3})`)

func (h *harness) promptSaying(prompt string) string {
	data, _ := json.Marshal(map[string]string{"session_id": h.session, "prompt": prompt})

	return string(data)
}

// addLine adds one line to the seat's ledger and answers its id: a line
// whose extra names no --until ends on none.
func (h *harness) addLine(line string, extra ...string) string {
	h.t.Helper()

	if !slices.Contains(extra, "--until") {
		extra = append(extra, "--until", "none")
	}

	out := h.must("", append([]string{"ack", "state", "--add", line}, extra...)...)

	found := lineAddress.FindStringSubmatch(out)
	if found == nil {
		h.t.Fatalf("an add should name the line it added, got %q", out)
	}

	return found[1]
}

// stateOf is the brief's state section: its lines up to the next section.
func stateOf(brief string) string {
	var kept []string

	inside := false

	for _, line := range strings.Split(brief, "\n") {
		switch {
		case strings.HasPrefix(line, "state"):
			inside = true
		case inside && !strings.HasPrefix(line, "  "):
			return strings.Join(kept, "\n")
		}

		if inside {
			kept = append(kept, line)
		}
	}

	return strings.Join(kept, "\n")
}

func TestAckStateChangesTheCallingSeatsLedger(t *testing.T) {
	cases := []struct {
		name   string
		change func(h *harness, id string) string
		open   bool
		shown  string
	}{
		{name: "an added line stands open", change: func(*harness, string) string { return "" }, open: true, shown: "[ ]"},
		{name: "done ticks it", change: func(h *harness, id string) string { return h.must("", "ack", "state#"+id, "--done") },
			shown: "[x]"},
		{name: "remove takes it out", change: func(h *harness, id string) string { return h.must("", "ack", "state#"+id, "--remove") }},
		{name: "undo brings a ticked line back", open: true, shown: "[ ]", change: func(h *harness, id string) string {
			h.must("", "ack", "state#"+id, "--done")

			return h.must("", "ack", "state#"+id, "--undo")
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")
			id := h.addLine("gate fix halfway; next: docs")

			reply := tc.change(h, id)
			if reply != "" && strings.Contains(reply, "ledger holds") {
				t.Errorf("a short ledger should draw no crowding note, got %q", reply)
			}

			brief := stateOf(h.must("", "brief"))
			if strings.Contains(brief, "#"+id+": gate fix halfway; next: docs") != tc.open {
				t.Errorf("the brief should list the line exactly while it is open, got:\n%s", brief)
			}

			shown := h.must("", "show", "state", "--all-items")
			if tc.shown == "" && strings.Contains(shown, "#"+id) {
				t.Errorf("show state should leave a removed line out, got:\n%s", shown)
			}

			if tc.shown != "" && !strings.Contains(shown, tc.shown+" #"+id+": gate fix halfway; next: docs") {
				t.Errorf("show state should list the line with its state, got:\n%s", shown)
			}
		})
	}
}

func TestAckStatePinsWhatItNames(t *testing.T) {
	h := newHarness(t)
	withGraph(t, h)
	h.must("", "claim", "main")
	finding := h.file("Gate reads Bash only today", "finding")
	todo := h.file("Wire the gate into MCP", "body", "--kind", "todo", "--item", "read the schema")
	item := itemPattern.FindStringSubmatch(h.must("", "show", todo, "--again"))[1]

	cases := []struct {
		name string
		pin  string
		show string
	}{
		{name: "a memory id", pin: finding, show: ": Gate reads Bash only today"},
		{name: "a todo item", pin: todo + "#" + item, show: ": read the schema  → " + todo + "#" + item + " open"},
		{name: "a graph id", pin: activeDecision, show: ": " + activeDecision},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := h.addLine(tc.pin)

			if brief := stateOf(h.must("", "brief")); !strings.Contains(brief, "#"+id+tc.show) {
				t.Errorf("a pinned line should show its target, got:\n%s", brief)
			}
		})
	}
}

func TestAnotherSeatsLedgerIsOutOfReach(t *testing.T) {
	h, other := twoSeats(t)
	id := h.addLine("main's own line")

	if shown := other.must("", "show", "state"); strings.Contains(shown, "main's own line") {
		t.Errorf("a seat should read its own ledger alone, got:\n%s", shown)
	}

	before := h.logBytes()

	got := other.run("", "ack", "state#"+id, "--done")
	if got.code != 1 || !strings.Contains(got.err, "state#"+id+": no such line") {
		t.Errorf("an address of another seat's line should name no line of the caller's, got %d %q", got.code, got.err)
	}

	if !h.logUnchanged(before) {
		t.Error("a refused ack should write nothing")
	}
}

func TestAckStateRefusesWhatIsNoLedgerChange(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	todo := h.file("Wire the gate into MCP", "body", "--kind", "todo", "--item", "read the schema")
	rule := h.file("Read the board on claim", "body", "--kind", "rule", "--from", "user", "--on", "claim")
	id := h.addLine("a line")
	removed := h.addLine("a removed line")
	h.must("", "ack", "state#"+removed, "--remove")

	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "until on a todo's items", args: []string{"ack", todo, "--add", "x", "--until", "claim"}, want: "--until declares the ends"},
		{name: "until on a rule", args: []string{"ack", rule, "--done", "--until", "claim"}, want: "--until declares the ends"},
		{name: "until without add", args: []string{"ack", "state#" + id, "--done", "--until", "claim"}, want: "--until declares the ends"},
		{name: "a state id beside an entry", args: []string{"ack", "state#" + id, rule, "--done"}, want: "an ack call of their own"},
		{name: "a decline", args: []string{"ack", "state#" + id, "--decline", "why"}, want: "exactly one of --add, --done"},
		{name: "two changes", args: []string{"ack", "state#" + id, "--done", "--remove"}, want: "exactly one of --add, --done"},
		{name: "an add on a line", args: []string{"ack", "state#" + id, "--add", "x"}, want: "--add takes the one id state"},
		{name: "an unknown line", args: []string{"ack", "state#zzz", "--done"}, want: "state#zzz: no such line"},
		{name: "a removed line", args: []string{"ack", "state#" + removed, "--done"}, want: "state#" + removed + ": no such line"},
		{name: "old with no old slot", args: []string{"ack", "state#old", "--done"}, want: "state#old: no such line"},
		{name: "a bad until", args: []string{"ack", "state", "--add", "x", "--until", "soon"}, want: "--until takes"},
		{name: "an add without until", args: []string{"ack", "state", "--add", "x"},
			want: "ack state --add needs --until: closed:<graph id>, merged:<branch>[@<into>], changed:<path>, gone:<path>, claim, session, at:<time> or captured; or none"},
		{name: "none beside a bad until", args: []string{"ack", "state", "--add", "x", "--until", "none", "--until", "soon"}, want: "--until takes"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := h.logBytes()

			got := h.run("", tc.args...)
			if got.code != 1 || !strings.Contains(got.err, tc.want) {
				t.Errorf("the call should be refused naming why, got %d %q", got.code, got.err)
			}

			if !h.logUnchanged(before) {
				t.Error("a refused ack should write nothing")
			}
		})
	}
}

func TestUntilNoneAddsALineNoConditionEnds(t *testing.T) {
	cases := []struct {
		name  string
		until []string
		shown string
	}{
		{name: "none alone", until: []string{"none"}, shown: ": kept open\n"},
		{name: "none beside a condition", until: []string{"none", "claim"}, shown: ": kept open  until claim\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")

			args := []string{"ack", "state", "--add", "kept open"}
			for _, end := range tc.until {
				args = append(args, "--until", end)
			}

			found := lineAddress.FindStringSubmatch(h.must("", args...))
			if found == nil {
				t.Fatal("an add naming none should be accepted")
			}

			if shown := h.must("", "show", "state") + "\n"; !strings.Contains(shown, "#"+found[1]+tc.shown) {
				t.Errorf("none should keep no condition of its own, got:\n%s", shown)
			}
		})
	}
}

func TestMCPAckRefusesAStateLineWithoutUntil(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	answer := h.mcp(`{"name":"ack","arguments":{"ids":["state"],"add":["over MCP"]}}`)
	if !strings.Contains(answer, `"isError":true`) || !strings.Contains(answer, "needs --until") {
		t.Errorf("MCP ack should refuse a state line naming no end, got %s", answer)
	}
}

func TestMCPAckTakesStateIdsAndUntil(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	answer := h.mcp(`{"name":"ack","arguments":{"ids":["state"],"add":["over MCP"],"until":["claim"]}}`)
	found := lineAddress.FindStringSubmatch(answer)

	if strings.Contains(answer, `"isError":true`) || found == nil {
		t.Fatalf("MCP ack should add a state line with an end, got %s", answer)
	}

	if shown := h.must("", "show", "state"); !strings.Contains(shown, "#"+found[1]+": over MCP  until claim") {
		t.Errorf("the line should carry its end, got:\n%s", shown)
	}

	if shown := h.mcp(`{"name":"show","arguments":{"ids":["state"]}}`); !strings.Contains(shown, `\"state\":{\"lines\":[{\"id\":\"`+found[1]+`\"`) {
		t.Errorf("MCP show should carry the ledger, got %s", shown)
	}

	answer = h.mcp(fmt.Sprintf(`{"name":"ack","arguments":{"ids":["state#%s"],"done":true}}`, found[1]))
	if strings.Contains(answer, `"isError":true`) || !strings.Contains(answer, "done") {
		t.Errorf("MCP ack should tick a state line, got %s", answer)
	}

	if answer := h.mcp(`{"name":"state","arguments":{"body":"x"}}`); !strings.Contains(answer, "unknown tool") && !strings.Contains(answer, `"error"`) {
		t.Errorf("the MCP state tool should be gone, got %s", answer)
	}
}

func TestALineEndsWhenWhatItCitesEnds(t *testing.T) {
	cases := []struct {
		name  string
		until []string
		setup func(h *harness) (string, func())
	}{
		{name: "every memory id it cites retired", setup: func(h *harness) (string, func()) {
			one, other := h.file("Gate reads Bash only today", "x"), h.file("Waiter checked on the runner", "y")

			return "settle " + one + " and " + other, func() { h.must("", "retire", one, other) }
		}},
		{name: "a superseded memory id", setup: func(h *harness) (string, func()) {
			one := h.file("Gate reads Bash only today", "x")

			return "settle " + one, func() { h.must("", "supersede", one, "--because", "moved") }
		}},
		{name: "a ticked todo item", setup: func(h *harness) (string, func()) {
			todo := h.file("Wire the gate into MCP", "body", "--kind", "todo", "--item", "read", "--item", "write")
			item := itemPattern.FindStringSubmatch(h.must("", "show", todo, "--again"))[1]

			return "after " + todo + "#" + item, func() { h.must("", "ack", todo+"#"+item, "--done") }
		}},
		{name: "a closed graph entry", setup: func(h *harness) (string, func()) {
			return "decided in " + closedDecision, nil
		}},
		{name: "a pinned memory entry retired", setup: func(h *harness) (string, func()) {
			one := h.file("Gate reads Bash only today", "x")

			return one, func() { h.must("", "retire", one) }
		}},
		{name: "a declared end reached", until: []string{"--until", "gone:README.md"}, setup: func(h *harness) (string, func()) {
			path := filepath.Join(h.repo, "README.md")

			return "until the readme goes", func() { removeFile(t, path) }
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			withGraph(t, h)
			h.must("", "claim", "main")
			text, end := tc.setup(h)
			id := h.addLine(text, tc.until...)

			// A line whose cited id ended before it was added ends at the
			// first brief; every other is read open once first.
			if end != nil {
				if brief := stateOf(h.must("", "brief")); !strings.Contains(brief, "#"+id+":") {
					t.Fatalf("the line should stand open before its end, got:\n%s", brief)
				}

				end()
			}

			brief := h.must("", "brief")
			if !strings.Contains(brief, "ended since last brief") || !strings.Contains(brief, "  state#"+id+" ") ||
				strings.Contains(stateOf(brief), "#"+id+":") {
				t.Fatalf("the ended line should leave the state section for ended since last brief, got:\n%s", brief)
			}

			if again := h.must("", "brief"); strings.Contains(again, "state#"+id) {
				t.Errorf("an ended line should be named once, got:\n%s", again)
			}

			h.must("", "ack", "state#"+id, "--undo")

			if brief := stateOf(h.must("", "brief")); !strings.Contains(brief, "#"+id+":") {
				t.Errorf("undo should bring the ended line back, got:\n%s", brief)
			}
		})
	}
}

func removeFile(t *testing.T, path string) {
	t.Helper()

	err := os.Remove(path)
	if err != nil {
		t.Fatalf("removing %s: %v", path, err)
	}
}

func TestALineStaysWhileAnyCitedIDIsOpen(t *testing.T) {
	h := newHarness(t)
	withGraph(t, h)
	h.must("", "claim", "main")
	one, other := h.file("Gate reads Bash only today", "x"), h.file("Waiter checked on the runner", "y")
	cited := h.addLine("settle " + one + " and " + other + " per " + activeDecision)
	plain := h.addLine("no id at all")
	pinned := h.addLine(other)

	h.must("", "retire", one)
	h.must("", "supersede", other, "--because", "moved")

	brief := stateOf(h.must("", "brief"))
	if !strings.Contains(brief, "#"+cited+":") || !strings.Contains(brief, "#"+plain+":") {
		t.Errorf("a line citing an open graph entry, and a line citing nothing, should stay, got:\n%s", brief)
	}

	if !strings.Contains(brief, "#"+pinned+":") {
		t.Errorf("a pinned line should follow a superseded target as a linked item does, not end, got:\n%s", brief)
	}
}

func TestAnEndedLineAppearsOnceUnderEndedSinceLastBrief(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	id := h.addLine("ends at noon", "--until", "at:2026-10-25T12:00:00Z")
	if brief := h.must("", "brief"); strings.Contains(brief, "ended since last brief") {
		t.Fatalf("a line before its end should stand open, got:\n%s", brief)
	}

	h.clock.Advance(4 * time.Hour)

	brief := h.must("", "brief")
	if !strings.Contains(brief, "state#"+id+" ends at noon: end reached: at:2026-10-25T12:00:00Z (agentic-memory ack state#"+id+" --undo)") {
		t.Errorf("the ended line should name its end and its undo, got:\n%s", brief)
	}
}

// commitHarness is a seated session whose HEAD moved past its claim by one
// commit.
func commitHarness(t *testing.T) *harness {
	t.Helper()

	h := newHarness(t)
	withGraph(t, h)
	h.must("", "claim", "main")
	h.git.Commit = "9c0ffee1"
	h.git.Log = map[string]string{"15af1a8c..HEAD": "9c0ffee1 fix: the gate reads MCP calls\n"}

	return h
}

func TestHooksAddLandedLines(t *testing.T) {
	cases := []struct {
		name string
		hook func(h *harness)
	}{
		{name: "at a commit call", hook: func(h *harness) { h.bashHook("git commit -m 'fix: the gate reads MCP calls'") }},
		{name: "at a merge call", hook: func(h *harness) { h.bashHook("git merge feature") }},
		{name: "at a cherry-pick call", hook: func(h *harness) { h.bashHook("git cherry-pick 9c0ffee1") }},
		{name: "at Stop, for a commit made elsewhere", hook: func(h *harness) { h.must(h.stop(), "stop") }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := commitHarness(t)
			tc.hook(h)
			tc.hook(h)

			brief := stateOf(h.must("", "brief"))
			if strings.Count(brief, "hook: landed 9c0ffee1 fix: the gate reads MCP calls (0m)") != 1 {
				t.Errorf("the commit should land as one hook line, got:\n%s", brief)
			}
		})
	}

	t.Run("not at a call that lands nothing", func(t *testing.T) {
		h := commitHarness(t)
		h.bashHook("git status")

		if brief := stateOf(h.must("", "brief")); strings.Contains(brief, "landed") {
			t.Errorf("a git call landing nothing should add no line, got:\n%s", brief)
		}
	})
}

func TestAGraphGainAddsACapturedLine(t *testing.T) {
	h := newHarness(t)
	withGraph(t, h)
	h.must("", "claim", "main")
	h.callTool("Bash", map[string]string{"command": "ls"})

	test.WriteFile(t, filepath.Join(h.repo, ".sdd", "graph", "2026", "10", "25-120000-d-tac-one.md"),
		"---\ntype: decision\ntitle: Seat state as a ledger\n---\n")
	h.callTool("Bash", map[string]string{"command": "sdd new"})

	if brief := stateOf(h.must("", "brief")); !strings.Contains(brief, "hook: captured 20261025-120000-d-tac-one Seat state as a ledger") {
		t.Errorf("the graph's gain should land as a captured line with its title, got:\n%s", brief)
	}

	h.callTool("Bash", map[string]string{"command": "sdd show"})
	h.must(h.stop(), "stop")

	if brief := stateOf(h.must("", "brief")); strings.Count(brief, "hook: captured 20261025-120000-d-tac-one") != 1 {
		t.Errorf("a graph entry should land as one captured line however often the hooks run, got:\n%s", brief)
	}
}

func TestAHookLineStaysUntilItsHookTicksIt(t *testing.T) {
	h := newHarness(t)
	withGraph(t, h)

	path := filepath.Join(h.repo, ".sdd", "graph", "2026", "10", "25-162357-d-tac-427.md")
	removeFile(t, path)
	h.must("", "claim", "main")
	h.callTool("Bash", map[string]string{"command": "ls"})
	test.WriteFile(t, path, "---\ntype: decision\n---\n")
	h.callTool("Bash", map[string]string{"command": "sdd new"})

	brief := h.must("", "brief")
	if !strings.Contains(stateOf(brief), "hook: captured "+closedDecision) || strings.Contains(brief, "--undo)") {
		t.Errorf("a captured line should stay listed though its graph entry is closed, got:\n%s", brief)
	}
}

func TestStopAddsOneDirtyAndOneAskedLine(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.said(h.promptSaying("fix the gate for MCP reads"), "prompt")
	h.callTool("Edit", map[string]string{"file_path": filepath.Join(h.repo, "internal", "gate", "gate.go")})
	h.callTool("Write", map[string]string{"file_path": filepath.Join(h.repo, "README.md")})
	h.git.SetDirty("internal/gate/gate.go", "README.md")
	h.must(h.stop(), "stop")

	brief := stateOf(h.must("", "brief"))
	if !strings.Contains(brief, "hook: dirty internal/gate/gate.go, README.md") ||
		!strings.Contains(brief, "hook: asked fix the gate for MCP reads") {
		t.Fatalf("Stop should add the dirty files and the last prompt, got:\n%s", brief)
	}

	h.said(h.promptSaying("now the docs"), "prompt")
	h.git.SetDirty("README.md")
	h.must(h.stop(), "stop")
	agent := h.addLine("added after the hook lines")

	brief = stateOf(h.must("", "brief"))
	if strings.Index(brief, "#"+agent+": ") > strings.Index(brief, "hook: dirty") {
		t.Errorf("agent lines should open the section, ahead of the hook lines, got:\n%s", brief)
	}

	if strings.Count(brief, "hook: dirty") != 1 || !strings.Contains(brief, "hook: dirty README.md") ||
		strings.Count(brief, "hook: asked") != 1 || !strings.Contains(brief, "hook: asked now the docs") {
		t.Errorf("a later Stop should keep one dirty and one asked line, current, got:\n%s", brief)
	}

	h.git.SetDirty()
	h.must(h.stop(), "stop")

	if brief := stateOf(h.must("", "brief")); strings.Contains(brief, "dirty") {
		t.Errorf("the dirty line should end once every path is committed or reverted, got:\n%s", brief)
	}
}

func TestAnUnchangedStopWritesNothing(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.said(h.promptSaying("fix the gate"), "prompt")
	h.callTool("Write", map[string]string{"file_path": filepath.Join(h.repo, "README.md")})
	h.git.SetDirty("README.md")
	h.must(h.stop(), "stop")

	before := h.logBytes()
	h.must(h.stop(), "stop")

	if !h.logUnchanged(before) {
		t.Error("a Stop finding the hook lines current should write nothing")
	}
}

func TestTheAskedLineNeverCarriesACredential(t *testing.T) {
	credential := test.SecretFixture(t, "positive/github-pat.txt")
	h := newHarness(t)
	h.must("", "claim", "main")
	h.said(h.promptSaying("fix the gate for MCP reads"), "prompt")
	h.said(h.promptSaying("deploy with this\n"+credential), "prompt")
	h.must(h.stop(), "stop")

	brief := h.must("", "brief")
	if strings.Contains(brief, credential) || !strings.Contains(brief, "hook: asked fix the gate for MCP reads") {
		t.Errorf("a prompt holding a credential should never reach the asked line, got:\n%s", brief)
	}
}

func TestTheAskedLineSkipsClaudeCodesOwnTurns(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.said(h.promptSaying("fix the gate for MCP reads"), "prompt")
	h.said(h.promptSaying("<task-notification>\n<summary>done</summary>\n</task-notification>"), "prompt")
	h.said(h.promptSaying("<agent-message from=\"a1b2c3\">\n[Subagent hand-back] all done\n</agent-message>"), "prompt")
	h.said(h.promptSaying("<cross-session-message from=\"uds:/tmp/s.sock\">\nthe field names\n</cross-session-message>"), "prompt")
	h.must(h.stop(), "stop")

	if brief := h.must("", "brief"); !strings.Contains(brief, "hook: asked fix the gate for MCP reads") {
		t.Errorf("a turn Claude Code sent should not stand as the last prompt, got:\n%s", brief)
	}
}

func TestTheBriefFoldsHookLinesPastTheNewestTwelve(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	var history []string
	for index := range 15 {
		history = append([]string{fmt.Sprintf("c%07d commit number %d", index, index)}, history...)
	}

	h.git.Commit = "c0000014"
	h.git.Log = map[string]string{"15af1a8c..HEAD": strings.Join(history, "\n") + "\n"}
	h.must(h.stop(), "stop")

	brief := stateOf(h.must("", "brief"))
	if strings.Count(brief, "hook: landed") != 12 || !strings.Contains(brief, "commit number 14") ||
		strings.Contains(brief, "commit number 2 ") || !strings.Contains(brief, "  (3 more: agentic-memory show state --with-history)") {
		t.Errorf("the brief should list the newest 12 hook lines and fold the rest, got:\n%s", brief)
	}

	if shown := h.must("", "show", "state", "--with-history"); strings.Count(shown, "hook: landed") != 15 {
		t.Errorf("show state --with-history should list every hook line, got:\n%s", shown)
	}
}

func TestALandedMatchNestsUnderItsLine(t *testing.T) {
	h := newLandedHarness(t)
	h.must("", "claim", "main")
	id := h.addLine("rework the waiter loop")

	h.land(test.Commit{SHA: "c1c1c1c1c1c1c1c1", Message: "Rework the waiter loop"})
	h.runPrompt()

	if notice := h.runPrompt(); strings.Contains(notice, "landed commits may settle") {
		t.Errorf("a match to a state line is no question to settle, got:\n%s", notice)
	}

	h.git.Commit = "c1c1c1c1"
	h.git.Log = map[string]string{"15af1a8c..HEAD": "c1c1c1c1 Rework the waiter loop\n"}
	h.must(h.stop(), "stop")

	brief := stateOf(h.must("", "brief"))
	if !strings.Contains(brief, "#"+id+": rework the waiter loop\n    landed c1c1c1c1 Rework the waiter loop") ||
		strings.Contains(brief, "hook: landed c1c1c1c1") {
		t.Errorf("a matched commit should nest under the line it may settle, and only there, got:\n%s", brief)
	}

	if slot := h.slots("landed")["landed"]; !strings.Contains(slot, `"state:`) {
		t.Errorf("the match should be kept while its line stands open, got %s", slot)
	}

	h.must("", "ack", "state#"+id, "--done")

	if brief := stateOf(h.must("", "brief")); strings.Contains(brief, "    landed c1c1c1c1") || !strings.Contains(brief, "hook: landed c1c1c1c1") {
		t.Errorf("the nested commit should end with its line, its hook line standing alone again, got:\n%s", brief)
	}

	h.land(test.Commit{SHA: "d2d2d2d2d2d2d2d2", Message: "Unrelated docs"})
	h.runPrompt()

	if slot := h.slots("landed")["landed"]; strings.Contains(slot, `"state:`) {
		t.Errorf("a landed round should drop the match of a line that ended, got %s", slot)
	}
}

func TestNoLineIsRefusedForItsLengthOrCount(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	long := strings.TrimSpace(strings.Repeat("the gate reads every MCP call and the docs follow ", 20))
	h.addLine(long)

	for index := range 11 {
		out := h.must("", "ack", "state", "--add", fmt.Sprintf("line %d", index), "--until", "none")
		if strings.Contains(out, "ledger holds") {
			t.Fatalf("twelve open lines should draw no note, got %q", out)
		}
	}

	out := h.must("", "ack", "state", "--add", "the thirteenth", "--until", "none")
	if !strings.Contains(out, "ledger holds 13 lines; drop the settled ones") {
		t.Errorf("past twelve open agent lines the reply should say so, got %q", out)
	}

	if brief := h.must("", "brief"); !strings.Contains(brief, ": "+long+"\n") {
		t.Errorf("the brief should print an agent line whole, got:\n%s", brief)
	}
}

func TestAnEmptyLedgerReadsEmpty(t *testing.T) {
	h := newHarness(t)

	if out := h.must("", "claim", "main"); !strings.Contains(out, "\nstate: empty\n") {
		t.Errorf("a seat with no ledger should read as empty at claim, got:\n%s", out)
	}
}

func TestTheStateCommandRefusesNamingAckState(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	before := h.logBytes()

	got := h.run("where work stands", "state")
	if got.code != 1 || !strings.Contains(got.err, "agentic-memory ack state --add '<line>' --until <end>") {
		t.Errorf("state should refuse, naming the ack state form, got %d %q", got.code, got.err)
	}

	if !h.logUnchanged(before) {
		t.Error("a refused state should write nothing")
	}
}

func TestBriefJSONCarriesTheLedger(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	id := h.addLine("gate fix halfway", "--until", "claim")

	var brief struct {
		State struct {
			Lines []struct {
				ID    string   `json:"id"`
				Text  string   `json:"text"`
				State string   `json:"state"`
				Until []string `json:"until"`
			} `json:"lines"`
		} `json:"state"`
	}

	err := json.Unmarshal([]byte(h.must("", "brief", "--json")), &brief)
	if err != nil || len(brief.State.Lines) != 1 || brief.State.Lines[0].ID != id ||
		brief.State.Lines[0].Text != "gate fix halfway" || brief.State.Lines[0].State != "open" ||
		len(brief.State.Lines[0].Until) != 1 {
		t.Errorf("brief --json should carry the ledger's lines, got %+v, %v", brief.State, err)
	}
}

// nudged runs prompts past the compaction threshold until the nudge fires.
func nudged(t *testing.T, h *harness) {
	t.Helper()

	h.sensor(70)

	if !strings.Contains(h.said(h.promptSaying("go on"), "prompt"), "Context near compaction. Add or tick state lines where work moved: "+
		"agentic-memory ack state --add '<line>' --until <end> | ack state#<item> --done") {
		t.Fatal("the nudge should fire past 65% of the window, worded for the ledger")
	}
}

func TestOnlyTheAgentsLedgerChangeAnswersTheNudge(t *testing.T) {
	cases := []struct {
		name     string
		change   func(h *harness)
		answered bool
	}{
		{name: "an added line", change: func(h *harness) { h.addLine("where work stands") }, answered: true},
		{name: "a ticked line", answered: true, change: func(h *harness) {
			h.must("", "ack", "state#"+h.addLine("x"), "--done")
		}},
		{name: "a hook line", change: func(h *harness) {
			h.git.Commit = "9c0ffee1"
			h.git.Log = map[string]string{"15af1a8c..HEAD": "9c0ffee1 fix: something\n"}
			h.must(h.stop(), "stop")
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")
			h.clock.Advance(time.Minute)
			nudged(t, h)
			h.clock.Advance(time.Minute)
			tc.change(h)

			again := strings.Contains(h.said(h.promptSaying("go on"), "prompt"), "Context near compaction")
			if again == tc.answered {
				t.Errorf("the change should answer the nudge exactly where the agent made it, nudged again: %v", again)
			}
		})
	}
}

func TestShippedWorkAsksNothing(t *testing.T) {
	h := commitHarness(t)

	for range 6 {
		h.callTool("Edit", map[string]string{"file_path": filepath.Join(h.repo, "README.md")})
	}

	got := h.run(h.stop(), "stop")
	prompt := h.said(h.promptSaying("go on"), "prompt")

	if strings.Contains(got.out+prompt, "Work shipped") || got.out != "" {
		t.Errorf("shipped work should ask for nothing, the hook lines record it, got %q %q", got.out, prompt)
	}
}

func TestAnAgentWrittenSlotWaitsAsStateOld(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	writeOldState(t, h, "gate fix halfway; next: docs", false)

	want := "  #old (0m old, branch main): gate fix halfway; next: docs\n  " +
		"state is a ledger now: split this into lines (agentic-memory ack state --add '<line>' --until <end> ...), then agentic-memory ack state#old --done"

	moments := []struct {
		name string
		run  func() string
	}{
		{name: "at claim", run: func() string { return h.must("", "claim", "main") }},
		{name: "at compaction", run: func() string { return h.said(h.startHook("compact", ""), "session-start") }},
		{name: "at resume", run: func() string { return h.said(h.startHook("resume", ""), "session-start") }},
	}

	for _, moment := range moments {
		t.Run(moment.name, func(t *testing.T) {
			if out := moment.run(); !strings.Contains(out, want) {
				t.Errorf("the old slot should wait as state#old with the request, got:\n%s", out)
			}
		})
	}

	h.addLine("gate fix halfway")

	if out := h.must("", "claim", "main"); !strings.Contains(out, want) {
		t.Errorf("a new line should not settle state#old, got:\n%s", out)
	}

	for _, settle := range []string{"--done", "--remove"} {
		h.must("", "ack", "state#old", settle)

		if out := h.said("", "claim", "main") + h.must(h.startHook("resume", ""), "session-start"); strings.Contains(out, "#old") {
			t.Errorf("state#old settled with %s should no longer show, got:\n%s", settle, out)
		}

		h.must("", "ack", "state#old", "--undo")

		if out := h.must("", "claim", "main"); !strings.Contains(out, "): gate fix halfway; next: docs\n  state is a ledger now: split this") {
			t.Errorf("an undone %s should bring state#old back, got:\n%s", settle, out)
		}
	}
}

func TestADraftOnlySlotMigratesSilently(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	writeOldState(t, h, "files written: README.md", true)

	if out := h.must("", "claim", "main"); strings.Contains(out, "#old") || strings.Contains(out, "README.md") {
		t.Errorf("a hook's draft should be dropped unasked, got:\n%s", out)
	}
}

// writeOldState appends a state row as a release before the ledger wrote it.
func writeOldState(t *testing.T, h *harness, body string, draft bool) {
	t.Helper()

	payload, _ := json.Marshal(map[string]any{"body": body, "branch": "main", "commit": "15af1a8c", "draft": draft})

	log, err := memorylog.Open(h.store(), filepath.Join(h.store(), "log"))
	if err != nil {
		t.Fatalf("opening the log: %v", err)
	}

	err = log.Append(context.Background(), memorylog.Event{
		Kind: memorylog.KindState, Time: h.clock.Now(), Seat: seatID(t, h.store(), "main"), Payload: payload,
	})
	if err != nil {
		t.Fatalf("appending a state row: %v", err)
	}
}

func TestAFormatSevenStoreMovesToEightWithItsSlotAsStateOld(t *testing.T) {
	h := newHarness(t)
	test.FixtureTree(t, "memory/format-7", h.store())

	main := h.must("", "claim", "main")
	if !strings.Contains(main, "#old (") || !strings.Contains(main, "gate fix halfway; next: wire the MCP reads, then docs") {
		t.Errorf("the seat's own slot should read as state#old, got:\n%s", main)
	}

	h.must("", "release")

	drive := h.must("", "claim", "drive")
	if strings.Contains(drive, "#old") || strings.Contains(drive, "rework the drive sync") {
		t.Errorf("a hook's draft should be dropped, got:\n%s", drive)
	}

	if written, _ := os.ReadFile(filepath.Join(h.store(), "format.json")); !strings.Contains(string(written), `"format":11`) {
		t.Errorf("a format-7 store should move through format 8 to the current one: %s", written)
	}

	if test.Exists(t, filepath.Join(h.store(), "backup")) {
		t.Error("a step fencing older binaries off should change nothing but the format")
	}

	if mismatches := test.LedgerMismatches(t, h.store()); len(mismatches) > 0 {
		t.Errorf("the size ledger should match the files after the move: %s", strings.Join(mismatches, "; "))
	}
}
