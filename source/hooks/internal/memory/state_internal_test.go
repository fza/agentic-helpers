package memory

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
)

func TestMergePolicyDropsBodiesSupersededLongAgo(t *testing.T) {
	filed := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	superseded := filed.Add(time.Hour)

	body, err := json.Marshal(newPayload{Title: "Old finding about the gate", Body: "the body"})
	if err != nil {
		t.Fatal(err)
	}

	because, err := json.Marshal(supersedePayload{Because: "wrong"})
	if err != nil {
		t.Fatal(err)
	}

	rows := []memorylog.Row{
		{Kind: memorylog.KindNew, Entry: "m-aaaa", Time: filed, Seat: "main"},
		{Kind: memorylog.KindSupersede, Entry: "m-aaaa", Time: superseded, Seat: "main"},
	}
	payloads := [][]byte{body, because}
	read := func(index int) ([]byte, error) { return payloads[index], nil }

	cases := []struct {
		name    string
		now     time.Time
		dropped bool
	}{
		{name: "fifteen days after the supersede the body goes", now: superseded.Add(15 * 24 * time.Hour), dropped: true},
		{name: "a day after the supersede the body stays", now: superseded.Add(24 * time.Hour)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kept, err := mergePolicy(tc.now, 90*24*time.Hour)(rows, read)
			if err != nil {
				t.Fatalf("the policy should fold the rows: %v", err)
			}

			if len(kept) != len(rows) {
				t.Fatal("the policy should keep every event")
			}

			var entry newPayload

			err = json.Unmarshal(kept[0].Payload, &entry)
			if err != nil {
				t.Fatal(err)
			}

			if entry.Dropped != tc.dropped || (entry.Body == "") != tc.dropped {
				t.Error("a merge should drop exactly the bodies superseded more than 14 days earlier")
			}

			if entry.Title == "" {
				t.Error("a dropped body should keep its title")
			}
		})
	}
}

type logged struct {
	row     memorylog.Row
	payload any
}

func rowsOf(t *testing.T, events []logged) ([]memorylog.Row, func(int) ([]byte, error)) {
	t.Helper()

	rows := make([]memorylog.Row, 0, len(events))
	payloads := make([][]byte, 0, len(events))

	for _, event := range events {
		raw := []byte("{}")
		if event.payload != nil {
			var err error

			raw, err = json.Marshal(event.payload)
			if err != nil {
				t.Fatal(err)
			}
		}

		rows = append(rows, event.row)
		payloads = append(payloads, raw)
	}

	return rows, func(index int) ([]byte, error) { return payloads[index], nil }
}

func foldEvents(t *testing.T, events []memorylog.Event) *state {
	t.Helper()

	folded := &state{entries: map[string]*entry{}}

	for _, event := range events {
		row := memorylog.Row{Kind: event.Kind, Entry: event.Entry, Time: event.Time, Seat: event.Seat, Target: event.Target}

		err := folded.apply(row, event.Payload)
		if err != nil {
			t.Fatal(err)
		}
	}

	return folded
}

func foldRows(t *testing.T, rows []memorylog.Row, read func(int) ([]byte, error)) *state {
	t.Helper()

	events := make([]memorylog.Event, 0, len(rows))

	for index, row := range rows {
		raw, err := read(index)
		if err != nil {
			t.Fatal(err)
		}

		events = append(events, memorylog.Event{
			Kind: row.Kind, Entry: row.Entry, Time: row.Time, Seat: row.Seat, Target: row.Target, Payload: raw,
		})
	}

	return foldEvents(t, events)
}

type entryState struct {
	title, body, retention, status, commit, branch string
	topics                                         []string
	anchors                                        []anchor
	verified                                       time.Time
	dropped                                        bool
	transitions                                    []transition
}

func stateOf(held *entry) entryState {
	return entryState{
		title: held.title, body: held.body, retention: held.retention, status: held.status, commit: held.commit,
		branch: held.branch, topics: held.topics, anchors: held.anchors, verified: held.verified,
		dropped: held.dropped, transitions: held.transitions,
	}
}

