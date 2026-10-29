package memory_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

var itemsLine = regexp.MustCompile(`(?m)^items (m-[a-z0-9]+): (.+)$`)

// todo files a todo with items and returns its id and item ids in order.
func (h *harness) todo(title string, items ...string) (string, []string) {
	h.t.Helper()

	args := []string{"new", "--title", title, "--topic", "gate", "--kind", "todo"}
	for _, item := range items {
		args = append(args, "--item", item)
	}

	out := h.must("the work", args...)

	match := itemsLine.FindStringSubmatch(out)
	if match == nil {
		h.t.Fatalf("filing a todo with items should name them: %s", out)
	}

	return match[1], strings.Fields(strings.ReplaceAll(match[2], "#", ""))
}

func TestATodoFilesItsItemsWithDistinctIDs(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	id, items := h.todo("Port the waiter", "read the timer", "swap the clock", "run CI")
	if len(items) != 3 || items[0] == items[1] || items[1] == items[2] || items[0] == items[2] {
		t.Fatalf("each item should get its own id: %v", items)
	}

	for _, item := range items {
		if !regexp.MustCompile(`^[a-z0-9]{3}$`).MatchString(item) || strings.Trim(item, "0123456789") == "" {
			t.Errorf("an item id should be three base36 characters, never all digits: %q", item)
		}
	}

	shown := h.must("", "show", id, "--again")
	for index, text := range []string{"read the timer", "swap the clock", "run CI"} {
		if !strings.Contains(shown, "  "+string(rune('1'+index))+". [ ] #"+items[index]+": "+text) {
			t.Errorf("show should list every item in order, open: %s", shown)
		}
	}

	failed := h.run("x", "new", "--title", "A note with items", "--topic", "gate", "--item", "step")
	if failed.code != 1 || !strings.Contains(failed.err+failed.out, "only a todo holds") {
		t.Errorf("--item on another kind should be refused, got %d %s%s", failed.code, failed.out, failed.err)
	}

	answer := h.mcp(`{"name":"new","arguments":{"title":"Port the clock too","body":"x","topic":["gate"],"kind":"todo","item":["one","two"]}}`)
	if !strings.Contains(answer, "items m-") {
		t.Errorf("MCP new should file items as the command does: %s", answer)
	}
}

