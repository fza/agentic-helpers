package memory_test

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/cacheprune"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

// landedAt is when the commits of a landed-commit test land: after every
// entry the test files.
var landedAt = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

var itemPattern = regexp.MustCompile(`#([a-z0-9]{3})`)

const baseCommit = "b0b0b0b0b0b0b0b0"

// newLandedHarness is a harness whose rounds run at once, inside the hook
// that starts them, on a checkout whose HEAD is a base commit older than
// every entry.
func newLandedHarness(t *testing.T) *harness {
	t.Helper()

	h := newHarness(t)
	withGraph(t, h)
	h.git.Commits = []test.Commit{{SHA: baseCommit, At: landedAt.Add(-6 * time.Hour), Message: "Initial import"}}
	h.starter = &test.Starter{}
	// The daily cache prune runs agentic-helpers, not this binary.
	h.starter.Run = func(args []string) {
		if slices.Equal(args, []string{cacheprune.Command}) {
			return
		}

		h.must("", args...)
	}

	return h
}

// land puts commits, newest first, on top of HEAD's history; a commit
// naming no parent takes the next one.
func (h *harness) land(commits ...test.Commit) {
	history := append(slices.Clone(commits), h.git.Commits...)

	for index := range commits {
		if history[index].Parents == nil {
			history[index].Parents = []string{history[index+1].SHA}
		}

		if history[index].At.IsZero() {
			history[index].At = landedAt
		}
	}

	h.git.SetCommits(history)
}

func (h *harness) runPrompt() string {
	h.t.Helper()

	return h.said(h.promptOf("go on"), "prompt")
}

func (h *harness) bashHook(command string) string {
	h.t.Helper()

	return toolContext(h.must(bashCall(h.session, command), "tool"))
}

func bashCall(session string, command string) string {
	return fmt.Sprintf(`{"session_id":%q,"tool_name":"Bash","tool_input":{"command":%q}}`, session, command)
}

// landedLine is the landed-commit notice among a hook's lines.
func landedLine(out string) string {
	for _, text := range strings.Split(out, "\n") {
		if strings.HasPrefix(text, "agentic-memory: landed commits may settle live work") {
			return text
		}
	}

	return ""
}

func (h *harness) logReads() [][]string {
	var reads [][]string

	for _, call := range h.git.Calls() {
		if len(call) > 1 && call[0] == "log" && slices.Contains(call, "--reverse") {
			reads = append(reads, call)
		}
	}

	return reads
}

func retireCommand(id string, sha string) string {
	return "agentic-memory retire " + id + " --because 'landed in " + sha[:8] + "'"
}

