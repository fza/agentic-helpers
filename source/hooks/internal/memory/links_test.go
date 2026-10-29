package memory_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/sddcaps"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const (
	closedDecision = "20261025-162357-d-tac-427"
	itsCloser      = "20261026-101500-s-tac-a1b"
	activeDecision = "20261025-100000-d-tac-act"
)

// withGraph gives the harness's checkout the graph entries the export fixture
// names, and the export itself.
func withGraph(t *testing.T, h *harness) {
	t.Helper()

	for _, path := range []string{"2026/10/25-162357-d-tac-427.md", "2026/10/26-101500-s-tac-a1b.md", "2026/10/25-100000-d-tac-act.md"} {
		test.WriteFile(t, filepath.Join(h.repo, ".sdd", "graph", filepath.FromSlash(path)), "---\ntype: decision\n---\n")
	}

	h.graph.Err, h.graph.Out = nil, test.Fixture(t, "memory/sdd-export.json")
}

func TestTheTextLinksWhatResolves(t *testing.T) {
	h := newHarness(t)
	withGraph(t, h)
	h.must("", "claim", "main")
	earlier := h.file("Waiter checked on the CI runner", "an earlier finding")

	out := h.must("see "+closedDecision+" and "+earlier+", not d-tac-427 nor m-zzzz99", "new", "--title", "Waiter flaky on CI again", "--topic", "gate")
	id := idPattern.FindString(out)

	if !strings.Contains(out, "not linked (no such entry): m-zzzz99") || strings.Contains(out, "d-tac-427,") {
		t.Errorf("the reply should name the full id that resolved to nothing, and only it, got:\n%s", out)
	}

	shown := h.must("", "show", id)
	if !strings.Contains(shown, "links:\n  → "+closedDecision+" closed by "+itsCloser+"\n  → "+earlier+" ") {
		t.Errorf("a full graph id and a memory id named in the text should link the entry, got:\n%s", shown)
	}

	if strings.Contains(shown, "→ m-zzzz99") || strings.Contains(shown, "→ d-tac-427") {
		t.Errorf("neither a short graph id nor an unknown id should link, got:\n%s", shown)
	}
}

func TestAnExplicitLinkMustResolve(t *testing.T) {
	h := newHarness(t)
	withGraph(t, h)
	h.must("", "claim", "main")

	for _, target := range []string{"m-zzzz99", "d-tac-427"} {
		got := h.run("body", "new", "--title", "A finding linking nowhere", "--topic", "gate", "--link", target)
		if got.code != 1 || !strings.Contains(got.err, "agentic-memory: "+target+": no such entry") {
			t.Errorf("--link %s should refuse the filing, got %d %q", target, got.code, got.err)
		}
	}

	if out := h.must("", "search", "--term", "linking nowhere"); strings.Contains(out, "m-") {
		t.Errorf("a refused filing should file nothing, got:\n%s", out)
	}

	answer := h.mcp(`{"name":"new","arguments":{"title":"A finding linking nowhere","body":"b","topic":["gate"],"link":["m-zzzz99"]}}`)
	if !strings.Contains(answer, `m-zzzz99: no such entry`) || !strings.Contains(answer, `"isError":true`) {
		t.Errorf("MCP new with an unresolved link should refuse the same way, got:\n%s", answer)
	}

	id := h.file("A finding linking somewhere", "body", "--link", activeDecision)
	if shown := h.must("", "show", id); !strings.Contains(shown, "links:\n  → "+activeDecision) {
		t.Errorf("--link should link to a resolving id, got:\n%s", shown)
	}
}

func TestAnySeatLinksAndUnlinks(t *testing.T) {
	h, other := twoSeats(t)
	withGraph(t, h)
	id := h.file("Waiter flaky on CI", "a finding")

	other.must("", "link", id, activeDecision)

	if brief := h.must("", "brief"); !strings.Contains(brief, id+" linked to "+activeDecision+" by idea") {
		t.Errorf("the owner's brief should list another seat's link, got:\n%s", brief)
	}

	other.must("", "unlink", id, activeDecision)

	if brief := h.must("", "brief"); !strings.Contains(brief, id+" unlinked from "+activeDecision+" by idea") {
		t.Errorf("the owner's brief should list another seat's unlink, got:\n%s", brief)
	}

	texted := h.file("Follow-up on the waiter", "follows "+closedDecision)

	got := other.run("", "unlink", texted, closedDecision)
	if got.code != 1 || !strings.Contains(got.err, "agentic-memory: "+closedDecision+" is named in "+texted+"'s text; supersede the entry instead") {
		t.Errorf("a link taken from the text should not be unlinked, got %d %q", got.code, got.err)
	}
}

