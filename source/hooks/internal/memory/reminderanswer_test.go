package memory_test

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

var hookLineAddress = regexp.MustCompile(`#([a-z0-9]{3}) hook: (?:landed|captured)`)

// hookLineID is the address of the one landed or captured line the seat's
// ledger shows.
func (h *harness) hookLineID() string {
	h.t.Helper()

	found := hookLineAddress.FindStringSubmatch(h.must("", "show", "state"))
	if found == nil {
		h.t.Fatal("the ledger should hold the hook's line")
	}

	return found[1]
}

func (h *harness) nudgedAgain() bool {
	h.t.Helper()

	return strings.Contains(h.said(h.promptSaying("go on"), "prompt"), "Context near compaction")
}

func TestAReminderWaitsForTheLandedLinesSinceIt(t *testing.T) {
	cases := []struct {
		name     string
		lands    bool
		answer   func(h *harness, line string)
		answered bool
	}{
		{name: "a throwaway line, with work landed since", lands: true, answer: func(h *harness, _ string) { h.addLine("x") }},
		{
			name: "a line citing the landed line's address", lands: true, answered: true,
			answer: func(h *harness, line string) { h.addLine("gate fix checked, see state#" + line) },
		},
		{
			name: "a line citing the landed commit", lands: true, answered: true,
			answer: func(h *harness, _ string) { h.addLine("9c0ffee1 settles the gate fix") },
		},
		{
			name: "a tick of the landed line", lands: true, answered: true,
			answer: func(h *harness, line string) { h.must("", "ack", "state#"+line, "--done") },
		},
		{
			name: "a drop of the landed line", lands: true, answered: true,
			answer: func(h *harness, line string) { h.must("", "ack", "state#"+line, "--remove") },
		},
		{
			name: "a citing line dropped again", lands: true,
			answer: func(h *harness, line string) {
				h.must("", "ack", "state#"+h.addLine("see state#"+line), "--remove")
			},
		},
		{name: "a throwaway line, with nothing landed since", answered: true, answer: func(h *harness, _ string) { h.addLine("x") }},
		{
			name: "an entry, with nothing landed since", answered: true,
			answer: func(h *harness, _ string) { h.file("Where the gate stands", "filed") },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")
			h.clock.Advance(time.Minute)
			nudged(t, h)
			h.clock.Advance(time.Minute)

			line := ""

			if tc.lands {
				h.git.Commit = "9c0ffee1"
				h.git.Log = map[string]string{"15af1a8c..HEAD": "9c0ffee1 fix: the gate reads MCP calls\n"}
				h.must(h.stop(), "stop")
				line = h.hookLineID()
				h.clock.Advance(time.Minute)
			}

			tc.answer(h, line)
			h.clock.Advance(time.Minute)

			if again := h.nudgedAgain(); again == tc.answered {
				t.Errorf("the reminder should count as answered exactly once the landed lines since it settle, nudged again: %v", again)
			}
		})
	}
}

func TestALaterReminderStillWaitsForTheLinesSinceTheFirst(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.clock.Advance(time.Second)
	nudged(t, h)
	h.clock.Advance(time.Second)

	h.git.Commit = "9c0ffee1"
	h.git.Log = map[string]string{"15af1a8c..HEAD": "9c0ffee1 fix: the gate reads MCP calls\n"}
	h.must(h.stop(), "stop")
	h.hookLineID()
	h.clock.Advance(time.Second)

	if !h.nudgedAgain() {
		t.Fatal("the second reminder should fire while nothing answered the first")
	}

	h.clock.Advance(time.Second)
	h.addLine("x")
	h.clock.Advance(time.Second)

	if !h.nudgedAgain() {
		t.Error("a line landed between two reminders should keep both unanswered until settled")
	}
}

func TestAReminderNamesTheHookLinesItWaitsFor(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.clock.Advance(time.Minute)
	h.sensor(70)

	if first := h.said(h.promptSaying("go on"), "prompt"); !strings.HasSuffix(first, "< body") {
		t.Errorf("a reminder with no hook line to wait on should end at the nudge, got %q", first)
	}

	h.clock.Advance(time.Minute)

	h.git.Commit = "9c0ffee1"
	h.git.Log = map[string]string{"15af1a8c..HEAD": "9c0ffee1 fix: the gate reads MCP calls\n"}
	h.must(h.stop(), "stop")
	line := h.hookLineID()
	h.clock.Advance(time.Minute)
	h.addLine("x")
	h.clock.Advance(time.Minute)

	reminder := h.said(h.promptSaying("go on"), "prompt")
	if !strings.Contains(reminder, "< body"+openHookAsk+"state#"+line) {
		t.Errorf("a reminder waiting on hook lines should name each and what settles it, got %q", reminder)
	}
}

func TestAReminderWaitsForTheCapturedLinesSinceIt(t *testing.T) {
	cases := []struct {
		name     string
		answer   string
		answered bool
	}{
		{name: "a throwaway line", answer: "x"},
		{name: "a line citing the captured entry", answer: "decided 20261025-120000-d-tac-one", answered: true},
		{name: "a line pinning the captured entry", answer: "20261025-120000-d-tac-one", answered: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			withGraph(t, h)
			h.must("", "claim", "main")
			h.callTool("Bash", map[string]string{"command": "ls"})
			h.clock.Advance(time.Minute)
			nudged(t, h)
			h.clock.Advance(time.Minute)

			test.WriteFile(t, filepath.Join(h.repo, ".sdd", "graph", "2026", "10", "25-120000-d-tac-one.md"),
				"---\ntype: decision\ntitle: Seat state as a ledger\n---\n")
			h.callTool("Bash", map[string]string{"command": "sdd new"})
			h.hookLineID()
			h.clock.Advance(time.Minute)

			h.addLine(tc.answer)
			h.clock.Advance(time.Minute)

			if again := h.nudgedAgain(); again == tc.answered {
				t.Errorf("the reminder should count as answered exactly once the captured lines since it settle, nudged again: %v", again)
			}
		})
	}
}

func TestAReminderFoldsTheHookLinesPastFive(t *testing.T) {
	cases := []struct {
		name   string
		landed int
		folded bool
	}{
		{name: "five lines", landed: 5, folded: false},
		{name: "six lines", landed: 6, folded: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")
			h.clock.Advance(time.Minute)
			h.sensor(70)
			h.said(h.promptSaying("go on"), "prompt")
			h.clock.Advance(time.Minute)

			h.landCommits(1, tc.landed)
			h.clock.Advance(time.Minute)

			reminder := h.said(h.promptSaying("go on"), "prompt")
			named := len(regexp.MustCompile(`state#[0-9a-z]+`).FindAllString(reminder[strings.Index(reminder, "ticked or dropped: "):], -1))
			fold := strings.Contains(reminder, "+1 more: agentic-memory show state  (run before concluding the nudge answered)")

			if fold != tc.folded || named != min(tc.landed, 5) {
				t.Errorf("a reminder should name up to five hook lines and fold the rest into a count, got %q", reminder)
			}

			if strings.Contains(reminder, "\n") {
				t.Errorf("a reminder should stay one line, got %q", reminder)
			}
		})
	}
}
