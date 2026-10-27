package memory_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

type returned struct {
	Reason    string   `json:"reason"`
	Detail    string   `json:"detail"`
	Verbs     []string `json:"verbs"`
	Answer    string   `json:"answer"`
	Unchecked bool     `json:"unchecked"`
}

// returnsByTitle is every entry a returns layout lists, by title, with its
// reasons.
func returnsByTitle(t *testing.T, h *harness, text string) map[string][]returned {
	t.Helper()

	var answer struct {
		Sections map[string]struct {
			Entries []struct {
				Title   string     `json:"title"`
				Returns []returned `json:"returns"`
			} `json:"entries"`
		} `json:"sections"`
	}

	err := json.Unmarshal([]byte(h.must("", "view", "--layout", text, "--json")), &answer)
	if err != nil {
		t.Fatalf("view --json should answer one object: %v", err)
	}

	found := map[string][]returned{}

	for _, section := range answer.Sections {
		for _, listed := range section.Entries {
			found[listed.Title] = listed.Returns
		}
	}

	return found
}

func TestReturnsReportsEachReason(t *testing.T) {
	h := newHarness(t)
	withGraph(t, h)
	h.must("", "claim", "main")

	h.file("Gate reads only Bash calls", "the gate reads internal/gate/gate.go only", "--kind", "trap")
	h.file("Readme names the build steps", "the README.md lists them")
	h.file("Finding citing a closed decision", "see the decision", "--link", closedDecision)
	h.must("follow it", "new", "--title", "Rule citing a closed decision", "--topic", "grants", "--kind", "rule",
		"--from", "user", "--link", closedDecision)
	replaced := h.file("Waiter polls every five seconds", "an older finding")
	replacing := h.file("Waiter polls every second now", "the newer finding")
	h.file("Finding citing a replaced one", "see "+replaced)
	ended := h.file("Note that ended its run", "done")
	h.file("Finding citing an ended one", "see "+ended)
	h.file("Finding citing a vanished decision", "see the decision", "--link", activeDecision)
	h.file("Todo ending at half past nine", "a deadline", "--kind", "todo", "--until", "at:2026-10-25T09:30:00Z")
	h.git.SwitchBranch("feature")
	h.file("Finding filed on the feature branch", "only there")
	h.git.SwitchBranch("main")
	h.file("Finding that needs nothing at all", "fine as it stands")
	retiredReadme := h.file("Retired note naming the readme", "the README.md said so")
	h.must("", "retire", retiredReadme)

	h.must("", "supersede", replaced, "--by", replacing)
	h.must("", "retire", ended)
	test.WriteFile(t, filepath.Join(h.repo, "internal", "gate", "gate.go"), "package gate\n\nfunc Changed() {}\n")

	for _, gone := range []string{filepath.Join(h.repo, "README.md"),
		filepath.Join(h.repo, ".sdd", "graph", "2026", "10", "25-100000-d-tac-act.md")} {
		err := os.Remove(gone)
		if err != nil {
			t.Fatal(err)
		}
	}

	h.clock.Advance(time.Hour)

	got := returnsByTitle(t, h, "returns:as-list")

	cases := []struct {
		title string
		want  []returned
	}{
		{title: "Gate reads only Bash calls", want: []returned{{Reason: "anchor", Detail: "drifted: internal/gate/gate.go",
			Verbs: []string{"confirm", "supersede"}, Answer: "confirm where the entry still holds, else supersede"}}},
		{title: "Readme names the build steps", want: []returned{
			{Reason: "anchor", Detail: "gone: README.md", Verbs: []string{"supersede", "retire"}, Answer: "supersede or retire"},
			{Reason: "until", Detail: "end inferred: every anchor gone", Verbs: []string{"retire"}, Answer: "retire"}}},
		{title: "Finding citing a closed decision", want: []returned{
			{Reason: "link", Detail: closedDecision + " closed by " + itsCloser, Verbs: []string{"supersede", "retire"},
				Answer: "read " + itsCloser + ", then supersede or retire"},
			{Reason: "until", Detail: "end inferred: every link ended", Verbs: []string{"retire"}, Answer: "retire"}}},
		{title: "Rule citing a closed decision", want: []returned{
			{Reason: "link", Detail: closedDecision + " closed by " + itsCloser, Verbs: []string{"supersede", "retire"},
				Answer: "read " + itsCloser + ", then supersede or retire"}}},
		{title: "Finding citing a replaced one", want: []returned{
			{Reason: "link", Detail: replaced + " superseded by " + replacing, Verbs: []string{"supersede", "retire"},
				Answer: "read " + replacing + ", then supersede or retire"},
			{Reason: "until", Detail: "end inferred: every link ended", Verbs: []string{"retire"}, Answer: "retire"}}},
		{title: "Finding citing an ended one", want: []returned{
			{Reason: "link", Detail: ended + " retired", Verbs: []string{"supersede", "retire"}, Answer: "supersede or retire"},
			{Reason: "until", Detail: "end inferred: every link ended", Verbs: []string{"retire"}, Answer: "retire"}}},
		{title: "Finding citing a vanished decision", want: []returned{
			{Reason: "link", Detail: activeDecision + " gone", Verbs: []string{"unlink"}, Answer: "unlink"},
			{Reason: "until", Detail: "end inferred: every link ended", Verbs: []string{"retire"}, Answer: "retire"}}},
		{title: "Todo ending at half past nine", want: []returned{{Reason: "until",
			Detail: "end met: at:2026-10-25T09:30:00Z", Verbs: []string{"retire"}, Answer: "retire"}}},
		{title: "Finding filed on the feature branch", want: []returned{{Reason: "branch", Detail: "filed on feature",
			Verbs: []string{}, Answer: "none until feature merges"}}},
	}

	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			if !slices.EqualFunc(got[tc.title], tc.want, func(a, b returned) bool {
				return a.Reason == b.Reason && a.Detail == b.Detail && slices.Equal(a.Verbs, b.Verbs) && a.Answer == b.Answer &&
					a.Unchecked == b.Unchecked
			}) {
				t.Errorf("returns should give each reason with the verbs that answer it, got %+v", got[tc.title])
			}
		})
	}

	for _, quiet := range []string{"Finding that needs nothing at all", "Note that ended its run", "Waiter polls every second now",
		"Retired note naming the readme"} {
		if _, listed := got[quiet]; listed {
			t.Errorf("returns should list only live entries needing attention, listed %q", quiet)
		}
	}

	anchors := returnsByTitle(t, h, "returns(anchor):as-list")
	if len(anchors) != 2 || len(anchors["Readme names the build steps"]) != 1 {
		t.Errorf("returns(anchor) should keep and show the anchor reasons alone, got %+v", anchors)
	}

	text := h.must("", "view", "--layout", "returns(branch):as-list")
	if !strings.Contains(text, " Finding filed on the feature branch  [") || !strings.Contains(text,
		"\n    branch filed on feature → none until feature merges") {
		t.Errorf("the text should name each reason under its entry, got:\n%s", text)
	}
}