func TestALandedCommitMatchesByEachTest(t *testing.T) {
	cases := []struct {
		name    string
		file    func(h *harness) string
		commit  test.Commit
		matched bool
		test    string
	}{
		{
			name: "a merged: end's branch merged",
			file: func(h *harness) string {
				return h.file("Rework the waiter loop", "body", "--kind", "todo", "--until", "merged:waiter-loop")
			},
			commit:  test.Commit{SHA: "c1c1c1c1c1c1c1c1", Message: "Merge pull request #7 from acme/waiter-loop"},
			matched: true, test: "branch",
		},
		{
			name: "a branch the entry's text names",
			file: func(h *harness) string {
				return h.file("Rework the waiter loop", "built on autopilot/waiter-loop", "--kind", "todo")
			},
			commit:  test.Commit{SHA: "c2c2c2c2c2c2c2c2", Message: "Merge branch 'autopilot/waiter-loop' into main"},
			matched: true, test: "branch",
		},
		{
			name: "a path the entry is about",
			file: func(h *harness) string {
				return h.file("Rework the waiter loop", "body", "--kind", "todo", "--about", "internal/gate/gate.go")
			},
			commit:  test.Commit{SHA: "c3c3c3c3c3c3c3c3", Message: "Tidy", Paths: []string{"internal/gate/gate.go"}},
			matched: true, test: "path",
		},
		{
			name: "a path a trap is anchored to",
			file: func(h *harness) string {
				return h.file("Gate trap on gate.go", "internal/gate/gate.go breaks on nil", "--kind", "trap")
			},
			commit:  test.Commit{SHA: "c4c4c4c4c4c4c4c4", Message: "Tidy", Paths: []string{"internal/gate/gate.go"}},
			matched: true, test: "path",
		},
		{
			name: "a similar subject",
			file: func(h *harness) string {
				return h.file("Rework waiter polling loop deadline", "waiter polling loop deadline", "--kind", "todo")
			},
			commit:  test.Commit{SHA: "c5c5c5c5c5c5c5c5", Message: "Rework waiter polling loop deadline"},
			matched: true, test: "subject",
		},
		{
			name: "an unrelated commit",
			file: func(h *harness) string {
				return h.file("Rework the waiter loop", "body", "--kind", "todo", "--about", "internal/gate/gate.go")
			},
			commit: test.Commit{SHA: "c6c6c6c6c6c6c6c6", Message: "Bump the release notes", Paths: []string{"README.md"}},
		},
		{
			name: "a trunk merged into the work's branch",
			file: func(h *harness) string {
				return h.file("Rework the waiter loop", "rebased on main daily", "--kind", "todo")
			},
			commit: test.Commit{SHA: "c7c7c7c7c7c7c7c7", Message: "Merge branch 'main' into waiter-loop"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newLandedHarness(t)
			h.must("", "claim", "main")
			id := tc.file(h)

			h.land(tc.commit)

			line := landedLine(h.runPrompt())
			want := id + " by " + tc.commit.SHA[:8] + " "

			if tc.matched != strings.Contains(line, want) {
				t.Fatalf("a landed commit should reach the seat exactly where a test matches it:\n%s", line)
			}

			if tc.matched && (!strings.Contains(line, "("+tc.test+")") || !strings.Contains(line, retireCommand(id, tc.commit.SHA))) {
				t.Errorf("the notice should name the test that matched and the retire command:\n%s", line)
			}
		})
	}
}

func TestALandedCommitMatchesByALinkedEntryID(t *testing.T) {
	cases := []struct {
		name    string
		connect bool
		file    func(h *harness) string
		message string
		item    bool
		matched bool
	}{
		{
			name: "the full id of a linked entry",
			file: func(h *harness) string {
				return h.file("Rework the waiter loop", "body", "--kind", "todo", "--link", activeDecision)
			},
			message: "Tidy\n\nRefs " + activeDecision, matched: true,
		},
		{
			name: "a unique short id of a linked entry",
			file: func(h *harness) string {
				return h.file("Rework the waiter loop", "body", "--kind", "todo", "--link", activeDecision)
			},
			message: "Tidy\n\nSettles d-tac-act.", matched: true,
		},
		{
			name:    "the qualified id of a connected repo's entry",
			connect: true,
			file: func(h *harness) string {
				return h.file("Rework the waiter loop", "body", "--kind", "todo", "--link", fdbox+":"+foreignActive)
			},
			message: "Tidy\n\nRefs " + fdbox + ":" + foreignActive, matched: true,
		},
		{
			name: "an item's linked entry",
			file: func(h *harness) string {
				return h.file("Rework the waiter loop", "body", "--kind", "todo", "--item", activeDecision)
			},
			message: "Tidy\n\nRefs " + activeDecision, item: true, matched: true,
		},
		{
			name: "an entry no live todo links",
			file: func(h *harness) string {
				return h.file("Rework the waiter loop", "body", "--kind", "todo", "--link", activeDecision)
			},
			message: "Tidy\n\nRefs " + closedDecision,
		},
		{
			name: "a short id two linked entries share",
			file: func(h *harness) string {
				test.WriteFile(h.t, h.repo+"/.sdd/graph/2026/10/24-100000-d-tac-act.md", "---\ntype: decision\n---\n")
				h.file("Older waiter work", "body", "--kind", "todo", "--link", "20261024-100000-d-tac-act")

				return h.file("Rework the waiter loop", "body", "--kind", "todo", "--link", activeDecision)
			},
			message: "Tidy\n\nSettles d-tac-act.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newLandedHarness(t)
			if tc.connect {
				withConnectedRepos(t, h)
			}

			h.must("", "claim", "main")
			id := tc.file(h)

			h.land(test.Commit{SHA: "d1d1d1d1d1d1d1d1", Message: tc.message})

			line := landedLine(h.runPrompt())

			target := id
			if tc.item {
				shown := h.must("", "show", id, "--again")
				target = id + "#" + itemPattern.FindStringSubmatch(shown)[1]
			}

			if tc.matched != strings.Contains(line, target+" by d1d1d1d1 ") {
				t.Fatalf("a message naming a linked entry should map exactly that todo or item:\n%s", line)
			}

			if tc.matched && !strings.Contains(line, "(id)") {
				t.Errorf("the notice should name the id test:\n%s", line)
			}

			if tc.item && !strings.Contains(line, "agentic-memory ack "+target+" --done") {
				t.Errorf("an item's match should name its ack command:\n%s", line)
			}
		})
	}
}

