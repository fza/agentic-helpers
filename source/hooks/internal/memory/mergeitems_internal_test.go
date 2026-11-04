package memory

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
)

// A merge folding an old todo's leading run into its new row keeps the item
// rows: the new row carries no items, so a folded item row would drop the
// item from the list.
func TestAMergeKeepsAnOldTodolistsItems(t *testing.T) {
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	filedAt := now.Add(-30 * 24 * time.Hour)

	filed, err := json.Marshal(newPayload{Title: "Port the waiter to Linux", Kind: kindTodo})
	if err != nil {
		t.Fatal(err)
	}

	first, err := json.Marshal(itemPayload{Op: itemAdd, ID: "abc", Text: "read the darwin waiter"})
	if err != nil {
		t.Fatal(err)
	}

	ticked, err := json.Marshal(itemPayload{Op: itemCheck, ID: "abc"})
	if err != nil {
		t.Fatal(err)
	}

	retitled, err := json.Marshal(retitlePayload{Title: "Port the waiter to Linux and BSD"})
	if err != nil {
		t.Fatal(err)
	}

	rows := []memorylog.Row{
		{Kind: memorylog.KindNew, Entry: "m-abc123", Time: filedAt, Seat: "main"},
		{Kind: memorylog.KindItem, Entry: "m-abc123", Time: filedAt, Seat: "main"},
		{Kind: memorylog.KindItem, Entry: "m-abc123", Time: filedAt.Add(time.Hour), Seat: "main"},
		{Kind: memorylog.KindRetitle, Entry: "m-abc123", Time: filedAt.Add(2 * time.Hour), Seat: "main"},
	}
	payloads := [][]byte{filed, first, ticked, retitled}

	kept, err := mergePolicy(now, 30*24*time.Hour)(rows, func(index int) ([]byte, error) { return payloads[index], nil })
	if err != nil {
		t.Fatal(err)
	}

	folded := newState()

	for _, event := range kept {
		err = folded.apply(memorylog.Row{Kind: event.Kind, Entry: event.Entry, Time: event.Time, Seat: event.Seat}, event.Payload)
		if err != nil {
			t.Fatal(err)
		}
	}

	held := folded.entries["m-abc123"]
	if held == nil || len(held.items) != 1 || held.items[0].State != itemDone {
		t.Fatalf("the merged todo should keep its items with their state: %+v", kept)
	}

	if held.title != "Port the waiter to Linux and BSD" || len(kept) != 3 {
		t.Errorf("the merge should still fold the retitle into the new row, keeping the item rows beside it: %+v", kept)
	}
}
