package memory

import (
	"encoding/json"
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
			kept, err := mergePolicy(tc.now)(rows, read)
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