func TestALandedMappingWaitsForTheBrief(t *testing.T) {
	h := newLandedHarness(t)
	h.must("", "claim", "main")
	id := h.file("Rework the waiter loop", "body", "--kind", "todo", "--about", "internal/gate/gate.go")

	h.land(test.Commit{SHA: "e1e1e1e1e1e1e1e1", Message: "Rework waiter", Paths: []string{"internal/gate/gate.go"}})

	// A round a seatless session starts stores the mapping for the seat.
	h.must("", "release")

	if line := landedLine(h.runPrompt()); line != "" {
		t.Fatalf("a session holding no seat should be told nothing:\n%s", line)
	}

	h.must(h.sessionEnd("logout"), "session-end")

	if out := h.must(`{"session_id":"`+h.session+`"}`, "stop"); strings.Contains(out, "landed") {
		t.Errorf("no Stop output should carry a landed commit:\n%s", out)
	}

	other := newHarnessSharing(h)
	brief := other.must("", "claim", "main")

	if !strings.HasSuffix(briefLine(brief, id), "retire? landed: e1e1e1e1 Rework waiter") {
		t.Fatalf("the claim brief should end the entry's line with the landed commit:\n%s", brief)
	}

	if line := briefLine(other.must("", "brief"), id); line != h.label(id)+" Rework the waiter loop (seen)  retire? landed: e1e1e1e1 Rework waiter" {
		t.Errorf("a delivered mapping should ride the entry's pointer in the next brief:\n%s", line)
	}

	if line := briefLine(other.must("", "brief", "--again"), id); !strings.HasPrefix(line, h.label(id)+" Rework the waiter loop  [") ||
		!strings.HasSuffix(line, "retire? landed: e1e1e1e1 Rework waiter") {
		t.Errorf("a brief asked again should print the flagged entry's line whole:\n%s", line)
	}

	if line := landedLine(other.runPrompt()); line != "" {
		t.Errorf("a mapping a brief named should reach no hook of the same context:\n%s", line)
	}

	if slot := other.slot("landed"); !strings.Contains(slot, `"target":"`+id+`"`) {
		t.Errorf("a delivered mapping should stay in the store:\n%s", slot)
	}
}

func TestLandedMappingsArriveTogetherOnce(t *testing.T) {
	h := newLandedHarness(t)
	h.must("", "claim", "main")
	first := h.file("Rework the waiter loop", "body", "--kind", "todo", "--about", "internal/gate/gate.go")
	second := h.file("Refresh the readme", "body", "--kind", "todo", "--about", "README.md")

	h.land(
		test.Commit{SHA: "f3f3f3f3f3f3f3f3", Message: "Readme", Paths: []string{"README.md"}},
		test.Commit{SHA: "f2f2f2f2f2f2f2f2", Message: "Gate two", Paths: []string{"internal/gate/gate.go"}},
		test.Commit{SHA: "f1f1f1f1f1f1f1f1", Message: "Gate one", Paths: []string{"internal/gate/gate.go"}},
	)

	line := landedLine(h.runPrompt())

	for _, want := range []string{first + " by f2f2f2f2 ", second + " by f3f3f3f3 "} {
		if !strings.Contains(line, want) {
			t.Errorf("the mapping of every entry should arrive in the one line:\n%s", line)
		}
	}

	if strings.Contains(line, "f1f1f1f1") {
		t.Errorf("of two commits scoring alike for one entry, the newer alone should be reported:\n%s", line)
	}

	if strings.Count(h.runPrompt()+h.bashHook("ls"), "landed commits") != 0 {
		t.Errorf("mappings should be delivered once")
	}
}