func kindsOf(events []memorylog.Event) []memorylog.Kind {
	kinds := make([]memorylog.Kind, 0, len(events))
	for _, event := range events {
		kinds = append(kinds, event.Kind)
	}

	return kinds
}

func TestMergePolicyFoldsEventsPastTheHorizon(t *testing.T) {
	filed := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	at := func(hours int) time.Time { return filed.Add(time.Duration(hours) * time.Hour) }
	retiredKeep := 90 * 24 * time.Hour
	filedPayload := newPayload{
		Title: "Gate reads Bash only", Body: "the body", Topics: []string{"gate"}, Retention: "normal",
		Commit: "15af1a8c", Branch: "main", Anchors: []anchor{{Path: "gate.go", Hash: "aaaa"}},
	}
	firstConfirm := confirmPayload{Anchors: []anchor{{Path: "gate.go", Hash: "bbbb"}}, Commit: "2d1fc1f8", Branch: "main"}
	lastConfirm := confirmPayload{Anchors: []anchor{{Path: "gate.go", Hash: "cccc"}}, Commit: "3e2a9b01", Branch: "fix"}

	cases := []struct {
		name      string
		now       time.Time
		events    []logged
		wantKinds []memorylog.Kind
		wantBody  bool
	}{
		{
			name: "an entry's whole history past the horizon folds into new, confirm and retire",
			now:  at(30 * 24),
			events: []logged{
				{memorylog.Row{Kind: memorylog.KindNew, Entry: "m-aaaa", Time: filed, Seat: "main"}, filedPayload},
				{memorylog.Row{Kind: memorylog.KindRetitle, Entry: "m-aaaa", Time: at(1), Seat: "drive"}, retitlePayload{Title: "Gate reads every tool"}},
				{memorylog.Row{Kind: memorylog.KindTag, Entry: "m-aaaa", Time: at(2), Seat: "main"}, tagPayload{Topics: []string{"mcp"}}},
				{memorylog.Row{Kind: memorylog.KindRetain, Entry: "m-aaaa", Time: at(3), Seat: "main"}, retainPayload{Level: "long"}},
				{memorylog.Row{Kind: memorylog.KindConfirm, Entry: "m-aaaa", Time: at(4), Seat: "main"}, firstConfirm},
				{memorylog.Row{Kind: memorylog.KindConfirm, Entry: "m-aaaa", Time: at(5), Seat: "drive"}, lastConfirm},
				{memorylog.Row{Kind: memorylog.KindRetire, Entry: "m-aaaa", Time: at(6), Seat: "main"}, nil},
			},
			wantKinds: []memorylog.Kind{memorylog.KindNew, memorylog.KindConfirm, memorylog.KindRetire},
			wantBody:  true,
		},
		{
			name: "events younger than the horizon stay as they are",
			now:  at(20 * 24),
			events: []logged{
				{memorylog.Row{Kind: memorylog.KindNew, Entry: "m-aaaa", Time: filed, Seat: "main"}, filedPayload},
				{memorylog.Row{Kind: memorylog.KindTag, Entry: "m-aaaa", Time: at(1), Seat: "main"}, tagPayload{Topics: []string{"mcp"}}},
				{memorylog.Row{Kind: memorylog.KindRetitle, Entry: "m-aaaa", Time: at(10 * 24), Seat: "main"}, retitlePayload{Title: "Gate reads every tool"}},
				{memorylog.Row{Kind: memorylog.KindRetire, Entry: "m-aaaa", Time: at(11 * 24), Seat: "main"}, nil},
				{memorylog.Row{Kind: memorylog.KindRevive, Entry: "m-aaaa", Time: at(12 * 24), Seat: "main"}, nil},
			},
			wantKinds: []memorylog.Kind{memorylog.KindNew, memorylog.KindRetitle, memorylog.KindRetire, memorylog.KindRevive},
			wantBody:  true,
		},
		{
			name: "a revive past the horizon folds away with the retire it undid",
			now:  at(30 * 24),
			events: []logged{
				{memorylog.Row{Kind: memorylog.KindNew, Entry: "m-aaaa", Time: filed, Seat: "main"}, filedPayload},
				{memorylog.Row{Kind: memorylog.KindRetire, Entry: "m-aaaa", Time: at(1), Seat: "main"}, nil},
				{memorylog.Row{Kind: memorylog.KindRevive, Entry: "m-aaaa", Time: at(2), Seat: "main"}, nil},
				{memorylog.Row{Kind: memorylog.KindSupersede, Entry: "m-aaaa", Time: at(3), Seat: "main", Target: "m-bbbb"}, supersedePayload{By: "m-bbbb"}},
			},
			wantKinds: []memorylog.Kind{memorylog.KindNew, memorylog.KindSupersede},
		},
		{
			// A writer reads the clock before it takes the log lock, so an
			// older stamp can land after a younger one.
			name: "an old stamp after a young event stays behind it",
			now:  at(20 * 24),
			events: []logged{
				{memorylog.Row{Kind: memorylog.KindNew, Entry: "m-aaaa", Time: filed, Seat: "main"}, filedPayload},
				{memorylog.Row{Kind: memorylog.KindRetire, Entry: "m-aaaa", Time: at(10 * 24), Seat: "main"}, nil},
				{memorylog.Row{Kind: memorylog.KindRevive, Entry: "m-aaaa", Time: at(1), Seat: "drive"}, nil},
			},
			wantKinds: []memorylog.Kind{memorylog.KindNew, memorylog.KindRetire, memorylog.KindRevive},
			wantBody:  true,
		},
		{
			name: "a topic merge survives the fold",
			now:  at(30 * 24),
			events: []logged{
				{memorylog.Row{Kind: memorylog.KindNew, Entry: "m-aaaa", Time: filed, Seat: "main"}, filedPayload},
				{memorylog.Row{Kind: memorylog.KindMerge, Time: at(1), Seat: "main"}, mergePayload{From: "gate", Into: "gate-reads"}},
				{memorylog.Row{Kind: memorylog.KindRetain, Entry: "m-aaaa", Time: at(2), Seat: "main"}, retainPayload{Level: "long"}},
			},
			wantKinds: []memorylog.Kind{memorylog.KindNew, memorylog.KindMerge},
			wantBody:  true,
		},
		{
			name: "a body retired longer ago than the setting is dropped",
			now:  at(6 + 91*24),
			events: []logged{
				{memorylog.Row{Kind: memorylog.KindNew, Entry: "m-aaaa", Time: filed, Seat: "main"}, filedPayload},
				{memorylog.Row{Kind: memorylog.KindRetire, Entry: "m-aaaa", Time: at(6), Seat: "main"}, nil},
			},
			wantKinds: []memorylog.Kind{memorylog.KindNew, memorylog.KindRetire},
		},
		{
			name: "a body retired more recently than the setting stays",
			now:  at(6 + 89*24),
			events: []logged{
				{memorylog.Row{Kind: memorylog.KindNew, Entry: "m-aaaa", Time: filed, Seat: "main"}, filedPayload},
				{memorylog.Row{Kind: memorylog.KindRetire, Entry: "m-aaaa", Time: at(6), Seat: "main"}, nil},
			},
			wantKinds: []memorylog.Kind{memorylog.KindNew, memorylog.KindRetire},
			wantBody:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, read := rowsOf(t, tc.events)

			kept, err := mergePolicy(tc.now, retiredKeep)(rows, read)
			if err != nil {
				t.Fatalf("the policy should fold the rows: %v", err)
			}

			if !slices.Equal(kindsOf(kept), tc.wantKinds) {
				t.Errorf("a merge should keep the shortest run rebuilding the entry, got %v", kindsOf(kept))
			}

			before := foldRows(t, rows, read)
			after := foldEvents(t, kept)

			if !slices.Equal(after.merges, before.merges) {
				t.Error("a merge should keep every topic merge")
			}

			want := stateOf(before.entries["m-aaaa"])
			got := stateOf(after.entries["m-aaaa"])

			if !tc.wantBody {
				want.body = ""
				want.dropped = true
			}

			if !reflect.DeepEqual(got, want) {
				t.Errorf("the folded log should rebuild the entry as the full log does\n got %+v\nwant %+v", got, want)
			}
		})
	}
}