func TestALineSaysWhatBecameOfItsLinks(t *testing.T) {
	h := newHarness(t)
	withGraph(t, h)
	h.must("", "claim", "main")

	old := h.file("The first take on the waiter", "a finding")
	successor := h.file("The second take on the waiter", "a better finding")
	h.must("", "supersede", old, "--by", successor)

	hub := h.file("Waiter flaky on CI", "see "+closedDecision+" and "+activeDecision+" and "+old)
	h.file("Retry once, then flag", "follows "+hub)
	h.file("Retry twice did not help", "follows "+hub)

	out := h.must("", "show", hub)
	first := strings.SplitN(out, "\n", 2)[0]

	for _, want := range []string{"→ " + closedDecision + " closed by " + itsCloser, "→ " + old + " superseded by " + successor, "← 2"} {
		if !strings.Contains(first, want) {
			t.Errorf("the line should carry %q, got:\n%s", want, first)
		}
	}

	if strings.Contains(first, "→ "+activeDecision) {
		t.Errorf("an active target should carry no flag, got:\n%s", first)
	}

	section := out[strings.Index(out, "linked from:"):]
	if strings.Count(section, "\n  m-") != 2 {
		t.Errorf("show should list each entry linking in, got:\n%s", section)
	}
}

const (
	settledDecision    = "20261024-110000-d-tac-set"
	supersededDecision = "20261024-120000-d-tac-sup"
	itsSuccessor       = "20261027-090000-d-tac-new"
)

// withEndedGraph is withGraph plus a settled decision and a superseded one
// with its successor.
func withEndedGraph(t *testing.T, h *harness) {
	t.Helper()
	withGraph(t, h)

	for _, path := range []string{"2026/10/24-110000-d-tac-set.md", "2026/10/24-120000-d-tac-sup.md", "2026/10/27-090000-d-tac-new.md"} {
		test.WriteFile(t, filepath.Join(h.repo, ".sdd", "graph", filepath.FromSlash(path)), "---\ntype: decision\n---\n")
	}

	h.graph.Out = test.Fixture(t, "memory/sdd-export-fold.json")
}

func TestTheShortLineFoldsEndedLinks(t *testing.T) {
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

	got := briefLine(h.must("", "brief"), hub)
	want := "  → " + settledDecision + " settled; → 2 ended, 1 superseded by " + successor + ", 1 superseded by " + itsSuccessor
	if !strings.HasSuffix(got, want) {
		t.Errorf("the brief line should list settled links and fold the ended ones keeping each successor, got:\n%s", got)
	}

	for _, full := range []string{h.must("", "show", hub), h.must("", "search", "--term", "flaky")} {
		first := strings.SplitN(full, "\n", 2)[0]

		for _, flag := range []string{"→ " + closedDecision + " closed by " + itsCloser, "→ " + retired + " retired",
			"→ " + settledDecision + " settled", "→ " + old + " superseded by " + successor,
			"→ " + supersededDecision + " superseded by " + itsSuccessor} {
			if !strings.Contains(first, flag) {
				t.Errorf("show and search should keep every link flag in full, got:\n%s", first)
			}
		}
	}
}

func TestTheBriefNamesTheNewerEntriesCitingAHit(t *testing.T) {
	h, other := twoSeats(t)
	hub := h.file("Waiter flaky on CI", "the hub")

	var citing []string

	for _, title := range []string{"Oldest note citing the hub", "Middle note citing the hub", "Newest note citing the hub"} {
		h.clock.Advance(time.Minute)
		citing = append(citing, other.file(title, "follows "+hub))
	}

	h.clock.Advance(time.Minute)
	received := other.file("A received note citing the hub", "follows "+hub, "--for", "main")

	brief := h.must("", "brief")
	under := brief[strings.Index(brief, hub):]
	under = under[:strings.Index(under, "\n  m-")+1]

	if !strings.Contains(under, "← "+citing[2]+" Newest note citing the hub [idea, ") ||
		!strings.Contains(under, "← "+citing[1]+" Middle note citing the hub [idea, ") ||
		strings.Contains(under, citing[0]) || strings.Contains(under, "← "+received) {
		t.Errorf("a hit should show the two newest citing entries not already listed, title only, got:\n%s", under)
	}
}

func TestAnOlderEntryLinkedLaterIsNotNamedUnderTheHit(t *testing.T) {
	h, other := twoSeats(t)
	older := other.file("An older note linked later", "filed before the hub")
	h.clock.Advance(time.Minute)
	hub := h.file("Waiter flaky on CI", "the hub")
	other.must("", "link", older, hub)

	if brief := h.must("", "brief"); strings.Contains(brief, "← "+older) {
		t.Errorf("only entries filed after the hit should be named under it, got:\n%s", brief)
	}
}