func TestALandedMappingReachesTheToolHookOfTheMainThread(t *testing.T) {
	h := newLandedHarness(t)
	h.must("", "claim", "main")
	id := h.file("Rework the waiter loop", "body", "--kind", "todo", "--about", "internal/gate/gate.go")
	h.runPrompt()

	h.land(test.Commit{SHA: "a7a7a7a7a7a7a7a7", Message: "Gate", Paths: []string{"internal/gate/gate.go"}})

	if line := landedLine(h.bashHook("ls")); line != "" {
		t.Errorf("a call landing nothing within the interval should start no round:\n%s", line)
	}

	if line := landedLine(h.subagentHook("git merge feature")); line != "" {
		t.Errorf("a subagent's call should not take the seat's notice:\n%s", line)
	}

	if line := landedLine(h.bashHook("git -C . commit -m done")); !strings.Contains(line, id+" by a7a7a7a7 ") {
		t.Errorf("the main thread's next tool call should get the mapping:\n%s", line)
	}
}

func TestAnEndedTargetDropsItsMapping(t *testing.T) {
	cases := []struct {
		name string
		end  func(h *harness, id string, item string)
	}{
		{name: "retired", end: func(h *harness, id string, _ string) { h.must("", "retire", id) }},
		{name: "superseded", end: func(h *harness, id string, _ string) {
			h.must("", "supersede", id, "--because", "folded", "--items", "leave")
		}},
		{name: "its item ticked", end: func(h *harness, id string, item string) { h.must("", "ack", id+"#"+item, "--done") }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newLandedHarness(t)
			h.must("", "claim", "main")
			out := h.must("body", "new", "--title", "Rework the waiter loop", "--topic", "gate", "--kind", "todo",
				"--item", "build waiter polling deadline", "--item", "capture the decision", "--about", "internal/gate/gate.go")
			id := idPattern.FindString(out)
			item := itemPattern.FindStringSubmatch(out)[1]

			h.land(test.Commit{SHA: "a8a8a8a8a8a8a8a8", Message: "Build waiter polling deadline", Paths: []string{"internal/gate/gate.go"}})
			h.subagentHook("git pull")

			if slot := h.slot("landed"); !strings.Contains(slot, id+"#"+item) || !strings.Contains(slot, `"target":"`+id+`"`) {
				t.Fatalf("both the todo and its item should hold a mapping:\n%s", slot)
			}

			tc.end(h, id, item)

			gone := id
			if tc.name == "its item ticked" {
				gone = id + "#" + item
			}

			brief := h.must("", "brief")
			if gone == id && strings.Contains(brief, "landed: a8a8a8a8") {
				t.Errorf("an ended entry's mapping should be gone from the brief:\n%s", brief)
			}

			if strings.Contains(brief, "ack #"+item+"?") {
				t.Errorf("an ended item's mapping should be gone from the brief:\n%s", brief)
			}

			if slot := h.slot("landed"); strings.Contains(slot, `"target":"`+gone+`"`) {
				t.Errorf("an ended target's mapping should be gone from the store:\n%s", slot)
			}
		})
	}
}

// release frees the seat, so a round's mapping stays stored until the next
// claim.
func (h *harness) release() {
	h.t.Helper()
	h.must("", "release")
}

// subagentHook runs a subagent's Bash call through the tool hook: it starts
// a round as any call does, and takes no notice meant for the seat.
func (h *harness) subagentHook(command string) string {
	h.t.Helper()

	call := fmt.Sprintf(`{"session_id":%q,"agent_id":"a1","tool_name":"Bash","tool_input":{"command":%q}}`, h.session, command)

	return toolContext(h.must(call, "tool"))
}