func TestMergePolicyKeepsYoungEventsByteForByte(t *testing.T) {
	filed := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	now := filed.Add(20 * 24 * time.Hour)

	rows, read := rowsOf(t, []logged{
		{memorylog.Row{Kind: memorylog.KindNew, Entry: "m-aaaa", Time: filed, Seat: "main"}, newPayload{Title: "Gate reads Bash only"}},
		{memorylog.Row{Kind: memorylog.KindRetitle, Entry: "m-aaaa", Time: now.Add(-time.Hour), Seat: "drive"}, retitlePayload{Title: "Gate reads every tool"}},
	})

	kept, err := mergePolicy(now, 90*24*time.Hour)(rows, read)
	if err != nil {
		t.Fatalf("the policy should fold the rows: %v", err)
	}

	young, _ := read(1)
	if len(kept) != 2 || kept[1].Seat != "drive" || !kept[1].Time.Equal(rows[1].Time) || string(kept[1].Payload) != string(young) {
		t.Error("an event past the horizon should be kept exactly as it was appended")
	}
}

func TestMergePolicyKeepsAYoungEntryByteForByte(t *testing.T) {
	filed := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	written := []byte(`{"title":"Gate reads Bash only","body":"the body","topics":["gate"],"retention":"normal","written_by_a_later_release":true}`)
	rows := []memorylog.Row{{Kind: memorylog.KindNew, Entry: "m-aaaa", Time: filed, Seat: "main"}}
	read := func(int) ([]byte, error) { return written, nil }

	kept, err := mergePolicy(filed.Add(24*time.Hour), 90*24*time.Hour)(rows, read)
	if err != nil {
		t.Fatalf("the policy should fold the rows: %v", err)
	}

	if len(kept) != 1 || string(kept[0].Payload) != string(written) {
		t.Error("an entry filed inside the horizon should keep its new row exactly as it was appended")
	}
}

