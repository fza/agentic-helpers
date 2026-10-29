package memory_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/sddcaps"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const olderDecision = "20261024-100000-d-tac-old"

func TestUntilTakesEachForm(t *testing.T) {
	h := newHarness(t)
	withGraph(t, h)
	h.must("", "claim", "main")

	for _, until := range []string{
		"closed:" + closedDecision, "merged:feature", "merged:feature@release", "changed:README.md",
		"gone:internal/gate/gate.go", "claim", "session", "at:2026-11-01", "at:2026-11-01T10:00:00Z", "captured",
	} {
		t.Run(until, func(t *testing.T) {
			out := h.must("body", "new", "--title", "Gate todo ending on a condition", "--kind", "todo", "--until", until)
			if !strings.Contains(out, "until: "+until) {
				t.Errorf("the filed line should carry the declared end:\n%s", out)
			}
		})
	}

	for _, malformed := range []string{
		"done", "closed:d-tac-427", "merged:", "merged:feature@", "at:tomorrow", "changed:", "gone:../outside", "claim:x",
		"session:x",
	} {
		t.Run("refuses "+malformed, func(t *testing.T) {
			got := h.run("body", "new", "--title", "Gate todo ending on nothing", "--until", malformed)
			if got.code != 1 || !strings.Contains(got.err, "--until") {
				t.Errorf("a malformed condition should be refused naming --until, got %d %q", got.code, got.err)
			}
		})
	}

	if got := h.run("body", "new", "--title", "Gate todo ending on nothing", "--until", "done"); !strings.Contains(got.err,
		"--until takes closed:<graph id>, merged:<branch>[@<into>], changed:<path>, gone:<path>, claim, session, at:<time> or captured") {
		t.Errorf("a malformed condition should name the forms, got %q", got.err)
	}

	got := h.run("body", "new", "--title", "Owner rule that never ends", "--kind", "rule", "--from", "user", "--until", "claim")
	if got.code != 1 || !strings.Contains(got.err, "a rule ends only when someone retires it") {
		t.Errorf("--until on a rule should be refused, got %d %q", got.code, got.err)
	}

	answer := h.mcp(`{"name":"new","arguments":{"title":"Gate todo over MCP","body":"b","kind":"todo","until":["claim","captured"]}}`)
	if strings.Contains(answer, `"isError":true`) || !strings.Contains(answer, "until: claim, captured") {
		t.Errorf("the MCP key until should declare the ends: %s", answer)
	}

	answer = h.mcp(`{"name":"new","arguments":{"title":"Gate todo over MCP","body":"b","until":["done"]}}`)
	if !strings.Contains(answer, `"isError":true`) || !strings.Contains(answer, "--until takes") {
		t.Errorf("a malformed condition over MCP should be refused: %s", answer)
	}

	answer = h.mcp(`{"name":"new","arguments":{"title":"Owner rule over MCP","body":"b","kind":"rule","from":"user","until":["claim"]}}`)
	if !strings.Contains(answer, `"isError":true`) || !strings.Contains(answer, "a rule ends only") {
		t.Errorf("--until on a rule over MCP should be refused: %s", answer)
	}
}

func TestClosedNeedsAnSddWithExport(t *testing.T) {
	h := newHarness(t)
	withGraph(t, h)
	h.sdd = &sddcaps.Probe{Runner: &test.Runner{Stock: true, Unreleased: true}}
	h.must("", "claim", "main")

	got := h.run("body", "new", "--title", "Gate todo ending on a closure", "--until", "closed:"+closedDecision)
	if got.code != 1 || !strings.Contains(got.err, "closed: needs sdd export, which this sdd lacks") {
		t.Errorf("closed: should be refused where sdd lacks export, got %d %q", got.code, got.err)
	}

	h.must("body", "new", "--title", "Gate todo ending on a claim", "--until", "claim")
}

// endCase files a todo ending on until, makes its end reached or not, and
// briefs the seat that checks it.
type endCase struct {
	name  string
	until string
	reach func(h *harness, id string) *harness
	// prior runs before the seat is claimed and the todo filed.
	prior   func(h *harness)
	retired bool
}