func TestARoundReadsOnlyWhatIsNew(t *testing.T) {
	h := newLandedHarness(t)
	h.must("", "claim", "main")
	h.file("Rework the waiter loop", "body", "--kind", "todo", "--about", "internal/gate/gate.go")

	h.land(test.Commit{SHA: "a1a1a1a1a1a1a1a1", Message: "One"})
	h.runPrompt()

	reads := h.logReads()
	if len(reads) != 1 || !strings.HasPrefix(reads[0][len(reads[0])-2], "--since=2026-10-25T09:") {
		t.Fatalf("a first round should read from the oldest live filing time: %v", reads)
	}

	h.clock.Advance(time.Hour)
	h.runPrompt()

	if reads := h.logReads(); len(reads) != 1 {
		t.Errorf("a round with no new commit should read no commits: %v", reads)
	}

	h.land(test.Commit{SHA: "a2a2a2a2a2a2a2a2", Message: "Two"})
	h.clock.Advance(time.Hour)
	h.runPrompt()

	if reads := h.logReads(); len(reads) != 2 || reads[1][len(reads[1])-1] != "a1a1a1a1a1a1a1a1..HEAD" {
		t.Errorf("a round over new commits should read from its mark: %v", reads)
	}
}

func TestAFirstRoundWithoutLiveWorkOnlySetsTheMark(t *testing.T) {
	h := newLandedHarness(t)
	h.must("", "claim", "main")
	h.file("Gate note", "body")
	h.land(test.Commit{SHA: "a3a3a3a3a3a3a3a3", Message: "One"})

	h.runPrompt()

	if reads := h.logReads(); len(reads) != 0 {
		t.Errorf("a first round without a live todo or trap should read no commits: %v", reads)
	}

	if marks := h.slots("landed-scan/"); len(marks) != 1 || !strings.Contains(fmt.Sprint(marks), "a3a3a3a3a3a3a3a3") {
		t.Errorf("the round should set the mark to HEAD: %v", marks)
	}
}

func TestAnEntryNeverMapsToAnOlderCommit(t *testing.T) {
	h := newLandedHarness(t)
	h.must("", "claim", "main")
	h.file("Gate note", "body")
	h.land(test.Commit{SHA: "a4a4a4a4a4a4a4a4", Message: "Gate", At: landedAt.Add(-5 * time.Hour), Paths: []string{"internal/gate/gate.go"}})
	h.runPrompt()

	id := h.file("Rework the waiter loop", "body", "--kind", "todo", "--about", "internal/gate/gate.go")
	h.land(test.Commit{SHA: "a5a5a5a5a5a5a5a5", Message: "Readme", Paths: []string{"README.md"}})
	h.clock.Advance(time.Hour)

	h.git.SetCommits(append([]test.Commit{{
		SHA: "a6a6a6a6a6a6a6a6", Parents: []string{"a5a5a5a5a5a5a5a5"}, At: landedAt.Add(-4 * time.Hour),
		Message: "Gate again", Paths: []string{"internal/gate/gate.go"},
	}}, h.git.Commits...))

	if line := landedLine(h.runPrompt()); strings.Contains(line, id) {
		t.Errorf("a commit dated before the todo's filing should never map to it:\n%s", line)
	}
}

func TestARoundRecoversFromARebase(t *testing.T) {
	h := newLandedHarness(t)
	h.must("", "claim", "main")
	id := h.file("Rework the waiter loop", "body", "--kind", "todo", "--about", "internal/gate/gate.go")
	h.land(test.Commit{SHA: "b1b1b1b1b1b1b1b1", Message: "One", At: landedAt})
	h.runPrompt()

	h.git.SetCommits([]test.Commit{
		{SHA: "b3b3b3b3b3b3b3b3", Parents: []string{"b2b2b2b2b2b2b2b2"}, At: landedAt.Add(2 * time.Hour), Message: "Gate", Paths: []string{"internal/gate/gate.go"}},
		{SHA: "b2b2b2b2b2b2b2b2", Parents: []string{baseCommit}, At: landedAt.Add(time.Hour), Message: "One, rebased"},
		{SHA: baseCommit, At: landedAt.Add(-6 * time.Hour), Message: "Initial import"},
	})
	h.clock.Advance(time.Hour)

	line := landedLine(h.runPrompt())

	reads := h.logReads()
	if last := reads[len(reads)-1]; last[len(last)-2] != "--since="+landedAt.Format(time.RFC3339) {
		t.Errorf("a mark HEAD left should give way to the commits dated after it: %v", last)
	}

	if !strings.Contains(line, id+" by b3b3b3b3 ") {
		t.Errorf("the round should recover the commits after the rebase:\n%s", line)
	}
}