func TestAMergeKeepsNetExplicitLinks(t *testing.T) {
	filed := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	at := func(hours int) time.Time { return filed.Add(time.Duration(hours) * time.Hour) }
	payload := newPayload{Title: "Waiter test flaky on CI", Body: "flaky", Topics: []string{"waiter"}, Retention: "long", From: "agent"}

	rows, read := rowsOf(t, []logged{
		{memorylog.Row{Kind: memorylog.KindNew, Entry: "m-aaaa", Time: filed, Seat: "idea"}, payload},
		{memorylog.Row{Kind: memorylog.KindLink, Entry: "m-aaaa", Time: at(1), Seat: "main", Target: "m-bbbb"}, nil},
		{memorylog.Row{Kind: memorylog.KindLink, Entry: "m-aaaa", Time: at(2), Seat: "main", Target: "m-cccc"}, nil},
		{memorylog.Row{Kind: memorylog.KindUnlink, Entry: "m-aaaa", Time: at(3), Seat: "drive", Target: "m-cccc"}, nil},
	})

	kept, err := mergePolicy(at(30*24), 90*24*time.Hour)(rows, read)
	if err != nil {
		t.Fatalf("the policy should fold the rows: %v", err)
	}

	if !slices.Equal(kindsOf(kept), []memorylog.Kind{memorylog.KindNew, memorylog.KindLink}) || kept[1].Target != "m-bbbb" ||
		kept[1].Seat != "main" {
		t.Errorf("a merge should keep one link row per net explicit link, with the seat that made it, got %+v", kept)
	}

	before := foldRows(t, rows, read)
	after := foldEvents(t, kept)

	if !reflect.DeepEqual(after.entries["m-aaaa"].explicit, before.entries["m-aaaa"].explicit) {
		t.Errorf("the folded log should keep the links the full log holds, got %+v want %+v",
			after.entries["m-aaaa"].explicit, before.entries["m-aaaa"].explicit)
	}
}
