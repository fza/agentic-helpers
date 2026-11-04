package memory_test

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestAStateLineCarriesItsDueTime(t *testing.T) {
	h := heldHarness(t)

	added := h.must("", "ack", "state", "--add", "Rescan the transcripts after the release", "--until", "none", "--due", "2027-01-01")
	if !regexp.MustCompile(`^added state#[a-z0-9]{3}  due: 2027-01-01 00:00 \S+$`).MatchString(strings.TrimSpace(added)) {
		t.Errorf("an added line should name its due time in its normal form: %s", added)
	}

	id := lineAddress.FindStringSubmatch(added)[1]

	if shown := h.must("", "show", "state"); !strings.Contains(shown, "#"+id+": Rescan the transcripts after the release  due in ") {
		t.Errorf("show state should say when the line comes due:\n%s", shown)
	}

	if listed := h.must("", "show", "state", "--json"); !strings.Contains(listed, `"due":"20`) {
		t.Errorf("show state --json should carry the line's due time:\n%s", listed)
	}

	state := stateOf(h.must("", "brief"))
	if strings.Contains(state, "#"+id+":") || !strings.Contains(state, "(1 due later, next 2027-01-01") {
		t.Errorf("the brief should hold a line due later back in a fold naming the next due time:\n%s", state)
	}
}

func TestAStateLineNearingItsDueTimeReturnsToTheBrief(t *testing.T) {
	h := heldHarness(t)
	id := h.addLine("Rescan the transcripts after the release", "--due", dueAt(h, 3*time.Hour))

	if state := stateOf(h.must("", "brief")); !strings.Contains(state, "#"+id+": Rescan the transcripts after the release  due in 3h") {
		t.Errorf("a line due within a day should stand in the brief with its due time:\n%s", state)
	}
}

func TestAStateLineComingDueIsToldOnce(t *testing.T) {
	h := heldHarness(t)
	id := h.addLine("Rescan the transcripts after the release", "--due", dueAt(h, time.Hour))

	h.must(h.toolCall(), "tool")

	if out := h.must(h.toolCall(), "tool"); strings.Contains(out, "due for") {
		t.Errorf("a line not yet due should be told of nothing:\n%s", out)
	}

	h.clock.Advance(2 * time.Hour)

	if out := h.must(h.toolCall(), "tool"); !strings.Contains(out, "agentic-memory: due for main (1):") ||
		!strings.Contains(out, "state#"+id+": Rescan the transcripts after the release  due 1h ago") {
		t.Errorf("the first tool call after the line came due should tell of it:\n%s", out)
	}

	if out := h.must(h.toolCall(), "tool"); strings.Contains(out, "due for") {
		t.Errorf("a line should be told of once:\n%s", out)
	}

	h.addLine("Read the rescan's output")

	if out := h.must(h.toolCall(), "tool"); strings.Contains(out, "due for") {
		t.Errorf("a line told of should stay told once the log moves:\n%s", out)
	}

	h.must("", "ack", "state#"+id, "--done")

	if shown := h.must("", "show", "state", "--all-items"); strings.Contains(shown, "due 1h ago") {
		t.Errorf("a ticked line should no longer say it is due:\n%s", shown)
	}
}

func TestDueTakesStateLinesBeingAddedOnly(t *testing.T) {
	h := heldHarness(t)
	id := h.addLine("Rescan the transcripts after the release")
	todo := h.file("Port the waiter to Linux", "body", "--kind", "todo", "--item", "build")

	for _, args := range [][]string{
		{"ack", "state#" + id, "--done", "--due", "2027-01-01"},
		{"ack", todo, "--add", "test", "--due", "2027-01-01"},
	} {
		failed := h.run("", args...)
		if failed.code == 0 || !strings.Contains(failed.out+failed.err, "--due sets when the lines ack state --add adds come due") {
			t.Errorf("--due should be refused outside ack state --add, got %d %s%s", failed.code, failed.out, failed.err)
		}
	}
}