func TestTheHookOnlyStartsARound(t *testing.T) {
	h := newLandedHarness(t)
	h.starter.Run = nil
	h.must("", "claim", "main")
	h.file("Rework the waiter loop", "body", "--kind", "todo", "--about", "internal/gate/gate.go")
	h.land(test.Commit{SHA: "c8c8c8c8c8c8c8c8", Message: "Gate", Paths: []string{"internal/gate/gate.go"}})

	h.runPrompt()
	h.bashHook("git merge feature")

	if starts := h.starter.Starts(); len(starts) != 1 || starts[0][0] != "landed-round" {
		t.Errorf("two due events should start one round: %v", starts)
	}

	if reads := h.logReads(); len(reads) != 0 {
		t.Errorf("the hook should not read git history itself: %v", reads)
	}

	if out := h.must("", "landed-round", "not-the-token"); out != "" || len(h.logReads()) != 0 {
		t.Errorf("a round not holding the lock should exit at once: %v", h.logReads())
	}
}

func TestALargeRangeTakesSeveralRounds(t *testing.T) {
	h := newLandedHarness(t)
	h.must("", "claim", "main")
	id := h.file("Rework the waiter loop", "body", "--kind", "todo", "--about", "internal/gate/gate.go")

	var commits []test.Commit

	for index := 250; index >= 1; index-- {
		commit := test.Commit{SHA: fmt.Sprintf("%016x", index), At: landedAt.Add(time.Duration(index) * time.Minute), Message: "Step"}
		if index == 230 {
			commit.Paths = []string{"internal/gate/gate.go"}
		}

		commits = append(commits, commit)
	}

	h.land(commits...)

	if line := landedLine(h.runPrompt()); line != "" {
		t.Fatalf("a commit past the first batch should wait for the next round:\n%s", line)
	}

	if marks := fmt.Sprint(h.slots("landed-scan/")); !strings.Contains(marks, fmt.Sprintf("%016x", 200)) {
		t.Fatalf("the round should move the mark to the last commit it processed: %s", marks)
	}

	h.clock.Advance(time.Hour)

	if line := landedLine(h.runPrompt()); !strings.Contains(line, id+" by "+fmt.Sprintf("%016x", 230)[:8]+" ") {
		t.Errorf("the next round should go on from the mark:\n%s", line)
	}

	if marks := fmt.Sprint(h.slots("landed-scan/")); !strings.Contains(marks, fmt.Sprintf("%016x", 250)) {
		t.Errorf("the last round should move the mark to HEAD: %s", marks)
	}
}

// mergeOf is a merge of side, newest first, onto first, git subjecting it
// by default.
func mergeOf(sha string, branch string, first string, side ...test.Commit) []test.Commit {
	merge := test.Commit{SHA: sha, Parents: []string{first, side[0].SHA}, At: landedAt.Add(3 * time.Hour), Message: "Merge branch '" + branch + "'"}

	for index := range side {
		if side[index].Parents == nil {
			parent := first
			if index+1 < len(side) {
				parent = side[index+1].SHA
			}

			side[index].Parents = []string{parent}
		}

		if side[index].At.IsZero() {
			side[index].At = landedAt
		}
	}

	return append([]test.Commit{merge}, side...)
}