func TestTheEndCheck(t *testing.T) {
	same := func(h *harness, _ string) *harness { return h }

	cases := []endCase{
		{name: "a closed graph entry", until: "closed:" + closedDecision, reach: same, retired: true},
		{name: "an active graph entry", until: "closed:" + activeDecision, reach: same},
		{name: "a merged branch", until: "merged:feature", retired: true, reach: func(h *harness, id string) *harness {
			h.git.Branches = map[string][]string{"feature": {"main"}}

			return h
		}},
		{name: "an unmerged branch", until: "merged:feature", reach: func(h *harness, id string) *harness {
			h.git.Branches = map[string][]string{"feature": {"release"}}

			return h
		}},
		{name: "a branch merged into its named target", until: "merged:feature@release", retired: true, reach: func(h *harness, id string) *harness {
			h.git.Branches = map[string][]string{"feature": {"release"}}

			return h
		}},
		{name: "a branch merged into origin's default", until: "merged:feature", retired: true, reach: func(h *harness, id string) *harness {
			h.git.OriginHead = "refs/remotes/origin/trunk"
			h.git.Branches = map[string][]string{"feature": {"refs/remotes/origin/trunk"}}

			return h
		}},
		{name: "a missing branch", until: "merged:feature", reach: same},
		{name: "a changed file", until: "changed:README.md", retired: true, reach: func(h *harness, id string) *harness {
			test.WriteFile(h.t, filepath.Join(h.repo, "README.md"), "# app, changed\n")

			return h
		}},
		{name: "an unchanged file", until: "changed:README.md", reach: same},
		{name: "a gone file", until: "gone:internal/gate/gate.go", retired: true, reach: func(h *harness, id string) *harness {
			err := os.Remove(filepath.Join(h.repo, "internal", "gate", "gate.go"))
			if err != nil {
				h.t.Fatal(err)
			}

			return h
		}},
		{name: "a file still there", until: "gone:internal/gate/gate.go", reach: same},
		{name: "a claim by another session", until: "claim", retired: true, reach: func(h *harness, id string) *harness {
			h.must("", "release")

			other := newHarnessSharing(h)
			other.clock.Advance(time.Minute)

			return other
		}},
		{name: "a claim by another session before the filing", until: "claim", reach: same, prior: func(h *harness) {
			other := newHarnessSharing(h)
			other.must("", "claim", "main")
			other.must("", "release")
			h.clock.Advance(time.Minute)
		}},
		{name: "a claim by the same session", until: "claim", reach: func(h *harness, _ string) *harness {
			h.must("", "release")
			h.clock.Advance(time.Minute)

			return h
		}},
		{name: "a time passed", until: "at:2026-10-25T10:00:00Z", retired: true, reach: func(h *harness, id string) *harness {
			h.clock.Advance(2 * time.Hour)

			return h
		}},
		{name: "a date begun", until: "at:2026-10-25", retired: true, reach: same},
		{name: "a time to come", until: "at:2026-10-26T10:00:00Z", reach: same},
		{name: "a link to a later graph entry", until: "captured", retired: true, reach: func(h *harness, id string) *harness {
			h.must("", "link", id, closedDecision)

			return h
		}},
		{name: "a link to an earlier graph entry", until: "captured", reach: func(h *harness, id string) *harness {
			h.must("", "link", id, olderDecision)

			return h
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			withGraph(t, h)
			test.WriteFile(t, filepath.Join(h.repo, ".sdd", "graph", "2026", "10", "24-100000-d-tac-old.md"), "---\n---\n")

			if tc.prior != nil {
				tc.prior(h)
			}

			h.must("", "claim", "main")
			id := h.file("Gate todo ending on a condition", "body", "--kind", "todo", "--until", tc.until)

			checker := tc.reach(h, id)
			checker.must("", "claim", "main")

			out := checker.must("", "search", "--term", "ending on a condition", "--status", "retired", "--status", "live")
			retired := strings.Contains(out, "end reached: "+tc.until)

			if retired != tc.retired {
				t.Errorf("the end check should retire exactly the entries whose end was reached:\n%s", out)
			}
		})
	}
}

func TestClosedReadsAConnectedRepo(t *testing.T) {
	for _, tc := range []struct {
		name    string
		until   string
		watched string
		retired bool
	}{
		{name: "qualified and closed", until: "closed:" + fdbox + ":" + foreignClosed, watched: fdbox + ":" + foreignClosed, retired: true},
		{name: "bare, one connected repo holding it", until: "closed:" + foreignClosed, watched: fdbox + ":" + foreignClosed, retired: true},
		{name: "qualified and active", until: "closed:" + fdbox + ":" + foreignActive, watched: fdbox + ":" + foreignActive},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			withConnectedRepos(t, h)
			h.must("", "claim", "main")

			out := h.must("body", "new", "--title", "Gate todo ending on fdbox", "--kind", "todo", "--until", tc.until)
			if !strings.Contains(out, "until: closed:"+tc.watched) {
				t.Errorf("a connected repo's entry should be watched qualified:\n%s", out)
			}

			h.must("", "brief")

			listed := h.must("", "search", "--status", "retired")
			if strings.Contains(listed, "end reached: closed:"+tc.watched) != tc.retired {
				t.Errorf("a connected repo's status should end the entry exactly where it closed:\n%s", listed)
			}
		})
	}
}