func TestAckChangesItems(t *testing.T) {
	cases := []struct {
		name  string
		args  func(id string, items []string) []string
		shows []string
		hides []string
	}{
		{name: "done", args: func(id string, items []string) []string { return []string{id + "#" + items[0], "--done"} },
			shows: []string{"1. [x] #"}},
		{name: "decline", args: func(id string, items []string) []string {
			return []string{id + "#" + items[1], "--decline", "not needed"}
		}, shows: []string{"2. [-] #", "dropped: not needed"}},
		{name: "remove", args: func(id string, items []string) []string { return []string{id + "#" + items[2], "--remove"} },
			hides: []string{"three"}},
		{name: "add", args: func(id string, _ []string) []string { return []string{id, "--add", "four"} },
			shows: []string{"4. [ ] #"}},
		{name: "order", args: func(id string, items []string) []string { return []string{id, "--order", items[2] + "," + items[1]} },
			shows: []string{"1. [ ] #%2: three", "2. [ ] #%1: two", "3. [ ] #%0: one"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")
			id, items := h.todo("Port the waiter", "one", "two", "three")

			h.must("", append([]string{"ack"}, tc.args(id, items)...)...)

			shown := h.must("", "show", id, "--again")
			for _, want := range tc.shows {
				for index, item := range items {
					want = strings.ReplaceAll(want, "%"+string(rune('0'+index)), item)
				}

				if !strings.Contains(shown, want) {
					t.Errorf("show should reflect the change %q:\n%s", want, shown)
				}
			}

			for _, hidden := range tc.hides {
				if strings.Contains(shown, hidden) {
					t.Errorf("a removed item should leave show:\n%s", shown)
				}

				if all := h.must("", "show", id, "--again", "--with-removed"); !strings.Contains(all, "[r] #"+items[2]+": "+hidden) {
					t.Errorf("show --with-removed should list a removed item:\n%s", all)
				}
			}
		})
	}

	h := newHarness(t)
	h.must("", "claim", "main")
	id, items := h.todo("Port the waiter", "one", "two")

	h.must("", "ack", id+"#"+items[0], "--done")
	h.must("", "ack", id+"#"+items[0], "--undo")

	if shown := h.must("", "show", id, "--again"); !strings.Contains(shown, "1. [ ] #"+items[0]) {
		t.Errorf("--undo should untick the item:\n%s", shown)
	}

	rule := h.must("", "new", "--title", "Owner allows commits", "--topic", "gate", "--kind", "rule", "--from", "user")
	ruleID := idPattern.FindString(rule)

	for _, refusal := range []struct {
		args []string
		says string
	}{
		{[]string{"ack", id + "#zzz", "--done"}, "no such item"},
		{[]string{"ack", id + "#2", "--done"}, "is a position; address an item by its id"},
		{[]string{"ack", ruleID + "#abc", "--done"}, "is a rule; items belong to a todo"},
		{[]string{"ack", id + "#" + items[0], "--done", "--undo"}, "takes exactly one of"},
		{[]string{"ack", id + "#" + items[0], "--add", "x"}, "take one todo id, no item address"},
		{[]string{"ack", id + "#" + items[0], "--order", items[1]}, "take one todo id, no item address"},
		{[]string{"ack", id, "--add", "x", "--order", items[0]}, "takes exactly one of"},
		{[]string{"ack", id + "#" + items[0]}, "takes exactly one of"},
		{[]string{"ack", id, "--order", "zzz"}, "no such item"},
		{[]string{"ack", ruleID, "--add", "x"}, "is a rule; items belong to a todo"},
	} {
		failed := h.run("", refusal.args...)
		if failed.code != 1 || !strings.Contains(failed.out+failed.err, refusal.says) {
			t.Errorf("%v should be refused saying %q, got %d %s%s", refusal.args, refusal.says, failed.code, failed.out, failed.err)
		}
	}

	if shown := h.must("", "show", id, "--again"); strings.Contains(shown, "[x]") {
		t.Errorf("a refused ack should change nothing:\n%s", shown)
	}
}

func TestALinkedItemReadsItsTarget(t *testing.T) {
	h := newHarness(t)
	withGraph(t, h)
	h.must("", "claim", "main")

	target := h.file("Waiter ported to Linux", "a finding")
	gone := "m-zzzz99"
	id, items := h.todo("Port the waiter", target, closedDecision, activeDecision, gone)

	shown := h.must("", "show", id, "--again")
	for _, want := range []string{
		"1. [ ] #" + items[0] + ": Waiter ported to Linux  → " + target,
		"2. [x] #" + items[1] + ": " + closedDecision + "  → " + closedDecision + " closed by " + itsCloser,
		"3. [ ] #" + items[2] + ": " + activeDecision + "  → " + activeDecision,
		"4. [ ] #" + items[3] + ": " + gone + "  → " + gone + " gone",
	} {
		if !strings.Contains(shown, want) {
			t.Errorf("a linked item should read its target's state, missing %q:\n%s", want, shown)
		}
	}

	successor := h.file("Waiter ported to Linux and BSD", "a later finding")
	h.must("", "supersede", target, "--by", successor)

	if shown := h.must("", "show", id, "--again"); !strings.Contains(shown, "1. [ ] #"+items[0]+": Waiter ported to Linux  → "+target+" → "+successor+"\n") {
		t.Errorf("a superseded target should move the item to its successor, open:\n%s", shown)
	}

	h.must("", "retire", successor, "--because", "done")

	if shown := h.must("", "show", id, "--again"); !strings.Contains(shown, "1. [x] #"+items[0]) {
		t.Errorf("a retired successor should tick the item:\n%s", shown)
	}

	h.must("", "revive", successor)

	if shown := h.must("", "show", id, "--again"); !strings.Contains(shown, "1. [ ] #"+items[0]) {
		t.Errorf("a target reopening should untick the item:\n%s", shown)
	}

	h.must("", "ack", id+"#"+items[1], "--decline", "moot")

	if shown := h.must("", "show", id, "--again"); !strings.Contains(shown, "2. [-] #"+items[1]) {
		t.Errorf("an explicit drop should win over a closed target:\n%s", shown)
	}

	h.must("", "ack", id+"#"+items[1], "--undo")

	if shown := h.must("", "show", id, "--again"); !strings.Contains(shown, "2. [x] #"+items[1]) {
		t.Errorf("--undo should give a linked item back to its target:\n%s", shown)
	}
}

