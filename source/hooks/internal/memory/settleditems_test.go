package memory_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestShowFoldsSettledItemsIntoACount(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	id, items := h.todo("Port the waiter", "one", "two", "three", "four")
	h.must("", "ack", id+"#"+items[0], "--done")
	h.must("", "ack", id+"#"+items[1], "--decline", "moot")
	h.must("", "ack", id+"#"+items[3], "--done")

	fold := "  3 settled (2 done, 1 dropped): agentic-memory show " + id + " --all-items  (run before judging what was done)\n"

	shown := h.must("", "show", id, "--again")
	if !strings.Contains(shown, "items 3/4:\n  3. [ ] #"+items[2]+": three\n"+fold) {
		t.Errorf("show should print open items in full and fold the settled ones into one count naming the flag:\n%s", shown)
	}

	_, section, _ := strings.Cut(shown, "items 3/4:")
	section, _, _ = strings.Cut(section, "events:")

	if strings.Contains(section, "#"+items[0]) || strings.Contains(section, "moot") {
		t.Errorf("a settled item and its reason should leave show without --all-items:\n%s", shown)
	}

	_, events, _ := strings.Cut(shown, "events:")
	if strings.Contains(events, " item #") || !strings.Contains(events, " item changes: agentic-memory show "+id+" --all-items  (run before judging what changed)\n") {
		t.Errorf("the seat's own item changes should fold into one line naming the flag:\n%s", events)
	}

	all := h.must("", "show", id, "--again", "--all-items")
	if !strings.Contains(all, " item #"+items[0]) || strings.Contains(all, "item changes:") {
		t.Errorf("--all-items should print each item change:\n%s", all)
	}

	for _, want := range []string{"1. [x] #" + items[0] + ": one", "2. [-] #" + items[1] + ": two\n    dropped: moot", "4. [x] #" + items[3] + ": four"} {
		if !strings.Contains(all, want) {
			t.Errorf("--all-items should print each settled item, missing %q:\n%s", want, all)
		}
	}

	if strings.Contains(all, "settled (") {
		t.Errorf("--all-items should print no count line:\n%s", all)
	}

	if view := h.must("", "view", "--layout", "kind(todo):as-bodies", "--again"); !strings.Contains(view, fold) {
		t.Errorf("a view's as-bodies should fold settled items as show does:\n%s", view)
	}

	ids := func(out string) []string {
		var answer struct {
			Entries []struct {
				Items []struct {
					ID string `json:"id"`
				} `json:"items"`
				Done  int `json:"done"`
				Total int `json:"total"`
			} `json:"entries"`
		}

		err := json.Unmarshal([]byte(out), &answer)
		if err != nil || len(answer.Entries) != 1 || answer.Entries[0].Done != 3 || answer.Entries[0].Total != 4 {
			t.Fatalf("show should answer the todo with its counts: %v %s", err, out)
		}

		var held []string
		for _, item := range answer.Entries[0].Items {
			held = append(held, item.ID)
		}

		return held
	}

	if got := ids(h.must("", "show", id, "--json", "--again")); len(got) != 1 || got[0] != items[2] {
		t.Errorf("show --json should carry the open items only, the counts covering the rest, got %v", got)
	}

	if got := ids(h.must("", "show", id, "--json", "--again", "--all-items")); len(got) != 4 {
		t.Errorf("show --json --all-items should carry every item, got %v", got)
	}

	if got := ids(mcpText(t, h.mcp(`{"name":"show","arguments":{"ids":["`+id+`"],"again":true,"all_items":true}}`))); len(got) != 4 {
		t.Errorf("MCP all_items should carry every item, got %v", got)
	}
}

func TestALinkedItemNamesItsTargetOnce(t *testing.T) {
	h := newHarness(t)
	withGraph(t, h)
	h.must("", "claim", "main")

	id, items := h.todo("Port the waiter", closedDecision, "write the notes")

	shown := h.must("", "show", id, "--again", "--all-items")
	if !strings.Contains(shown, "1. [x] #"+items[0]+": "+closedDecision+"  closed by "+itsCloser+"\n") {
		t.Errorf("a graph item should name its target once, the flag carrying the outcome alone:\n%s", shown)
	}

	var answer struct {
		Entries []struct {
			Items []map[string]any `json:"items"`
		} `json:"entries"`
	}

	err := json.Unmarshal([]byte(h.must("", "show", id, "--json", "--again", "--all-items")), &answer)
	if err != nil || len(answer.Entries) != 1 || len(answer.Entries[0].Items) != 2 {
		t.Fatalf("show --json should answer the todo's items: %v", err)
	}

	linked, plain := answer.Entries[0].Items[0], answer.Entries[0].Items[1]
	if _, found := linked["text"]; found || linked["link"] != closedDecision {
		t.Errorf("a linked item whose text equals its link should carry the link alone: %v", linked)
	}

	if plain["text"] != "write the notes" {
		t.Errorf("an item of text should keep it: %v", plain)
	}
}