func TestAMergeCarriesTheEvidenceOfItsCommits(t *testing.T) {
	cases := []struct {
		name string
		file func(h *harness) string
		side []test.Commit
	}{
		{
			name: "a merged commit naming a linked entry",
			file: func(h *harness) string {
				return h.file("Rework the waiter loop", "body", "--kind", "todo", "--link", activeDecision)
			},
			side: []test.Commit{{SHA: "1a1a1a1a1a1a1a1a", Message: "Tidy\n\nRefs " + activeDecision}},
		},
		{
			name: "a merged commit touching an anchored path",
			file: func(h *harness) string {
				return h.file("Rework the waiter loop", "body", "--kind", "todo", "--about", "README.md")
			},
			side: []test.Commit{{SHA: "1b1b1b1b1b1b1b1b", Message: "Tidy", Paths: []string{"README.md"}}},
		},
		{
			name: "a merged commit with a similar subject",
			file: func(h *harness) string {
				return h.file("Rework waiter polling loop deadline", "waiter polling loop deadline", "--kind", "todo")
			},
			side: []test.Commit{{SHA: "1c1c1c1c1c1c1c1c", Message: "Rework waiter polling loop deadline"}},
		},
		{
			name: "several merged commits matching",
			file: func(h *harness) string {
				return h.file("Rework the waiter loop", "body", "--kind", "todo", "--about", "README.md")
			},
			side: []test.Commit{
				{SHA: "1d1d1d1d1d1d1d1d", Message: "Tidy two", Paths: []string{"README.md"}},
				{SHA: "1e1e1e1e1e1e1e1e", Message: "Tidy one", Paths: []string{"README.md"}},
			},
		},
		{
			name: "a merged commit older than the todo",
			file: func(h *harness) string {
				return h.file("Rework the waiter loop", "body", "--kind", "todo", "--about", "README.md")
			},
			side: []test.Commit{{SHA: "1f1f1f1f1f1f1f1f", Message: "Tidy", At: landedAt.Add(-5 * time.Hour), Paths: []string{"README.md"}}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newLandedHarness(t)
			h.must("", "claim", "main")
			id := tc.file(h)

			// The merge lists no path of its own here, so only the merged
			// commits can carry the evidence.
			h.land(mergeOf("9a9a9a9a9a9a9a9a", "topic", baseCommit, tc.side...)...)

			line := landedLine(h.runPrompt())

			if strings.Count(line, id+" by ") != 1 || !strings.Contains(line, id+" by 9a9a9a9a ") {
				t.Fatalf("the merge's evidence should map the entry to the merge, once:\n%s", line)
			}

			for _, merged := range tc.side {
				if strings.Contains(line, " by "+merged.SHA[:8]) {
					t.Errorf("a commit judged through its merge should map nothing on its own:\n%s", line)
				}
			}
		})
	}
}

func TestTheHighestScoringCommitIsReported(t *testing.T) {
	direct := func(sha string, message string) test.Commit {
		return test.Commit{SHA: sha, Parents: []string{baseCommit}, Message: message, Paths: []string{"README.md"}}
	}

	// mergeOnto merges a side commit touching the anchored path onto first:
	// strength 0.5, score 0.6.
	mergeOnto := func(sha string, side string, first string) []test.Commit {
		return mergeOf(sha, "topic", first, test.Commit{SHA: side, Message: "Tidy", Paths: []string{"README.md"}})
	}

	cases := []struct {
		name   string
		rounds [][]test.Commit
		want   string
	}{
		{
			name:   "a merge over a direct commit of the same strength",
			rounds: [][]test.Commit{append(mergeOnto("8a8a8a8a8a8a8a8a", "8c8c8c8c8c8c8c8c", "8b8b8b8b8b8b8b8b"), direct("8b8b8b8b8b8b8b8b", "Readme"))},
			want:   "8a8a8a8a8a8a8a8a",
		},
		{
			name: "a direct commit naming the linked id over a merge on a path",
			rounds: [][]test.Commit{append(mergeOnto("6a6a6a6a6a6a6a6a", "6c6c6c6c6c6c6c6c", "6b6b6b6b6b6b6b6b"),
				direct("6b6b6b6b6b6b6b6b", "Readme\n\nRefs "+activeDecision))},
			want: "6b6b6b6b6b6b6b6b",
		},
		{
			name: "a later round's higher score replaces the undelivered mapping",
			rounds: [][]test.Commit{
				{direct("7b7b7b7b7b7b7b7b", "Readme")},
				mergeOnto("7a7a7a7a7a7a7a7a", "7c7c7c7c7c7c7c7c", "7b7b7b7b7b7b7b7b"),
			},
			want: "7a7a7a7a7a7a7a7a",
		},
		{
			name: "a later round's lower score leaves the undelivered mapping",
			rounds: [][]test.Commit{
				{direct("5b5b5b5b5b5b5b5b", "Readme\n\nRefs "+activeDecision)},
				mergeOnto("5a5a5a5a5a5a5a5a", "5c5c5c5c5c5c5c5c", "5b5b5b5b5b5b5b5b"),
			},
			want: "5b5b5b5b5b5b5b5b",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newLandedHarness(t)
			h.must("", "claim", "main")
			id := h.file("Rework the waiter loop", "body", "--kind", "todo", "--about", "README.md", "--link", activeDecision)
			h.release()

			for _, round := range tc.rounds {
				h.land(round...)
				h.clock.Advance(time.Hour)
				h.runPrompt()
			}

			slot := h.slot("landed")
			if strings.Count(slot, `"target":"`+id+`"`) != 1 || !strings.Contains(slot, tc.want) {
				t.Fatalf("one mapping, the highest scoring, should stand for the todo:\n%s", slot)
			}

			line := briefLine(h.must("", "claim", "main"), id)
			if !strings.Contains(line, "landed: "+tc.want[:8]) {
				t.Errorf("the brief should carry the reported commit:\n%s", line)
			}
		})
	}
}