func TestTheBriefShowsTheCountAndTheNextOpenItems(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	id, items := h.todo("Port the waiter", "one", "two", "three", "four", "five", "six")
	h.must("", "ack", id+"#"+items[0], "--done")
	h.must("", "ack", id+"#"+items[4], "--remove")

	brief := h.must("", "brief", "--again")
	want := "  " + id + " Port the waiter  [0m]  1/5\n    2. #" + items[1] + ": two\n    3. #" + items[2] + ": three\n    4. #" + items[3] + ": four\n"

	if !strings.Contains(brief, want) || strings.Contains(brief, ": six") {
		t.Errorf("the brief should give the count and up to three open items in order:\n%s", brief)
	}

	var answer struct {
		Entries []struct {
			Items []struct {
				ID, Text, State string
				Position        int
			} `json:"items"`
			Done  int `json:"done"`
			Total int `json:"total"`
		} `json:"entries"`
	}

	err := json.Unmarshal([]byte(h.must("", "show", id, "--json", "--again")), &answer)
	if err != nil || len(answer.Entries) != 1 {
		t.Fatalf("show --json should answer the todo: %v", err)
	}

	got := answer.Entries[0]
	if got.Done != 1 || got.Total != 5 || len(got.Items) != 5 || got.Items[0].State != "done" || got.Items[1].Position != 2 {
		t.Errorf("JSON should carry the items, the count done and the total, removed ones aside: %+v", got)
	}
}

func TestAFinishedTodolistEndsItsTodo(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	id, items := h.todo("Port the waiter", "one", "two")

	h.must("", "ack", id+"#"+items[0], "--done")

	out := h.must("", "ack", id+"#"+items[1], "--decline", "moot")
	if !strings.Contains(out, "retired "+id+": end reached: items 2/2") {
		t.Errorf("ticking or dropping the last item should retire the todo:\n%s", out)
	}

	brief := h.must("", "brief", "--again")
	if !strings.Contains(brief, id+" Port the waiter") || !strings.Contains(brief, "ended since last brief") {
		t.Errorf("the ended todo should be listed once under ended since last brief:\n%s", brief)
	}

	h.must("", "revive", id)
	h.must("", "brief", "--again")

	shown := h.must("", "show", id, "--again")
	first, _, _ := strings.Cut(shown, "\n")
	if strings.Contains(first, "retired") || strings.Count(shown, "end reached") != 1 {
		t.Errorf("a revived todo should stay live while no item changes:\n%s", shown)
	}
}

