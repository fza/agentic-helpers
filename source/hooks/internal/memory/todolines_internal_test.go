package memory

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
)

// A merge folding a todo's rekind into its new row keeps the ends and due
// time the rekind moved to the todo's state line off the note.
func TestAMergeKeepsTheEndsARekindMovedOffTheNote(t *testing.T) {
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	filedAt := now.Add(-30 * 24 * time.Hour)

	filed, err := json.Marshal(newPayload{
		Title: "Rescan transcripts for parse failures", Kind: kindTodo, Until: []string{"merged:release"},
		UntilHashes: map[string]string{"README.md": "abc"}, Due: now.Add(-20 * 24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}

	moved, err := json.Marshal(rekindPayload{Kind: kindNote, Line: "state#k3x"})
	if err != nil {
		t.Fatal(err)
	}

	rows := []memorylog.Row{
		{Kind: memorylog.KindNew, Entry: "m-abc123", Time: filedAt, Seat: "main"},
		{Kind: memorylog.KindRekind, Entry: "m-abc123", Time: filedAt.Add(time.Hour), Seat: "main"},
	}
	payloads := [][]byte{filed, moved}

	kept, err := mergePolicy(now, 30*24*time.Hour)(rows, func(index int) ([]byte, error) { return payloads[index], nil })
	if err != nil {
		t.Fatal(err)
	}

	if len(kept) != 1 || kept[0].Kind != memorylog.KindNew {
		t.Fatalf("the merge should fold the rekind into the new row: %+v", kept)
	}

	var merged newPayload

	err = json.Unmarshal(kept[0].Payload, &merged)
	if err != nil {
		t.Fatal(err)
	}

	if merged.Kind != kindNote || len(merged.Until) > 0 || len(merged.UntilHashes) > 0 || !merged.Due.IsZero() {
		t.Errorf("the merged note should carry its kind and none of the ends or due time its line took: %+v", merged)
	}
}
