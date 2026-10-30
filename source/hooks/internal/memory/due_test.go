package memory_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

// dueAt spells a due time the given span from the harness's held clock.
func dueAt(h *harness, from time.Duration) string {
	return h.clock.Now().Add(from).UTC().Format(time.RFC3339)
}

func heldHarness(t *testing.T) *harness {
	t.Helper()

	h := newHarness(t)
	h.clock.Hold()
	h.must("", "claim", "main")

	return h
}

// section is the lines under a brief heading, up to the next unindented line.
func section(brief string, heading string) []string {
	var (
		lines []string
		in    bool
	)

	for _, line := range strings.Split(brief, "\n") {
		switch {
		case strings.HasPrefix(line, heading):
			in = true
		case in && !strings.HasPrefix(line, "  "):
			return lines
		case in:
			lines = append(lines, line)
		}
	}

	return lines
}

func TestFilingADueTime(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		refused string
	}{
		{name: "an RFC 3339 time on a todo", args: []string{"--kind", "todo", "--due", "2026-10-27T15:00:00Z"}},
		{name: "a date on a note", args: []string{"--due", "2026-10-28"}},
		{name: "a malformed time names the forms", args: []string{"--due", "tomorrow"}, refused: "--due takes RFC 3339 or a date"},
		{name: "a rule takes none", args: []string{"--kind", "rule", "--from", "user", "--due", "2026-10-28"}, refused: "--due takes a todo or a note"},
		{name: "a trap takes none", args: []string{"--kind", "trap", "--due", "2026-10-28"}, refused: "--due takes a todo or a note"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := heldHarness(t)
			got := h.run("body", append([]string{"new", "--title", "Rerun the gate measurement", "--topic", "gate"}, tc.args...)...)

			if tc.refused != "" {
				if got.code != 1 || !strings.Contains(got.err, tc.refused) {
					t.Errorf("the filing should be refused naming why, got %d %q", got.code, got.err)
				}

				return
			}

			id := idPattern.FindString(got.out)
			if got.code != 0 || id == "" {
				t.Fatalf("the filing should land, got %d %q %q", got.code, got.out, got.err)
			}

			if shown := h.must("", "show", id); !strings.Contains(shown, "due: 2026-10-2") {
				t.Errorf("show should carry the due time:\n%s", shown)
			}
		})
	}
}

func TestFilingADueTimeOverMCP(t *testing.T) {
	h := heldHarness(t)

	out := h.mcp(`{"name":"new","arguments":{"title":"Rerun the gate measurement","topic":["gate"],"body":"b","kind":"trap","due":"2026-10-28"}}`)
	if !strings.Contains(out, "--due takes a todo or a note") {
		t.Errorf("a trap with a due time should be refused over MCP too:\n%s", out)
	}

	out = h.mcp(`{"name":"new","arguments":{"title":"Rerun the gate measurement","topic":["gate"],"body":"b","due":"soon"}}`)
	if !strings.Contains(out, "--due takes RFC 3339 or a date") {
		t.Errorf("a malformed time should be refused over MCP naming the forms:\n%s", out)
	}
}

func TestMovingAndClearingADueTime(t *testing.T) {
	h, other := twoSeats(t)
	h.clock.Hold()

	id := h.file("Rerun the gate measurement", "body", "--kind", "todo")
	trap := h.file("Gate trips on heredocs", "body", "--kind", "trap")

	if out := h.must("", "due", id, "2026-11-02"); !strings.HasPrefix(out, "due 2026-11-0") {
		t.Errorf("due should say the new time, got %q", out)
	}

	if shown := h.must("", "show", id, "--again"); !strings.Contains(shown, "due: 2026-11-0") {
		t.Errorf("show should carry the moved time:\n%s", shown)
	}

	h.must("", "due", id, "--clear")

	if shown := h.must("", "show", id, "--again"); strings.Contains(shown, "due: 2026") || !strings.Contains(shown, "due cleared") {
		t.Errorf("a cleared due time should leave the line and stand in the events:\n%s", shown)
	}

	for _, refused := range []struct {
		args []string
		says string
	}{
		{[]string{"due", id}, "due takes a time"},
		{[]string{"due", id, "2026-11-02", "--clear"}, "not both"},
		{[]string{"due", id, "next week"}, "--due takes RFC 3339 or a date"},
		{[]string{"due", trap, "2026-11-02"}, "--due takes a todo or a note"},
	} {
		if got := h.run("", refused.args...); got.code != 1 || !strings.Contains(got.err, refused.says) {
			t.Errorf("%v should be refused naming why, got %d %q", refused.args, got.code, got.err)
		}
	}

	if got := other.run("", "due", id, "2026-11-02"); got.code != 1 || !strings.Contains(got.err, "is on shelf main") {
		t.Errorf("a seat on another shelf should be refused, got %d %q", got.code, got.err)
	}

	out := h.mcp(`{"name":"due","arguments":{"id":"` + id + `","due":"2026-11-03"}}`)
	if !strings.Contains(out, `"structuredContent":{"message":"due 2026-11-0`) {
		t.Errorf("the due tool should move the time and answer it:\n%s", out)
	}
}

