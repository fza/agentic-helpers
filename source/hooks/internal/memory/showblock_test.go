package memory_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestShowNamesEachStateOnce(t *testing.T) {
	h := newHarness(t)
	withGraph(t, h)
	h.must("", "claim", "main")

	log := filepath.Join(t.TempDir(), "build.log")
	test.WriteFile(t, log, "compile step passed\n")

	id := h.file("Gate reads README and gate.go", "see README.md and internal/gate/gate.go", "--attach", log)
	citer := h.file("Note citing the gate finding", "cites "+id)

	h.must("", "link", id, closedDecision)
	h.must("", "link", id, activeDecision)
	h.must("", "unlink", id, activeDecision)

	other := newHarnessSharing(h)
	other.must("", "claim", "drive")
	theirs := other.file("Drive's note on the gate", "a related finding")
	other.must("", "link", id, theirs)

	test.WriteFile(t, filepath.Join(h.repo, "README.md"), "# app, changed\n")

	// Anchor states print for a live entry only.
	live := h.must("", "show", id, "--again")
	view := h.must("", "view", "--layout", "kind(note):mine:as-bodies", "--again")

	h.must("", "retire", id, "--because", "first pass")
	h.must("", "revive", id)
	h.must("", "retire", id, "--because", "fixed in the gate")

	shown := h.must("", "show", id, "--again")
	header, _, _ := strings.Cut(shown, "\n")
	_, events, _ := strings.Cut(shown, "events:\n")

	checks := []struct {
		name  string
		holds bool
		why   string
	}{
		{"header status", strings.Contains(header, ": fixed in the gate"), "the header should carry the status"},
		{"header links", !strings.Contains(header, "→ "+closedDecision), "the header should leave link flags to the links section"},
		{"header anchors", !strings.Contains(header, "drifted"), "the header should leave anchor flags to the anchors line"},
		{"header citers", !strings.Contains(header, "← 1"), "the header should leave the citer count to the linked-from section"},
		{"anchors", strings.Contains(live, "\nanchors: README.md (drifted), internal/gate/gate.go\n"),
			"the anchors line should carry each anchor's state beside it"},
		{"links", strings.Contains(shown, "\nlinks:\n  → "+closedDecision+" closed by "+itsCloser+"\n"),
			"the links section should carry each link's state"},
		{"filed", strings.Contains(shown, "\nfiled: session "+test.Session[:8]+", branch main\n"),
			"the filed line should keep the session and branch alone"},
		{"filed stamp", !strings.Contains(shown, "\nfiled 20") && !strings.Contains(shown, "commit 15af1a8c"),
			"the filed line should leave the date and commit to the bracket"},
		{"linked from", strings.Contains(shown, "\nlinked from:\n  "+citer+" Note citing the gate finding (live)\n"),
			"linked from should print each citer as its id, title and status"},
		{"standing retire", strings.Count(events, "retired") == 1 && strings.Contains(events, "retired: first pass"),
			"events should keep an earlier retire and leave the standing one to the header"},
		{"revive", strings.Contains(events, "revived"), "events should keep a revive"},
		{"standing link", !strings.Contains(events, "linked to "+closedDecision), "events should leave a standing link to the links section"},
		{"unlinked", strings.Contains(events, "linked to "+activeDecision) && strings.Contains(events, "unlinked from "+activeDecision),
			"events should keep a link that no longer stands, and its unlink"},
		{"attachment", !strings.Contains(events, "attached build.log"), "events should leave an attachment to the attachments line"},
		{"another seat's link", strings.Contains(events, "linked to "+theirs+" by drive"),
			"events should keep another seat's change, which no section names the seat of"},
	}

	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if !check.holds {
				t.Errorf("%s:\n%s", check.why, shown)
			}
		})
	}

	if !strings.Contains(view, "\nfiled: session ") || !strings.Contains(view, "anchors: README.md (drifted)") {
		t.Errorf("a view's as-bodies should print the block as show does:\n%s", view)
	}
}

func TestShowKeepsTheRetireOfARevivedEntry(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	id := h.file("Finding retired and revived", "a finding")
	h.must("", "retire", id, "--because", "too early")
	h.must("", "revive", id)

	if shown := h.must("", "show", id, "--again"); !strings.Contains(shown, "retired: too early by main") {
		t.Errorf("a retire no header names should stay among the events:\n%s", shown)
	}
}

func TestShowNamesAnOtherBranchOnce(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	h.git.SwitchBranch("feature")
	id := h.file("Finding filed on the feature branch", "a finding")
	h.git.SwitchBranch("main")

	shown := h.must("", "show", id, "--again")
	header, _, _ := strings.Cut(shown, "\n")

	if !strings.Contains(header, "other branch: feature") || strings.Contains(shown, "branch feature") {
		t.Errorf("an other branch should print once, in the header:\n%s", shown)
	}
}
