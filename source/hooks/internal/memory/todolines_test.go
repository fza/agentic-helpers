package memory_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestATodoFiledWithoutItemsIsANoteAndAStateLine(t *testing.T) {
	h := heldHarness(t)

	out := h.must("rescan after the release", "new", "--title", "Rescan transcripts for parse failures", "--topic", "memory",
		"--kind", "todo", "--until", "merged:release", "--due", "2027-01-01", "--about", "README.md")

	reply := regexp.MustCompile(`^filed (m-[a-z0-9]{6}) \(rescan-transcripts\) as a note and added state#([a-z0-9]{3}): ` +
		`a todo without items is a state line  due: 2027-01-01 00:00 \S+$`)

	found := reply.FindStringSubmatch(strings.Split(out, "\n")[0])
	if found == nil {
		t.Fatalf("filing a todo without items should say in one line that it filed a note and added a state line:\n%s", out)
	}

	id, line := found[1], found[2]

	shown := h.must("", "show", id)
	if !strings.Contains(shown, ", note,") || strings.Contains(shown, "until:") || strings.Contains(shown, "due:") {
		t.Errorf("the entry should be a note carrying no ends and no due time:\n%s", shown)
	}

	if !strings.Contains(shown, "anchors: README.md") || !strings.Contains(shown, "rescan after the release") {
		t.Errorf("the note should keep the todo's body and anchors:\n%s", shown)
	}

	state := h.must("", "show", "state")
	if !strings.Contains(state, "#"+line+": Rescan transcripts for parse failures ("+id+")  until merged:release  due in ") {
		t.Errorf("the state line should carry the todo's title, id, ends and due time:\n%s", state)
	}

	h.must("", "retire", id, "--because", "rescanned")

	if state := h.must("", "show", "state", "--all-items"); !strings.Contains(state, "[e] #"+line+":") {
		t.Errorf("the line should end once the note it cites is retired:\n%s", state)
	}
}

func TestATodoFiledOverMCPWithoutItemsIsANoteAndAStateLine(t *testing.T) {
	h := heldHarness(t)

	answer := h.mcp(`{"name":"new","arguments":{"title":"Rescan transcripts for parse failures","body":"b","topic":["memory"],"kind":"todo","until":["claim"]}}`)
	if !regexp.MustCompile(`filed m-[a-z0-9]{6} \(rescan-transcripts\) as a note and added state#[a-z0-9]{3}: a todo without items is a state line`).MatchString(answer) {
		t.Errorf("the MCP new tool should file a todo without items as a note and a state line: %s", answer)
	}

	if state := h.must("", "show", "state"); !strings.Contains(state, "Rescan transcripts for parse failures (m-") || !strings.Contains(state, "until claim") {
		t.Errorf("the line should carry the todo's ends:\n%s", state)
	}
}

func TestATodoFiledWithItemsStaysATodo(t *testing.T) {
	h := heldHarness(t)

	out := h.must("", "new", "--title", "Port the waiter to Linux", "--topic", "gate", "--kind", "todo", "--item", "build")
	if !regexp.MustCompile(`^filed m-[a-z0-9]{6} \(port-waiter\)\nitems: #[a-z0-9]{3}`).MatchString(out) {
		t.Errorf("a todo filed with items should be filed as a todo:\n%s", out)
	}

	if state := h.must("", "show", "state"); strings.TrimSpace(state) != "state: empty" {
		t.Errorf("a todo filed with items should add no state line:\n%s", state)
	}
}

// itemlessTodo is a todo left without items, as a store of format 10 could
// hold one: filed with an item that is then removed.
func itemlessTodo(h *harness, title string, extra ...string) string {
	h.t.Helper()

	id := h.file(title, "body", append([]string{"--kind", "todo", "--item", "step"}, extra...)...)

	item := regexp.MustCompile(`#([a-z0-9]{3}):`).FindStringSubmatch(h.must("", "show", id))
	if item == nil {
		h.t.Fatalf("the todo should list its item")
	}

	h.must("", "ack", id+"#"+item[1], "--remove")

	return id
}

func TestAFormat10StoreTurnsItsTodosWithoutItemsIntoStateLines(t *testing.T) {
	h := heldHarness(t)

	ended := itemlessTodo(h, "Rescan transcripts for parse failures", "--until", "merged:release", "--due", "2027-01-01")
	carried := itemlessTodo(h, "Measure the gate after the fix")
	kept := h.file("Port the waiter to Linux", "body", "--kind", "todo", "--item", "build")
	retired := itemlessTodo(h, "Rerun the gate benchmark")
	h.must("", "retire", retired, "--because", "rerun")

	// A run killed after the line landed and before the rekind left the line.
	h.addLine("Measure the gate after the fix (" + carried + ")")

	formatPath := filepath.Join(h.store(), "format.json")
	test.WriteFile(t, formatPath, `{"format":10}`+"\n")

	state := h.must("", "show", "state")

	if written, _ := os.ReadFile(formatPath); !strings.Contains(string(written), `"format":11`) {
		t.Errorf("the store should move to the current format: %s", written)
	}

	if !regexp.MustCompile(`#[a-z0-9]{3}: Rescan transcripts for parse failures \(` + ended + `\)  until merged:release  due in `).MatchString(state) {
		t.Errorf("a todo without items should get a state line carrying its ends and due time:\n%s", state)
	}

	if strings.Count(state, "Measure the gate after the fix ("+carried+")") != 1 {
		t.Errorf("a todo whose line its seat already holds should get no second line:\n%s", state)
	}

	for _, id := range []string{ended, carried} {
		shown := h.must("", "show", id)
		if !strings.Contains(shown, ", note,") || strings.Contains(shown, "until:") || strings.Contains(shown, "due:") {
			t.Errorf("a todo without items should become a note carrying no ends and no due time:\n%s", shown)
		}
	}

	if shown := h.must("", "show", kept); !strings.Contains(shown, ", todo,") {
		t.Errorf("a todo with items should stay a todo:\n%s", shown)
	}

	if shown := h.must("", "show", retired); !strings.Contains(shown, ", todo,") || strings.Contains(state, "Rerun the gate benchmark") {
		t.Errorf("an ended todo should be left as it is, with no line:\n%s\n%s", shown, state)
	}

	test.WriteFile(t, formatPath, `{"format":10}`+"\n")

	if again := h.must("", "show", "state"); again != state {
		t.Errorf("running the step again should change nothing:\n%s\nfirst run:\n%s", again, state)
	}
}