func TestReturnsShowsWhatCannotBeCheckedAsUnchecked(t *testing.T) {
	h := newHarness(t)
	test.WriteFile(t, filepath.Join(h.repo, ".sdd", "graph", "2026", "10", "25-100000-d-tac-act.md"), "---\ntype: decision\n---\n")
	test.WriteFile(t, filepath.Join(h.repo, "secret", "plan.txt"), "the plan\n")
	h.must("", "claim", "main")

	h.file("Finding citing a decision", "see the decision", "--link", activeDecision)
	h.file("Plan names its steps", "the secret/plan.txt holds them", "--kind", "todo", "--until", "changed:secret/plan.txt")
	h.file("Todo ending when the decision closes", "wait for it", "--kind", "todo", "--until", "closed:"+activeDecision)

	locked := filepath.Join(h.repo, "secret", "plan.txt")

	err := os.Chmod(locked, 0)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.Chmod(locked, 0o600) })

	got := returnsByTitle(t, h, "returns:as-list")

	if !slices.ContainsFunc(got["Finding citing a decision"], func(reason returned) bool {
		return reason.Reason == "link" && reason.Unchecked && reason.Detail == activeDecision+": the graph status cache is cold"
	}) {
		t.Errorf("a graph link with no cached status should read unchecked, never absent, got %+v", got)
	}

	if !slices.ContainsFunc(got["Todo ending when the decision closes"], func(reason returned) bool {
		return reason.Reason == "until" && reason.Unchecked && reason.Detail == "closed:"+activeDecision+": the graph status cache is cold"
	}) {
		t.Errorf("an end on a graph entry with no cached status should read unchecked, got %+v", got)
	}

	plan := got["Plan names its steps"]
	if !slices.ContainsFunc(plan, func(reason returned) bool {
		return reason.Reason == "anchor" && reason.Unchecked && reason.Detail == "secret/plan.txt cannot be read"
	}) || !slices.ContainsFunc(plan, func(reason returned) bool {
		return reason.Reason == "until" && reason.Unchecked && reason.Detail == "changed:secret/plan.txt: the file cannot be read"
	}) || slices.ContainsFunc(plan, func(reason returned) bool { return strings.HasPrefix(reason.Detail, "end met") }) {
		t.Errorf("a file that cannot be read should leave its anchor and its end unchecked, got %+v", plan)
	}

	text := h.must("", "view", "--layout", "returns(link):as-list")
	if !strings.Contains(text, "    link unchecked: "+activeDecision+": the graph status cache is cold") {
		t.Errorf("the text should say unchecked under the entry, got:\n%s", text)
	}
}

func TestReturnsTakesOnlyItsReasons(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	got := h.run("", "view", "--layout", "returns(anchors):as-list")
	if got.code != 1 || !strings.Contains(got.err,
		`layout: returns takes anchor, link, until or branch: "anchors" at position 9; nearest valid word: anchor`) {
		t.Errorf("returns should refuse a reason it does not know, got %d %q", got.code, got.err)
	}
}