func TestAReachedEndIsListedOnce(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	id := h.file("Gate todo ending at noon", "body", "--kind", "todo", "--until", "at:2026-10-25T12:00:00Z")
	h.must("", "brief")

	h.clock.Advance(4 * time.Hour)

	first := h.must("", "brief")
	if !strings.Contains(first, "ended since last brief (revive to undo):\n  "+id+" Gate todo ending at noon: at:2026-10-25T12:00:00Z") {
		t.Errorf("a brief over a reached end should list the entry it retired with its condition:\n%s", first)
	}

	if shown := h.must("", "show", id); !strings.Contains(shown, "retired: end reached: at:2026-10-25T12:00:00Z by main") {
		t.Errorf("the retire should carry the condition as its reason, by the checking seat:\n%s", shown)
	}

	if second := h.must("", "brief"); strings.Contains(second, "ended since last brief") {
		t.Errorf("a reached end should be listed once:\n%s", second)
	}
}

func TestAReviveRefusesAnEndThatStillHolds(t *testing.T) {
	const gone = "internal/gate/gate.go"

	change := func(h *harness) { test.WriteFile(h.t, filepath.Join(h.repo, "README.md"), "# app, changed\n") }
	changeBack := func(h *harness) { test.WriteFile(h.t, filepath.Join(h.repo, "README.md"), "# app\n") }
	remove := func(h *harness) {
		err := os.Remove(filepath.Join(h.repo, filepath.FromSlash(gone)))
		if err != nil {
			h.t.Fatal(err)
		}
	}
	restore := func(h *harness) {
		test.WriteFile(h.t, filepath.Join(h.repo, filepath.FromSlash(gone)), "package gate\n")
	}
	brief := func(h *harness, _ string) { h.must("", "brief") }

	for _, tc := range []struct {
		name  string
		until string
		reach func(h *harness)
		// retire retires the entry once its end is reached.
		retire func(h *harness, id string)
		undo   func(h *harness)
		holds  bool
	}{
		{name: "a file still changed", until: "changed:README.md", reach: change, retire: brief, holds: true},
		{name: "a file changed back", until: "changed:README.md", reach: change, retire: brief, undo: changeBack},
		{name: "a file still gone", until: "gone:" + gone, reach: remove, retire: brief, holds: true},
		{name: "a file back again", until: "gone:" + gone, reach: remove, retire: brief, undo: restore},
		{name: "a time passed", until: "at:2026-10-25T10:00:00Z", retire: brief, holds: true, reach: func(h *harness) {
			h.clock.Advance(2 * time.Hour)
		}},
		{name: "a retire by hand naming the end", until: "gone:" + gone, reach: remove, retire: func(h *harness, id string) {
			h.must("", "retire", id, "--because", "gone:"+gone)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")
			id := h.file("Gate todo ending on a condition", "body", "--kind", "todo", "--until", tc.until)

			tc.reach(h)
			tc.retire(h, id)

			if tc.undo != nil {
				tc.undo(h)
			}

			got := h.run("", "revive", id)

			if tc.holds {
				if got.code != 1 || !strings.Contains(got.err, id+": its end "+tc.until+" still holds; file it again") {
					t.Errorf("a revive should be refused while the end that retired the entry still holds, got %d %q", got.code, got.err)
				}

				return
			}

			if got.code != 0 {
				t.Fatalf("a revive should restore an entry no reached end still holds retired, got %d %q", got.code, got.err)
			}

			if tc.undo == nil {
				return
			}

			h.must("", "brief")

			if live := h.must("", "search", "--status", "live"); !strings.Contains(live, id) {
				t.Errorf("the revived entry should survive the next brief:\n%s", live)
			}
		})
	}
}

