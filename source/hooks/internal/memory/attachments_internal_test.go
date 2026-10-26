package memory

import (
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
)

func TestAMergeKeepsAttachRowsWhileTheBodyLasts(t *testing.T) {
	filed := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	attached := attachPayload{Name: "build.log", SHA256: "ab12", Size: 10, Form: formRaw}

	cases := []struct {
		name    string
		retired bool
		kept    bool
	}{
		{name: "a live entry keeps its attachment", kept: true},
		{name: "a retire past retired_body_days drops it with the body", retired: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events := []logged{
				{row: memorylog.Row{Kind: memorylog.KindNew, Entry: "m-aaaaaa", Time: filed, Seat: "main"},
					payload: newPayload{Title: "Gate run with its log", Body: "body", Kind: kindNote}},
				{row: memorylog.Row{Kind: memorylog.KindAttach, Entry: "m-aaaaaa", Time: filed.Add(time.Hour), Seat: "main"},
					payload: attached},
			}

			if tc.retired {
				events = append(events, logged{
					row: memorylog.Row{Kind: memorylog.KindRetire, Entry: "m-aaaaaa", Time: filed.Add(2 * time.Hour), Seat: "main"},
				})
			}

			rows, read := rowsOf(t, events)

			kept, err := mergePolicy(filed.Add(120*24*time.Hour), 90*24*time.Hour)(rows, read)
			if err != nil {
				t.Fatalf("the policy should fold the rows: %v", err)
			}

			found := false
			for _, event := range kept {
				found = found || event.Kind == memorylog.KindAttach
			}

			if found != tc.kept {
				t.Errorf("a merge should keep an attach row exactly while its entry holds its body, kept %v", kept)
			}
		})
	}
}