func TestAnyShelfMateMovesADueTime(t *testing.T) {
	h := heldHarness(t)
	id := h.file("Rerun the gate measurement", "body", "--kind", "todo")

	mate := newHarnessSharing(h)
	mate.must("", "claim", "main-2", "--shelf", "main")

	if got := mate.run("", "due", id, "2026-11-02"); got.code != 0 {
		t.Errorf("a seat on the entry's shelf should move its due time, got %d %q", got.code, got.err)
	}
}

func TestTheBriefsDueSection(t *testing.T) {
	h := heldHarness(t)

	plain := h.file("Gate reads Bash only", "body")
	past := h.file("Check the migration ran", "body", "--kind", "todo", "--due", dueAt(h, -50*time.Hour))
	soon := h.file("Rerun the gate measurement", "body", "--due", dueAt(h, 5*time.Hour))
	later := h.file("Review the release notes", "body", "--kind", "todo", "--due", dueAt(h, 72*time.Hour))
	latest := h.file("Prune the old seats", "body", "--due", dueAt(h, 96*time.Hour))

	brief := h.must("", "brief")

	due := section(brief, "due (2 of 2):")
	if len(due) != 2 || !strings.Contains(due[0], past) || !strings.Contains(due[0], "due 2d ago") ||
		!strings.Contains(due[1], soon) || !strings.Contains(due[1], "due in 5h") {
		t.Errorf("the due section should hold the entries due and due within a day, earliest first:\n%s", brief)
	}

	if !strings.Contains(brief, "2 due later (next 2026-10-28") {
		t.Errorf("the entries due later should be counted on one line, naming the next:\n%s", brief)
	}

	for _, id := range []string{past, soon, later, latest} {
		if strings.Count(brief, id) > 1 || strings.Contains(strings.Join(section(brief, "todos:"), "\n")+strings.Join(section(brief, "notes:"), "\n"), id) {
			t.Errorf("%s should stand in no kind section:\n%s", id, brief)
		}
	}

	if !strings.Contains(strings.Join(section(brief, "notes:"), "\n"), plain) {
		t.Errorf("an entry carrying no due time should stay in its kind section:\n%s", brief)
	}

	stateAt := strings.Index(brief, "state")
	dueAtLine := strings.Index(brief, "due (2 of 2):")

	if dueAtLine < 0 || (stateAt >= 0 && stateAt > dueAtLine) {
		t.Errorf("the due section should follow the state:\n%s", brief)
	}
}

func TestTheDueSectionIsCappedAndLeavesKindSectionsAlone(t *testing.T) {
	without := heldHarness(t)
	with := heldHarness(t)

	for _, title := range []string{"Gate reads Bash only", "Gate trips on heredocs", "Waiter polls every minute"} {
		without.file(title, "body")
		with.file(title, "body")
	}

	var due []string
	for index := range 7 {
		due = append(due, with.file("Due item number "+string(rune('a'+index)), "body", "--due", dueAt(with, time.Duration(7-index)*time.Hour)))
	}

	plainBrief := without.must("", "brief")
	dueBrief := with.must("", "brief")

	if got := section(dueBrief, "due (5 of 7):"); len(got) != 5 || !strings.Contains(dueBrief, "+2 more due: agentic-memory brief --due") {
		t.Errorf("the due section should show five and count the rest:\n%s", dueBrief)
	}

	strip := regexp.MustCompile(`m-[a-z0-9]+ `)
	if a, b := strip.ReplaceAllString(strings.Join(section(plainBrief, "notes:"), "\n"), ""),
		strip.ReplaceAllString(strings.Join(section(dueBrief, "notes:"), "\n"), ""); a != b {
		t.Errorf("due entries should not change a kind section:\n%s\n---\n%s", a, b)
	}

	listed := strings.TrimSpace(with.must("", "brief", "--due"))
	if strings.Count(listed, "\n")+1 != 7 || !strings.HasPrefix(listed, due[6]) {
		t.Errorf("brief --due should list every due entry, earliest first:\n%s", listed)
	}

}

func TestAnEntryComingDueIsToldOnce(t *testing.T) {
	h := heldHarness(t)
	id := h.file("Rerun the gate measurement", "body", "--due", dueAt(h, time.Hour))

	h.must(h.toolCall(), "tool")

	if out := h.must(h.toolCall(), "tool"); strings.Contains(out, "due for") {
		t.Errorf("an entry not yet due should be told of nothing:\n%s", out)
	}

	h.clock.Advance(2 * time.Hour)

	if out := h.must(h.toolCall(), "tool"); !strings.Contains(out, "agentic-memory: due for main (1):") || !strings.Contains(out, id) {
		t.Errorf("the first tool call after the entry came due should tell of it:\n%s", out)
	}

	if out := h.must(h.toolCall(), "tool"); strings.Contains(out, "due for") {
		t.Errorf("an entry should be told of once:\n%s", out)
	}

	h.must("", "due", id, dueAt(h, -time.Minute))

	if out := h.must(h.toolCall(), "tool"); !strings.Contains(out, "due for main (1):") {
		t.Errorf("a moved due time should be told of again:\n%s", out)
	}
}