func TestAMergedBranchEndsAfterItsDeletion(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(h *harness)
		retired bool
	}{
		{name: "a tip a probe recorded", retired: true, prepare: func(h *harness) {
			h.git.Branches = map[string][]string{"feature": {"release"}}
			h.git.Tips = map[string]string{"feature": "5e5e5e5e5e5e5e5e"}
			h.must("", "brief")

			h.git.Branches = nil
			h.git.Ancestors = map[string][]string{"5e5e5e5e5e5e5e5e": {"main"}}
		}},
		{name: "a recorded tip not on the target", prepare: func(h *harness) {
			h.git.Branches = map[string][]string{"feature": {"release"}}
			h.git.Tips = map[string]string{"feature": "5e5e5e5e5e5e5e5e"}
			h.must("", "brief")

			h.git.Branches = nil
		}},
		{name: "a branch never seen", prepare: func(*harness) {}},
		{name: "a tip a default merge gave", retired: true, prepare: func(h *harness) {
			h.land(test.Commit{
				SHA: "6a6a6a6a6a6a6a6a", Parents: []string{baseCommit, "6b6b6b6b6b6b6b6b"}, Message: "Merge branch 'feature' into main",
			})
			h.git.Ancestors = map[string][]string{"6b6b6b6b6b6b6b6b": {"main"}}
			h.runPrompt()
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newLandedHarness(t)
			h.must("", "claim", "main")
			h.file("Ship the feature", "body", "--kind", "todo", "--until", "merged:feature")

			tc.prepare(h)
			h.must("", "brief")

			out := h.must("", "search", "--term", "Ship the feature", "--status", "retired", "--status", "live")
			if strings.Contains(out, "end reached: merged:feature") != tc.retired {
				t.Errorf("a deleted branch should end the todo exactly where its recorded tip reached the target:\n%s", out)
			}
		})
	}
}

func TestATodoWithoutAnEndGetsTheFilingHint(t *testing.T) {
	const hint = "no end declared: --until merged:<branch> ends it at its merge; " +
		"or split the work into items, such as --item build --item capture"

	cases := []struct {
		name  string
		extra []string
		hint  bool
	}{
		{name: "a todo with neither", extra: []string{"--kind", "todo"}, hint: true},
		{name: "a todo with --until", extra: []string{"--kind", "todo", "--until", "merged:feature"}},
		{name: "a todo with --item", extra: []string{"--kind", "todo", "--item", "build"}},
		{name: "a note", extra: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")

			out := h.must("body", append([]string{"new", "--title", "Rework the waiter loop", "--topic", "gate"}, tc.extra...)...)
			if strings.Contains(out, hint) != tc.hint {
				t.Errorf("the filing hint should print exactly for a todo declaring no end:\n%s", out)
			}
		})
	}
}