func TestARuleNeverEndsByACondition(t *testing.T) {
	h := newHarness(t)
	withGraph(t, h)
	h.must("", "claim", "main")
	id := h.file("Owner instruction until noon", "body", "--kind", "todo", "--from", "user", "--until", "at:2026-10-25T12:00:00Z")
	h.must("", "rekind", id, "rule")
	h.file("Owner instruction citing a closed decision", "body", "--kind", "rule", "--from", "user", "--link", closedDecision)

	h.clock.Advance(4 * time.Hour)

	if brief := h.must("", "brief"); strings.Contains(brief, "ended since last brief") || strings.Contains(brief, "retire?") {
		t.Errorf("a rule should end only when someone retires it:\n%s", brief)
	}
}

func TestOnlyTheCheckingSeatsShelfIsRetired(t *testing.T) {
	h, other := twoSeats(t)

	theirs := other.file("Idea todo ending at noon", "body", "--kind", "todo", "--until", "at:2026-10-25T12:00:00Z",
		"--for", "main")

	h.clock.Advance(4 * time.Hour)
	h.must("", "brief")

	if out := h.must("", "search", "--status", "live"); !strings.Contains(out, theirs) {
		t.Errorf("a seat's brief should never retire an entry of another shelf:\n%s", out)
	}

	other.must("", "brief")

	if out := other.must("", "search", "--status", "retired"); !strings.Contains(out, theirs) {
		t.Errorf("the filing shelf's brief should retire it:\n%s", out)
	}
}

func TestRetireQuestionListsInferredEnds(t *testing.T) {
	h := newHarness(t)
	withGraph(t, h)
	h.must("", "claim", "main")

	linked := h.file("Gate finding citing a closed decision", "body", "--link", closedDecision)
	test.WriteFile(t, filepath.Join(h.repo, "notes.txt"), "notes\n")
	anchored := h.file("Gate finding about notes.txt", "body")
	branch := h.file("Gate todo for a branch", "body", "--kind", "todo", "--until", "merged:feature")
	kept := h.file("Gate finding citing an active decision", "body", "--link", activeDecision)

	err := os.Remove(filepath.Join(h.repo, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}

	brief := h.must("", "brief")

	for id, why := range map[string]string{linked: "every link ended", anchored: "every anchor gone", branch: "branch feature is gone"} {
		if !strings.HasSuffix(briefLine(brief, id), "retire? "+why) {
			t.Errorf("an entry whose end is inferred should carry retire? with why on its own line:\n%s", brief)
		}
	}

	if strings.Contains(briefLine(brief, kept), "retire?") {
		t.Errorf("an entry whose link is live should not be flagged:\n%s", brief)
	}

	if strings.Contains(brief, "\nretire?\n") {
		t.Errorf("the retire? section should not repeat entries printed above:\n%s", brief)
	}

	if out := h.must("", "search", "--status", "retired"); out != "no entry matches\n" {
		t.Errorf("retire? should retire nothing:\n%s", out)
	}
}

func TestLinkingTheCaptureEndsTheTodo(t *testing.T) {
	h := newHarness(t)
	withGraph(t, h)
	h.must("", "claim", "main")

	captured := h.file("Owner decision waiting for capture", "body", "--kind", "todo", "--until", "captured")

	if out := h.must("", "link", captured, closedDecision); !strings.Contains(out, "retired "+captured+": end reached: captured") {
		t.Errorf("linking a graph entry filed after the todo should retire it:\n%s", out)
	}

	waiting := h.file("Owner decision still waiting", "body", "--kind", "todo", "--until", "captured")
	test.WriteFile(t, filepath.Join(h.repo, ".sdd", "graph", "2026", "10", "24-100000-d-tac-old.md"), "---\n---\n")

	if out := h.must("", "link", waiting, olderDecision); strings.Contains(out, "retired") {
		t.Errorf("a graph entry filed before the todo should not end it:\n%s", out)
	}
}

// toolContext is the note a PostToolUse answer hands the agent.
func toolContext(out string) string {
	var answer struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}

	_ = json.Unmarshal([]byte(out), &answer)

	return answer.HookSpecificOutput.AdditionalContext
}

func (h *harness) toolCall() string {
	data, _ := json.Marshal(map[string]any{
		"session_id": h.session, "tool_name": "Bash", "tool_input": map[string]string{"command": "sdd new"},
	})

	return string(data)
}

