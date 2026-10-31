package memory_test

import (
	"strings"
	"testing"
	"time"
)

// linesHolding is every line of out holding text.
func linesHolding(out string, text string) []string {
	var held []string

	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, text) {
			held = append(held, line)
		}
	}

	return held
}

func TestTheBriefNamesACitersSeatOnlyWhereItIsAnotherAndMarksAnEndedOne(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	cited := h.file("Gate note others cite", "the cited finding")
	h.clock.Advance(time.Minute)
	own := h.file("Gate follow-up of the seat", "follows up", "--link", cited)
	h.must("", "retire", own, "--because", "folded")

	other := newHarnessSharing(h)
	other.must("", "claim", "drive")
	other.clock.Advance(2 * time.Minute)
	foreign := other.file("Drive follow-up citing the note", "follows up too", "--link", cited)

	brief := h.must("", "brief", "--again")

	ownLines := linesHolding(brief, "← "+own+" ")
	if len(ownLines) != 1 || strings.Contains(ownLines[0], "main") || !localStamp.MatchString(ownLines[0]) {
		t.Errorf("an own citer should keep its date and leave out the reader's seat:\n%s", brief)
	}

	if !strings.HasSuffix(ownLines[0], "] (retired)") {
		t.Errorf("a retired citer should say it ended:\n%s", brief)
	}

	foreignLines := linesHolding(brief, "← "+foreign+" ")
	if len(foreignLines) != 1 || !strings.Contains(foreignLines[0], "[drive, ") || strings.HasSuffix(foreignLines[0], ")") {
		t.Errorf("a live citer of another seat should name its seat and no status:\n%s", brief)
	}
}

func TestTheBriefLeavesTheRulesTheDoNowListNamedOutOfItsRules(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	owed := h.file("Read the board on claim", "read it", "--kind", "rule", "--from", "user", "--on", "claim")
	standing := h.file("Commit only when asked", "ask first", "--kind", "rule", "--from", "user")
	h.must("", "release")

	out := h.must("", "claim", "main")
	if len(linesHolding(out, owed)) != 1 || !strings.Contains(linesHolding(out, owed)[0], "1. "+owed) {
		t.Errorf("a rule the do-now list names should stand there alone:\n%s", out)
	}

	if len(linesHolding(out, standing)) != 1 || !strings.Contains(out, "rules (from the user") {
		t.Errorf("a rule owed at no moment should stay in the rules section:\n%s", out)
	}
}

func TestTheBriefPrintsNoRulesHeadingWhereTheDoNowListNamedEveryRule(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("Read the board on claim", "read it", "--kind", "rule", "--from", "user", "--on", "claim")
	h.must("", "release")

	out := h.must("", "claim", "main")
	if !strings.Contains(out, "do now (claim):") || strings.Contains(out, "rules (from the user") {
		t.Errorf("a rules section left empty by the do-now list should print no heading:\n%s", out)
	}
}

func TestTheBriefListsAnEntryFiledForTheSeatOnce(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	other := newHarnessSharing(h)
	other.must("", "claim", "drive")
	other.clock.Advance(time.Minute)
	handed := other.file("Review owes the API diff", "a handover", "--for", "main")
	unrelated := other.file("Drive keeps its own staging notes", "its own")

	brief := h.must("", "brief", "--again")
	if len(linesHolding(brief, handed)) != 1 {
		t.Errorf("an entry a section printed should stay out of the news:\n%s", brief)
	}

	if len(linesHolding(brief, unrelated)) != 1 || !strings.Contains(brief, "new from other seats:") {
		t.Errorf("an entry of another seat no section printed should stay in the news:\n%s", brief)
	}
}

func TestTheBriefFoldsARunOfSeenEntries(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	trap := h.file("Gate trap of the seat", "a trap", "--kind", "trap")
	h.file("Commit only when asked", "ask first", "--kind", "rule", "--from", "user")
	h.file("Push only when asked", "ask first", "--kind", "rule", "--from", "user")
	notes := []string{
		h.file("Gate note about the alpha finding", "alpha"),
		h.file("Gate note about the bravo finding", "bravo"),
		h.file("Gate note about the charlie finding", "charlie"),
	}

	h.must("", "brief")
	fresh := h.file("Gate note about the delta finding", "delta")

	brief := h.must("", "brief")
	if !strings.Contains(brief, "[0m]\n  3 seen above, unchanged\ntopics") {
		t.Errorf("a run of seen notes should fold into one line:\n%s", brief)
	}

	if !strings.Contains(brief, "relying):\n  2 seen above, unchanged\n") {
		t.Errorf("a run of seen rules should fold into one line:\n%s", brief)
	}

	for _, id := range notes {
		if strings.Contains(brief, id) {
			t.Errorf("a folded note should not print its own line:\n%s", brief)
		}
	}

	if !strings.Contains(brief, "  "+trap+" Gate trap of the seat (seen)") {
		t.Errorf("a lone seen entry should keep its pointer:\n%s", brief)
	}

	if len(linesHolding(brief, fresh)) != 1 || strings.Contains(linesHolding(brief, fresh)[0], "(seen)") {
		t.Errorf("a fresh note should print in full beside the fold:\n%s", brief)
	}
}

func TestASeenEntryACiterFollowsKeepsItsPointerAndCiter(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	cited := h.file("Gate note others cite", "the cited finding")
	h.file("Gate note about the alpha finding", "alpha")

	other := newHarnessSharing(h)
	other.must("", "claim", "drive")
	other.clock.Advance(time.Minute)
	citer := other.file("Drive follow-up citing the note", "follows up", "--link", cited)

	h.must("", "brief")

	brief := h.must("", "brief")
	if !strings.Contains(brief, "  "+cited+" Gate note others cite (seen)\n    ← "+citer) {
		t.Errorf("a seen entry followed by a citer should keep its pointer above the citer:\n%s", brief)
	}
}
