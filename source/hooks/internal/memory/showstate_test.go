package memory_test

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// shownLineID is a line's address where show state prints it with its box.
var shownLineID = regexp.MustCompile(`(?m)^  \[.\] #([a-z0-9]{3})`)

// landCommits has a Stop record the commits numbered first to last, each
// as a landed line, the oldest added first.
func (h *harness) landCommits(first int, last int) {
	h.t.Helper()

	var history []string
	for index := first; index <= last; index++ {
		history = append([]string{fmt.Sprintf("c%07d commit number %d", index, index)}, history...)
	}

	h.git.Commit = fmt.Sprintf("c%07d", last)
	h.git.Log = map[string]string{"15af1a8c..HEAD": strings.Join(history, "\n") + "\n"}
	h.must(h.stop(), "stop")
}

// shownLine is the line show state prints for a landed commit's number.
func shownLine(shown string, number int) string {
	for _, line := range strings.Split(shown, "\n") {
		if strings.Contains(line, fmt.Sprintf(" commit number %d (", number)) {
			return line
		}
	}

	return ""
}

var (
	shownHookID   = regexp.MustCompile(`#([a-z0-9]{3}) hook:`)
	openAskedLine = regexp.MustCompile(`(?m)^  \[ \] #[a-z0-9]{3} hook: asked fix the gate for MCP reads`)
)

// tickLanded ticks the landed line of a commit's number.
func (h *harness) tickLanded(number int) {
	h.t.Helper()

	found := shownHookID.FindStringSubmatch(shownLine(h.must("", "show", "state", "--with-history"), number))
	if found == nil {
		h.t.Fatalf("the ledger should hold the landed line of commit %d", number)
	}

	h.must("", "ack", "state#"+found[1], "--done")
}

// liveAndSettled is a seat whose ledger holds an open agent line, a ticked
// one and fifteen landed lines, the newest ticked.
func liveAndSettled(t *testing.T) (*harness, string, string) {
	t.Helper()

	h := newHarness(t)
	h.must("", "claim", "main")
	open := h.addLine("gate fix halfway; next: docs")
	ticked := h.addLine("gate tests written")
	h.must("", "ack", "state#"+ticked, "--done")
	h.landCommits(0, 14)
	h.tickLanded(14)

	return h, open, ticked
}

func TestShowStatePrintsWhatIsLive(t *testing.T) {
	h, open, _ := liveAndSettled(t)

	shown := h.must("", "show", "state")

	if !strings.Contains(shown, "  [ ] #"+open+": gate fix halfway; next: docs") {
		t.Errorf("show state should print the open agent line, got:\n%s", shown)
	}

	if strings.Count(shown, "hook: landed") != 12 || shownLine(shown, 13) == "" || shownLine(shown, 2) == "" ||
		shownLine(shown, 14) != "" || shownLine(shown, 1) != "" {
		t.Errorf("show state should print the newest 12 open landed lines alone, got:\n%s", shown)
	}

	if strings.Contains(shown, "gate tests written") || strings.Contains(shown, "[x]") || strings.Contains(shown, "[e]") {
		t.Errorf("show state should print nothing settled, got:\n%s", shown)
	}

	if !strings.HasSuffix(strings.TrimSpace(shown), "\n  (3 older landed and captured: agentic-memory show state --with-history)\n"+
		"  (1 settled: agentic-memory show state --all-items)") {
		t.Errorf("show state should end on a fold line per hidden group, each naming its flag, got:\n%s", shown)
	}
}

func TestShowStateFlagsAddTheirGroup(t *testing.T) {
	h, _, ticked := liveAndSettled(t)

	cases := []struct {
		name        string
		flags       []string
		mcp         string
		landed      int
		settled     bool
		history     int
		settledFold int
	}{
		{name: "neither", landed: 12, history: 3, settledFold: 1},
		{name: "with history", flags: []string{"--with-history"}, mcp: `,"with_history":true`, landed: 15, settledFold: 1},
		{name: "all items", flags: []string{"--all-items"}, mcp: `,"all_items":true`, landed: 12, settled: true, history: 3},
		{
			name: "both", flags: []string{"--with-history", "--all-items"}, mcp: `,"with_history":true,"all_items":true`,
			landed: 15, settled: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			shown := h.must("", append([]string{"show", "state"}, tc.flags...)...)

			if strings.Count(shown, "hook: landed") != tc.landed {
				t.Errorf("show state should print the landed lines its flags ask for, got:\n%s", shown)
			}

			if strings.Contains(shown, "[x] #"+ticked+": gate tests written") != tc.settled {
				t.Errorf("show state should print the ticked line exactly where --all-items asks, got:\n%s", shown)
			}

			if strings.Contains(shown, "older landed and captured") != (tc.history > 0) ||
				strings.Contains(shown, "settled: agentic-memory show state --all-items") != (tc.settledFold > 0) {
				t.Errorf("show state should fold exactly the groups no flag prints, got:\n%s", shown)
			}

			var answer struct {
				State struct {
					Lines []struct {
						ID string `json:"id"`
					} `json:"lines"`
					Folded struct {
						History int `json:"history"`
						Settled int `json:"settled"`
					} `json:"folded"`
				} `json:"state"`
			}

			text := mcpText(t, h.mcp(`{"name":"show","arguments":{"ids":["state"]`+tc.mcp+`}}`))
			noEmptyKey(t, text)

			err := json.Unmarshal([]byte(text), &answer)
			if err != nil {
				t.Fatalf("MCP show should answer JSON: %v\n%s", err, text)
			}

			var textIDs, jsonIDs []string
			for _, found := range shownLineID.FindAllStringSubmatch(shown, -1) {
				textIDs = append(textIDs, found[1])
			}

			for _, line := range answer.State.Lines {
				jsonIDs = append(jsonIDs, line.ID)
			}

			if !slices.Equal(textIDs, jsonIDs) {
				t.Errorf("MCP show should carry the lines the text prints, got %v beside %v", jsonIDs, textIDs)
			}

			if answer.State.Folded.History != tc.history || answer.State.Folded.Settled != tc.settledFold {
				t.Errorf("MCP show should count what each fold holds back as the text does, got %+v", answer.State.Folded)
			}
		})
	}
}