func TestTheCaptureNotice(t *testing.T) {
	h := newHarness(t)
	withGraph(t, h)
	h.must("", "claim", "main")

	if out := h.must(h.toolCall(), "tool"); strings.Contains(out, "graph gained") {
		t.Errorf("the first look should only mark the graph:\n%s", out)
	}

	test.WriteFile(t, filepath.Join(h.repo, ".sdd", "graph", "2026", "10", "25-120000-d-tac-one.md"), "---\n---\n")

	if out := h.must(h.toolCall(), "tool"); strings.Contains(out, "graph gained") {
		t.Errorf("a seat holding no entry waiting for a capture should hear nothing:\n%s", out)
	}

	waiting := h.file("Owner decision waiting for capture", "body", "--kind", "todo", "--until", "captured")
	h.must(h.toolCall(), "tool")

	test.WriteFile(t, filepath.Join(h.repo, ".sdd", "graph", "2026", "10", "25-130000-d-tac-two.md"), "---\n---\n")

	out := toolContext(h.must(h.toolCall(), "tool"))
	if !strings.Contains(out, "agentic-memory: graph gained 20261025-130000-d-tac-two; open todos waiting for a capture: "+
		waiting+" Owner decision waiting for capture; link the one it settles: agentic-memory link <todo> <graph id>") {
		t.Errorf("a graph gain while a todo waits for its capture should be named:\n%s", out)
	}

	if again := h.must(h.toolCall(), "tool"); strings.Contains(again, "graph gained") {
		t.Errorf("the notice should fire once per gain:\n%s", again)
	}

	test.WriteFile(t, filepath.Join(h.repo, ".sdd", "graph", "2026", "10", "25-140000-d-tac-thr.md"), "---\n---\n")

	if next := toolContext(h.must(h.toolCall(), "tool")); !strings.Contains(next, "graph gained 20261025-140000-d-tac-thr;") {
		t.Errorf("a later gain should name only the entries gained since the last look:\n%s", next)
	}

	fresh := *h
	fresh.session = "a0000000-0000-4000-8000-000000000009"

	if first := fresh.must(fresh.toolCall(), "tool"); strings.Contains(first, "graph gained") {
		t.Errorf("a session's first look should only mark the graph, whatever waits:\n%s", first)
	}
}

func TestRetireQuestionReadsAConnectedRepo(t *testing.T) {
	h := newHarness(t)
	withConnectedRepos(t, h)
	h.must("", "claim", "main")

	ended := h.file("Gate finding citing a closed fdbox decision", "body", "--link", fdbox+":"+foreignClosed)
	endedTodo := h.file("Gate todo waiting on a closed fdbox decision", "body", "--kind", "todo", "--link", fdbox+":"+foreignClosed)
	kept := h.file("Gate finding citing an active fdbox decision", "body", "--link", fdbox+":"+foreignActive)

	brief := h.must("", "brief")

	if !strings.HasSuffix(briefLine(brief, ended), "retire? every link ended") {
		t.Errorf("an entry whose only link is a closed entry of a connected repo should be flagged retire?:\n%s", brief)
	}

	if !strings.HasSuffix(briefLine(brief, endedTodo), "retire? every link ended") {
		t.Errorf("a todo whose only link is a closed entry of a connected repo should be flagged retire?:\n%s", brief)
	}

	if strings.Contains(briefLine(brief, kept), "retire?") {
		t.Errorf("an entry whose connected-repo link is live should not be flagged:\n%s", brief)
	}
}

