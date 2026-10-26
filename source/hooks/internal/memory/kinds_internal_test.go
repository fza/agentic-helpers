package memory

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
)

func TestEachKindSetsItsDecay(t *testing.T) {
	filed := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

	cases := []struct {
		name    string
		kind    string
		later   []logged
		decay   string
		retains string
	}{
		{name: "a rule never decays", kind: kindRule, decay: "standing"},
		{name: "a trap decays at the long rate", kind: kindTrap, decay: "long"},
		{name: "a todo decays at the short rate", kind: kindTodo, decay: "short"},
		{name: "a note decays at the normal rate", kind: kindNote, decay: "normal"},
		{name: "no kind reads as a note", decay: "normal"},
		{
			name: "a retain overrides the kind", kind: kindTodo, decay: "long", retains: "long",
			later: []logged{{row: memorylog.Row{Kind: memorylog.KindRetain}, payload: retainPayload{Level: "long"}}},
		},
		{
			name: "a rekind keeps the override", kind: kindNote, decay: "scratch", retains: "scratch",
			later: []logged{
				{row: memorylog.Row{Kind: memorylog.KindRetain}, payload: retainPayload{Level: "scratch"}},
				{row: memorylog.Row{Kind: memorylog.KindRekind}, payload: rekindPayload{Kind: kindTrap}},
			},
		},
		{
			name: "a rekind without an override takes the new kind's decay", kind: kindNote, decay: "long",
			later: []logged{{row: memorylog.Row{Kind: memorylog.KindRekind}, payload: rekindPayload{Kind: kindTrap}}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events := []logged{{
				row:     memorylog.Row{Kind: memorylog.KindNew, Entry: "m-aaaaaa", Time: filed, Seat: "main"},
				payload: newPayload{Title: "Gate finding of a kind", Kind: tc.kind},
			}}

			for index, later := range tc.later {
				later.row.Entry, later.row.Seat = "m-aaaaaa", "main"
				later.row.Time = filed.Add(time.Duration(index+1) * time.Minute)
				events = append(events, later)
			}

			rows, read := rowsOf(t, events)
			folded := foldRows(t, rows, read)
			held := folded.entries["m-aaaaaa"]

			if held.retention != tc.decay {
				t.Errorf("the decay in force should follow the kind and any retain override, got %s", held.retention)
			}

			if held.retainedOverride() != tc.retains {
				t.Errorf("only an override differing from the kind's decay should show, got %q", held.retainedOverride())
			}
		})
	}
}

func TestAMergeKeepsEachSeatsNewestStateRow(t *testing.T) {
	start := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

	rows, read := rowsOf(t, []logged{
		{row: memorylog.Row{Kind: memorylog.KindState, Time: start, Seat: "s-main01"}, payload: statePayload{Body: "first"}},
		{row: memorylog.Row{Kind: memorylog.KindState, Time: start.Add(time.Minute), Seat: "s-drive1"}, payload: statePayload{Body: "drive"}},
		{row: memorylog.Row{Kind: memorylog.KindState, Time: start.Add(2 * time.Minute), Seat: "s-main01"}, payload: statePayload{Body: "second"}},
	})

	kept, err := mergePolicy(start.Add(time.Hour), 90*24*time.Hour)(rows, read)
	if err != nil {
		t.Fatalf("the policy should fold the rows: %v", err)
	}

	bodies := map[string]string{}

	for _, event := range kept {
		var slot statePayload

		err = json.Unmarshal(event.Payload, &slot)
		if err != nil {
			t.Fatal(err)
		}

		if _, twice := bodies[event.Seat]; twice {
			t.Errorf("a merge should keep one state row per seat, %s kept two", event.Seat)
		}

		bodies[event.Seat] = slot.Body
	}

	if bodies["s-main01"] != "second" || bodies["s-drive1"] != "drive" {
		t.Errorf("a merge should keep each seat's newest state row, got %v", bodies)
	}
}

func TestAMergeFoldsARekindIntoTheNewRow(t *testing.T) {
	filed := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)

	rows, read := rowsOf(t, []logged{
		{
			row:     memorylog.Row{Kind: memorylog.KindNew, Entry: "m-aaaaaa", Time: filed, Seat: "main"},
			payload: newPayload{Title: "Gate finding of a kind", Kind: kindNote, Retention: "scratch"},
		},
		{
			row:     memorylog.Row{Kind: memorylog.KindRekind, Entry: "m-aaaaaa", Time: filed.Add(time.Hour), Seat: "main"},
			payload: rekindPayload{Kind: kindTrap},
		},
	})

	kept, err := mergePolicy(filed.Add(30*24*time.Hour), 90*24*time.Hour)(rows, read)
	if err != nil {
		t.Fatalf("the policy should fold the rows: %v", err)
	}

	var filedAs newPayload

	err = json.Unmarshal(kept[0].Payload, &filedAs)
	if err != nil {
		t.Fatal(err)
	}

	if len(kept) != 1 || filedAs.Kind != kindTrap || filedAs.Retention != "scratch" {
		t.Errorf("a rekind past the horizon should fold into the new row, keeping the override, got %d rows, %+v", len(kept), filedAs)
	}
}

func TestAssignKinds(t *testing.T) {
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

	filed := func(entry string, payload string) memorylog.Event {
		return memorylog.Event{Kind: memorylog.KindNew, Entry: entry, Time: at, Seat: "s-main01", Payload: []byte(payload)}
	}

	retained := func(entry string, level string) memorylog.Event {
		return memorylog.Event{Kind: memorylog.KindRetain, Entry: entry, Time: at.Add(time.Hour), Seat: "s-main01",
			Payload: []byte(`{"level":"` + level + `"}`)}
	}

	cases := []struct {
		name      string
		events    []memorylog.Event
		kind      string
		retention string
		from      string
	}{
		{name: "standing becomes a rule keeping its source", kind: kindRule, from: "agent",
			events: []memorylog.Event{filed("m-a", `{"title":"t","retention":"standing","from":"agent"}`)}},
		{name: "long becomes a note keeping its retention", kind: kindNote, retention: "long", from: "user",
			events: []memorylog.Event{filed("m-a", `{"title":"t","retention":"long","from":"user"}`)}},
		{name: "normal becomes a note keeping its retention", kind: kindNote, retention: "normal", from: "agent",
			events: []memorylog.Event{filed("m-a", `{"title":"t","retention":"normal","from":"agent"}`)}},
		{name: "retained to standing becomes a rule", kind: kindRule, from: "user",
			events: []memorylog.Event{filed("m-a", `{"title":"t","retention":"short","from":"user"}`), retained("m-a", "standing")}},
		{name: "retained away from standing becomes a note", kind: kindNote, retention: "standing", from: "user",
			events: []memorylog.Event{filed("m-a", `{"title":"t","retention":"standing","from":"user"}`), retained("m-a", "long")}},
		{name: "a kind already given stays", kind: kindTodo, from: "agent",
			events: []memorylog.Event{filed("m-a", `{"title":"t","kind":"todo","from":"agent"}`)}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := assignKinds(tc.events)
			if err != nil {
				t.Fatalf("assigning kinds should read every event: %v", err)
			}

			var got newPayload

			err = json.Unmarshal(tc.events[0].Payload, &got)
			if err != nil {
				t.Fatal(err)
			}

			if got.Kind != tc.kind || got.Retention != tc.retention || got.From != tc.from || got.Title != "t" {
				t.Errorf("the migrated entry should carry its kind, override and source, got %+v", got)
			}
		})
	}
}