func TestALandedLineEndsOutOfTheNewestTwelveOnceADayOld(t *testing.T) {
	cases := []struct {
		name  string
		after time.Duration
		tick  bool
		ended bool
	}{
		{name: "under a day old", after: 23 * time.Hour},
		{name: "over a day old", after: 25 * time.Hour, ended: true},
		{name: "over a day old, a newer line ticked", after: 25 * time.Hour, tick: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")
			h.landCommits(0, 12)

			if tc.tick {
				h.tickLanded(12)
			}

			h.clock.Advance(tc.after)

			before := h.logBytes()
			shown := h.must("", "show", "state", "--with-history")

			if strings.HasPrefix(strings.TrimSpace(shownLine(shown, 0)), "[e]") != tc.ended ||
				strings.Contains(shown, "    ended: out of the newest 12") != tc.ended {
				t.Errorf("the line out of the newest 12 should read as ended exactly once a day old, got:\n%s", shown)
			}

			if !strings.HasPrefix(strings.TrimSpace(shownLine(shown, 1)), "[ ]") {
				t.Errorf("a line among the newest 12 should stay open, got:\n%s", shown)
			}

			if !h.logUnchanged(before) {
				t.Error("a hook line's end should be worked out at the read, never written")
			}
		})
	}
}

func TestTheAskedLineNeverEndsByAge(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.said(h.promptSaying("fix the gate for MCP reads"), "prompt")
	h.must(h.stop(), "stop")
	h.clock.Advance(time.Minute)
	h.landCommits(0, 12)
	h.clock.Advance(25 * time.Hour)

	if shown := h.must("", "show", "state"); !openAskedLine.MatchString(shown) {
		t.Errorf("the asked line should stay open however old, got:\n%s", shown)
	}
}

func TestALineEndingOnItsOwnNeverAnswersANudge(t *testing.T) {
	cases := []struct {
		name     string
		cited    int
		answered bool
	}{
		{name: "the line that ended left uncited", cited: 1},
		{name: "every landed line cited", answered: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")
			h.clock.Advance(time.Minute)
			nudged(t, h)
			h.clock.Advance(time.Minute)
			h.landCommits(0, 12)
			h.clock.Advance(25 * time.Hour)

			var shas []string
			for index := tc.cited; index <= 12; index++ {
				shas = append(shas, fmt.Sprintf("c%07d", index))
			}

			h.addLine("checked " + strings.Join(shas, " "))
			h.clock.Advance(time.Minute)

			if shown := h.must("", "show", "state", "--with-history"); !strings.HasPrefix(strings.TrimSpace(shownLine(shown, 0)), "[e]") {
				t.Fatalf("the oldest landed line should read as ended, got:\n%s", shown)
			}

			if again := h.nudgedAgain(); again == tc.answered {
				t.Errorf("the nudge should wait on every landed line the agent left unsettled, ended or not, nudged again: %v", again)
			}
		})
	}
}

func TestTheBriefFoldCountsOpenHookLinesOnly(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.landCommits(0, 14)

	if brief := stateOf(h.must("", "brief")); !strings.Contains(brief, "\n  (3 more: agentic-memory show state --with-history)") {
		t.Errorf("the brief should fold the open hook lines past the newest 12, naming --with-history, got:\n%s", brief)
	}

	h.clock.Advance(25 * time.Hour)
	h.landCommits(15, 29)

	brief := h.must("", "brief")
	state := stateOf(brief)

	if strings.Count(state, "hook: landed") != 12 || !strings.Contains(state, "commit number 29 ") ||
		!strings.Contains(state, "\n  (3 more: agentic-memory show state --with-history)") {
		t.Errorf("the brief's fold should count the open hook lines alone, got:\n%s", state)
	}

	if strings.Contains(brief, "out of the newest") {
		t.Errorf("a hook line ending on its own should stay out of ended since last brief, got:\n%s", brief)
	}
}