func TestRetireQuestionFlagsInPlaceAndListsOnlyTheRest(t *testing.T) {
	h := newHarness(t)
	withGraph(t, h)
	test.WriteFile(t, h.settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"brief_note_limit":1}}}}`)
	h.must("", "claim", "main")

	overCap := h.file("Cold finding citing a closed decision", "body", "--link", closedDecision)
	listed := h.file("Hot finding citing a closed decision", "body", "--link", closedDecision)
	h.must("", "show", listed)
	h.must("", "show", listed)

	brief := h.must("", "brief")

	if !strings.HasSuffix(briefLine(brief, listed), "retire? every link ended") {
		t.Errorf("a candidate the notes print should carry retire? on its own line:\n%s", brief)
	}

	_, section, found := strings.Cut(brief, "\nretire?\n")
	if !found {
		t.Fatalf("a candidate past the note limit should be named under retire?:\n%s", brief)
	}

	if strings.Contains(section, listed) {
		t.Errorf("a candidate printed above should not repeat under retire?:\n%s", brief)
	}

	if !strings.HasPrefix(section, "  "+overCap+" Cold finding citing a closed decision  retire? every link ended\n") {
		t.Errorf("a candidate past the note limit should be named under retire? with its title and why alone:\n%s", brief)
	}
}

func TestEveryLinkEndedSkipsOwnPredecessorsAndATodosMemoryLinks(t *testing.T) {
	cases := []struct {
		name      string
		file      func(h *harness) string
		candidate bool
		// quiet is where returns lists no reason for the entry: a predecessor
		// it superseded is no link to answer.
		quiet bool
	}{
		{name: "a todo linking only a retired memory entry", file: func(h *harness) string {
			finding := h.file("Finding the todo acts on", "body")
			h.must("", "retire", finding)

			return h.file("Todo acting on the finding", "body", "--kind", "todo", "--link", finding)
		}},
		{name: "a todo linking the predecessor it superseded", quiet: true, file: func(h *harness) string {
			request := h.file("First take on the request", "body", "--kind", "todo")
			todo := h.file("Second take on the request", "body", "--kind", "todo", "--link", request)
			h.must("", "supersede", request, "--by", todo)

			return todo
		}},
		{name: "a todo with a closed graph link", candidate: true, file: func(h *harness) string {
			return h.file("Todo waiting on a closed decision", "body", "--kind", "todo", "--link", closedDecision)
		}},
		{name: "a todo with a closed graph link and a live memory link", candidate: true, file: func(h *harness) string {
			finding := h.file("Finding still in force", "body")

			return h.file("Todo waiting on a closed decision", "body", "--kind", "todo", "--link", closedDecision,
				"--link", finding)
		}},
		{name: "a todo with a closed graph link and a retired memory link", candidate: true, file: func(h *harness) string {
			finding := h.file("Finding the todo acts on", "body")
			h.must("", "retire", finding)

			return h.file("Todo waiting on a closed decision", "body", "--kind", "todo", "--link", closedDecision,
				"--link", finding)
		}},
		{name: "a trap linking its retired fix todo", candidate: true, file: func(h *harness) string {
			fix := h.file("Todo fixing the gate", "body", "--kind", "todo")
			h.must("", "retire", fix)

			return h.file("Gate trap until the fix lands", "body", "--kind", "trap", "--link", fix)
		}},
		{name: "a note linking only the predecessor it superseded", quiet: true, file: func(h *harness) string {
			old := h.file("First take on the waiter", "body")
			note := h.file("Second take on the waiter", "body", "--link", old)
			h.must("", "supersede", old, "--by", note)

			return note
		}},
		{name: "a note linking its predecessor and a closed decision", file: func(h *harness) string {
			old := h.file("First take on the waiter", "body")
			note := h.file("Second take on the waiter", "body", "--link", old, "--link", closedDecision)
			h.must("", "supersede", old, "--by", note)

			return note
		}},
		{name: "a note linking a settled decision and a closed one", file: func(h *harness) string {
			return h.file("Note citing two decisions", "body", "--link", settledDecision, "--link", closedDecision)
		}},
		{name: "a note linking a superseded decision", candidate: true, file: func(h *harness) string {
			return h.file("Note citing a superseded decision", "body", "--link", supersededDecision)
		}},
		{name: "a note linking a predecessor another entry superseded", candidate: true, file: func(h *harness) string {
			old := h.file("First take on the waiter", "body")
			successor := h.file("Second take on the waiter", "body")
			h.must("", "supersede", old, "--by", successor)

			return h.file("Note citing the first take", "body", "--link", old)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			withEndedGraph(t, h)
			h.must("", "claim", "main")

			id := tc.file(h)

			flagged := strings.HasSuffix(briefLine(h.must("", "brief"), id), "retire? every link ended")
			if flagged != tc.candidate {
				t.Errorf("the brief should flag retire? only where every link that can end the entry ended, flagged: %t", flagged)
			}

			title, _, found := strings.Cut(strings.TrimPrefix(briefLine(h.must("", "brief"), id), id+" "), "  [")
			if !found {
				t.Fatalf("the brief should list the entry")
			}

			reasons, listed := returnsByTitle(t, h, "returns:as-list")[title]

			inferred := slices.ContainsFunc(reasons, func(reason returned) bool {
				return reason.Detail == "end inferred: every link ended"
			})
			if inferred != tc.candidate {
				t.Errorf("returns should infer the end only where every link that can end the entry ended, inferred: %t", inferred)
			}

			if tc.quiet && listed {
				t.Errorf("returns should give no reason for a predecessor the entry superseded, got %+v", reasons)
			}
		})
	}
}

// briefLine is the brief's line for id, its two-space indent cut.
func briefLine(brief string, id string) string {
	for _, text := range strings.Split(brief, "\n") {
		if strings.HasPrefix(text, "  "+id+" ") {
			return strings.TrimPrefix(text, "  ")
		}
	}

	return ""
}
