package memory_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestSearchAndViewLinesFoldEndedLinks(t *testing.T) {
	h := newHarness(t)
	withEndedGraph(t, h)
	h.must("", "claim", "main")

	retired := h.file("A finding that ended", "body")
	h.must("", "retire", retired, "--because", "done")

	old := h.file("The first take on the waiter", "a finding")
	successor := h.file("The second take on the waiter", "a better finding")
	h.must("", "supersede", old, "--by", successor)

	hub := h.file("Waiter flaky on CI", "body", "--link", closedDecision, "--link", retired, "--link", settledDecision,
		"--link", activeDecision, "--link", old, "--link", supersededDecision)

	want := "  → " + settledDecision + " settled; → 2 ended, 1 superseded by " + successor + ", 1 superseded by " + itsSuccessor

	reads := []struct {
		name string
		out  string
	}{
		{"search", h.must("", "search", "--term", "flaky", "--again")},
		{"view", h.must("", "view", "--layout", "active:mine:as-list", "--again")},
	}

	for _, read := range reads {
		t.Run(read.name, func(t *testing.T) {
			got := ""

			for _, text := range strings.Split(read.out, "\n") {
				if strings.Contains(text, hub) {
					got = text
				}
			}

			if !strings.HasSuffix(got, want) {
				t.Errorf("the line should list settled links and fold the ended ones keeping each successor, got:\n%s", got)
			}
		})
	}

	shown := h.must("", "show", hub, "--again")
	if !strings.Contains(shown, "  → "+closedDecision+" closed by "+itsCloser+"\n") || !strings.Contains(shown, "  → "+retired+" retired\n") {
		t.Errorf("show's links section should still name each target's state in full:\n%s", shown)
	}
}

func TestAReturnsLineLeavesWhatItsReasonsSay(t *testing.T) {
	h := newHarness(t)
	withGraph(t, h)
	h.must("", "claim", "main")

	h.git.SwitchBranch("feature")
	id := h.file("Gate reads README", "see README.md", "--link", closedDecision, "--link", activeDecision)
	h.git.SwitchBranch("main")
	h.file("Note citing the gate finding", "cites "+id)
	test.WriteFile(t, filepath.Join(h.repo, "README.md"), "# app, changed\n")

	// A view reads only cached graph statuses; a show fills the cache.
	h.must("", "show", id, "--no-heat")

	out := h.must("", "view", "--layout", "active:mine:returns:as-list", "--again")

	var header string

	for _, text := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(text), id) {
			header = text
		}
	}

	for _, reason := range []string{"anchor drifted: README.md", "link " + closedDecision + " closed by " + itsCloser, "branch filed on feature"} {
		if !strings.Contains(out, reason) {
			t.Fatalf("the returns section should list the reason %q:\n%s", reason, out)
		}
	}

	for _, flag := range []string{"drifted:", "ended", "other branch:"} {
		if strings.Contains(header, flag) {
			t.Errorf("the entry line should leave out what its reasons restate, %q among:\n%s", flag, header)
		}
	}

	if !strings.Contains(header, "← 1") {
		t.Errorf("the entry line should keep the flags no reason restates:\n%s", header)
	}
}