func TestTheBriefTellsWhatIsAlreadyDue(t *testing.T) {
	h := heldHarness(t)
	id := h.file("Check the migration ran", "body", "--due", dueAt(h, -time.Hour))

	if brief := h.must("", "brief"); !strings.Contains(brief, id) {
		t.Fatalf("the brief should list the due entry:\n%s", brief)
	}

	if out := h.must(h.toolCall(), "tool"); strings.Contains(out, "due for") {
		t.Errorf("an entry the brief listed as due should not be told of again:\n%s", out)
	}
}

func TestTheWaiterWakesOnADueEntry(t *testing.T) {
	h := heldHarness(t)
	id := h.file("Rerun the gate measurement", "body", "--due", dueAt(h, time.Hour))
	h.must(h.toolCall(), "tool")

	var (
		group sync.WaitGroup
		got   ran
	)

	group.Go(func() { got = h.waiting(20*time.Millisecond, 10*time.Second).run(h.stop(), "wait") })

	time.Sleep(100 * time.Millisecond)
	h.clock.Advance(2 * time.Hour)

	group.Wait()

	if got.code != 2 || !strings.Contains(got.err, "agentic-memory: due for main (1):") || !strings.Contains(got.err, id) {
		t.Errorf("an entry coming due should wake the idle session, got %+v", got)
	}
}

func TestAStaleDueEntryIsFlagged(t *testing.T) {
	cases := []struct {
		name    string
		ago     time.Duration
		flagged bool
	}{
		{name: "seven days past due", ago: 7*24*time.Hour + time.Hour, flagged: true},
		{name: "six days past due", ago: 6 * 24 * time.Hour},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := heldHarness(t)
			id := h.file("Check the migration ran", "body", "--due", dueAt(h, -tc.ago))

			brief := h.must("", "brief")
			flagged := regexp.MustCompile(regexp.QuoteMeta(id) + `.*retire\? due 1w ago`).MatchString(brief)

			if flagged != tc.flagged {
				t.Errorf("an entry a week past due should be flagged retire?, and none earlier:\n%s", brief)
			}
		})
	}
}

func TestDueReadsCarryTheTime(t *testing.T) {
	h := heldHarness(t)
	due := h.file("Rerun the gate measurement", "body", "--due", "2026-10-30")
	plain := h.file("Gate reads Bash only", "body")

	view := h.must("", "view", "--layout", "due:as-list")
	if !strings.Contains(view, due) || strings.Contains(view, plain) {
		t.Errorf("view's due word should select the entries carrying a due time:\n%s", view)
	}

	if found := h.must("", "search", "--term", "measurement", "--no-heat", "--again"); !strings.Contains(found, "due: 2026-10-30") {
		t.Errorf("a search hit should carry the due time:\n%s", found)
	}

	var shown struct {
		Entries []struct {
			ID  string `json:"id"`
			Due string `json:"due"`
		} `json:"entries"`
	}

	if err := json.Unmarshal([]byte(h.must("", "show", due, "--json", "--again")), &shown); err != nil || len(shown.Entries) != 1 ||
		!strings.HasPrefix(shown.Entries[0].Due, "2026-10-2") && !strings.HasPrefix(shown.Entries[0].Due, "2026-10-30") {
		t.Errorf("the JSON entry should carry the due time, got %+v (%v)", shown, err)
	}
}

func TestAFormatSixStoreMovesToSevenWithABackup(t *testing.T) {
	h := heldHarness(t)
	h.file("Gate reads Bash only", "finding")

	formatPath := filepath.Join(h.store(), "format.json")
	test.WriteFile(t, formatPath, `{"format":6}`+"\n")

	h.must("", "search", "--term", "Gate", "--no-heat")

	mismatches := test.LedgerMismatches(t, h.store())
	if len(mismatches) > 0 {
		t.Errorf("the ledger should match the files right after the move: %s", strings.Join(mismatches, "; "))
	}

	h.must("", "search", "--term", "Gate", "--no-heat")

	if written, _ := os.ReadFile(formatPath); !strings.Contains(string(written), `"format":7`) {
		t.Errorf("a format-6 store should move to format 7: %s", written)
	}

	if backups, _ := filepath.Glob(filepath.Join(h.store(), "backup", "format-6-*", "log")); len(backups) != 1 {
		t.Errorf("the move should back the log up once, got %v", backups)
	}
}