func TestSupersedingATodoCarriesOrLeavesItsItems(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	old, items := h.todo("Port the waiter", "one", "two", "stray")
	h.must("", "ack", old+"#"+items[1], "--done")
	h.must("", "ack", old+"#"+items[2], "--remove")

	if failed := h.run("", "ack", old+"#"+items[2], "--done"); failed.code != 1 || !strings.Contains(failed.out+failed.err, "no such item") {
		t.Errorf("a removed item should no longer be addressable, got %d %s%s", failed.code, failed.out, failed.err)
	}

	successor, own := h.todo("Port the waiter and the clock", "clock")

	failed := h.run("", "supersede", old, "--by", successor)
	if failed.code != 1 || !strings.Contains(failed.out+failed.err, "holds a todolist; pass --items carry or --items leave") {
		t.Fatalf("a supersede of a todo with items should name --items, got %d %s%s", failed.code, failed.out, failed.err)
	}

	h.must("", "supersede", old, "--by", successor, "--items", "carry")

	shown := h.must("", "show", successor, "--again")
	for _, want := range []string{"1. [ ] #" + own[0] + ": clock", "2. [ ] #" + items[0] + ": one", "3. [x] #" + items[1] + ": two"} {
		if !strings.Contains(shown, want) {
			t.Errorf("carry should move every item with its id, state and order, missing %q:\n%s", want, shown)
		}
	}

	if all := h.must("", "show", successor, "--again", "--with-removed"); strings.Contains(all, "stray") {
		t.Errorf("a removed item never belonged and should not be carried:\n%s", all)
	}

	h.must("", "ack", old+"#"+items[0], "--done")

	if shown := h.must("", "show", successor, "--again"); !strings.Contains(shown, "[x] #"+items[0]) {
		t.Errorf("an old address should resolve through the supersede to the carried item:\n%s", shown)
	}

	if failed := h.run("", "new", "--title", "Absorbing without a mode", "--topic", "gate", "--kind", "todo", "--absorbs", successor); failed.code != 1 ||
		!strings.Contains(failed.out+failed.err, successor+" holds a todolist; pass --items carry or --items leave") {
		t.Errorf("absorbing a todo with items should need --items, got %d %s%s", failed.code, failed.out, failed.err)
	}

	left, kept := h.todo("Port the timer", "one")
	h.must("", "supersede", left, "--because", "moved elsewhere", "--items", "leave")

	if shown := h.must("", "show", left, "--again"); !strings.Contains(shown, "#"+kept[0]+": one") {
		t.Errorf("leave should keep the items on the superseded todo:\n%s", shown)
	}
}

func TestACarriedItemWhoseIDIsTakenGetsANewOne(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	old, items := h.todo("Port the waiter", "one")
	successor, _ := h.todo("Port the waiter and the clock", "clock")
	h.must("", "supersede", old, "--by", successor, "--items", "carry")

	// Carried once already, the old todo's ids are the successor's when it
	// carries the same list again.
	h.must("", "revive", old)

	out := h.must("", "supersede", old, "--by", successor, "--items", "carry")
	if !strings.Contains(out, old+"#"+items[0]+" is now #") {
		t.Errorf("a carried item whose id the successor holds should get a new one, named in the reply:\n%s", out)
	}

	shown := h.must("", "show", successor, "--again")
	if strings.Count(shown, "#"+items[0]+":") != 1 || strings.Count(shown, ": one") != 2 {
		t.Errorf("both carried items should land under distinct ids:\n%s", shown)
	}
}

func TestATodoBodyWithListLinesNamesItem(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	out := h.must("steps:\n- read the timer\n- swap the clock\n1. run CI\n", "new", "--title", "Port the waiter", "--topic", "gate", "--kind", "todo")
	if !strings.Contains(out, "3 list lines in the text; steps go into --item") {
		t.Errorf("a todo filed with list lines should name --item:\n%s", out)
	}

	if note := h.must("- a point\n", "new", "--title", "A note with a list", "--topic", "gate"); strings.Contains(note, "list lines") {
		t.Errorf("a note's list is no todolist and needs no hint:\n%s", note)
	}
}

func TestAFormatFourStoreMovesUpWithABackup(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("Gate reads Bash only", "finding")

	formatPath := filepath.Join(h.store(), "format.json")
	test.WriteFile(t, formatPath, `{"format":4}`+"\n")

	h.must("", "search", "--term", "Gate", "--no-heat")

	written, _ := os.ReadFile(formatPath)
	if !strings.Contains(string(written), "5") {
		t.Errorf("a format-4 store should move to format 5: %s", written)
	}

	backups, _ := filepath.Glob(filepath.Join(h.store(), "backup", "format-4-*", "log"))
	if len(backups) != 1 {
		t.Errorf("the move should back the log up first, got %v", backups)
	}

	test.WriteFile(t, formatPath, `{"format":6}`+"\n")

	failed := h.run("", "search", "--term", "Gate", "--no-heat")
	if failed.code == 0 || !strings.Contains(failed.out+failed.err, "this release reads up to 5") {
		t.Errorf("a store of a later format should be refused, got %d %s%s", failed.code, failed.out, failed.err)
	}
}