func TestGraphStatusIsCachedUntilTheGraphChanges(t *testing.T) {
	h := newHarness(t)
	withGraph(t, h)
	h.must("", "claim", "main")
	h.file("Waiter flaky on CI", "see "+closedDecision)

	h.must("", "search", "--term", "Waiter")
	h.must("", "search", "--term", "Waiter")

	if h.graph.Calls() != 1 {
		t.Errorf("sdd export should run once while the graph is unchanged, ran %d times", h.graph.Calls())
	}

	later := time.Now().Add(time.Hour)
	path := filepath.Join(h.repo, ".sdd", "graph", "2026", "10", "26-101500-s-tac-a1b.md")

	err := os.Chtimes(path, later, later)
	if err != nil {
		t.Fatal(err)
	}

	h.must("", "search", "--term", "Waiter")

	if h.graph.Calls() != 2 {
		t.Errorf("sdd export should run again once a graph file changes, ran %d times", h.graph.Calls())
	}
}

func TestWithoutAnExportLinksFallBackToExistence(t *testing.T) {
	h := newHarness(t)
	withGraph(t, h)
	h.graph.Err = errors.New("exit status 1 unknown command export")
	h.must("", "claim", "main")

	h.run("see "+closedDecision+" and "+activeDecision, "new", "--title", "Waiter flaky on CI", "--topic", "gate")

	filed := h.run("see "+closedDecision, "new", "--title", "Waiter flaky on CI again", "--topic", "gate")
	if strings.Count(filed.err, "graph status unavailable") != 1 {
		t.Errorf("a filing reading the graph twice should say the export failed once, got %q", filed.err)
	}

	err := os.Remove(filepath.Join(h.repo, ".sdd", "graph", "2026", "10", "25-100000-d-tac-act.md"))
	if err != nil {
		t.Fatal(err)
	}

	got := h.run("", "search", "--term", "Waiter")
	if strings.Count(got.err, "agentic-memory: graph status unavailable (sdd export: exit status 1 unknown command export)") != 1 {
		t.Errorf("a failed export should be said once per read, got %q", got.err)
	}

	if strings.Contains(got.out, "closed by") || !strings.Contains(got.out, "→ "+activeDecision+" gone") {
		t.Errorf("without the export only a missing graph file should flag, as gone, got:\n%s", got.out)
	}
}

func TestTheSimilarHintIsOneLine(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	old := h.file("Gate reads Bash only today", "the gate reads bash only")

	out := h.must("the gate reads bash only today", "new", "--title", "Gate reads Bash only still", "--topic", "gate")

	if !strings.Contains(out, "similar: "+old+" Gate reads Bash only today (") {
		t.Errorf("the filing should name the similar entry, got:\n%s", out)
	}

	if strings.Contains(out, "supersede") || strings.Contains(out, " link ") {
		t.Errorf("the similar-entry hint should spell out no command, got:\n%s", out)
	}
}

func TestRecallNamesTheNewerEntriesCitingAHit(t *testing.T) {
	h, other := twoSeats(t)
	hub := h.file("Zebra quokka narwhal sighting", "zebra quokka narwhal")

	h.clock.Advance(time.Minute)
	cited := other.file("Follow-up on the sighting", "follows "+hub)

	for _, word := range unrelated[:18] {
		h.file("Unrelated finding about "+word, word+" "+word+" "+word)
	}

	out := h.must(hookPayload("", "", "zebra quokka narwhal"), "prompt")
	if !strings.Contains(out, hub) || !strings.Contains(out, "    ← "+cited+" Follow-up on the sighting [idea, ") {
		t.Errorf("a recalled hit should name the newer entry citing it, got:\n%s", out)
	}
}

func TestAnSddWithoutExportFlagsLinksByFile(t *testing.T) {
	for _, tc := range []struct {
		name    string
		runner  *test.Runner
		exports bool
	}{
		{name: "stock sdd from main", runner: &test.Runner{Stock: true}, exports: true},
		{name: "a stock release", runner: &test.Runner{Stock: true, Unreleased: true}},
		{name: "no sdd", runner: &test.Runner{Missing: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			withGraph(t, h)
			h.sdd = &sddcaps.Probe{Runner: tc.runner}
			h.must("", "claim", "main")

			id := h.file("A finding linking a closed decision", "body", "--link", closedDecision)
			got := h.run("", "show", id)

			if tc.exports {
				if h.graph.Calls() == 0 || !strings.Contains(got.out, closedDecision+" closed by") {
					t.Errorf("an sdd with export should flag the link by its status, got:\n%s", got.out)
				}

				return
			}

			if h.graph.Calls() != 0 {
				t.Error("an sdd without export should never be asked for one")
			}

			if strings.Contains(got.out, "closed by") || strings.Contains(got.err, "graph status unavailable") {
				t.Errorf("the link should be flagged by its file alone, quietly, got:\n%s\n%s", got.out, got.err)
			}
		})
	}
}
