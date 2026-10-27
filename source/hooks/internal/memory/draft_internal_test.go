package memory

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
)

func TestADraftNeverReplacesTheAgentsStateOfItsSession(t *testing.T) {
	start := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

	cases := []struct {
		name  string
		draft statePayload
		want  string
	}{
		{name: "a draft racing the agent's write in the same session", draft: statePayload{Body: "draft", Session: "s-1", Draft: true}, want: "agent"},
		{name: "a draft over a state an earlier session wrote", draft: statePayload{Body: "draft", Session: "s-2", Draft: true}, want: "draft"},
		{name: "the agent's write over its own session's draft", draft: statePayload{Body: "again", Session: "s-1"}, want: "again"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, read := rowsOf(t, []logged{
				{row: memorylog.Row{Kind: memorylog.KindState, Time: start, Seat: "s-main01"}, payload: statePayload{Body: "agent", Session: "s-1"}},
				{row: memorylog.Row{Kind: memorylog.KindState, Time: start.Add(time.Minute), Seat: "s-main01"}, payload: tc.draft},
			})

			if got := foldRows(t, rows, read).states["s-main01"].Body; got != tc.want {
				t.Errorf("the fold should keep the state the agent wrote over a draft of its own session, got %q", got)
			}

			kept, err := mergePolicy(start.Add(time.Hour), 90*24*time.Hour)(rows, read)
			if err != nil {
				t.Fatalf("the policy should fold the rows: %v", err)
			}

			var bodies []string

			for _, event := range kept {
				var slot statePayload

				err = json.Unmarshal(event.Payload, &slot)
				if err != nil {
					t.Fatal(err)
				}

				bodies = append(bodies, slot.Body)
			}

			if len(bodies) != 1 || bodies[0] != tc.want {
				t.Errorf("a merge should keep the state row the fold reads, got %v", bodies)
			}
		})
	}
}

func TestADraftStaysWithinTheWordLimit(t *testing.T) {
	text := "commits since the claim: a1 one; b2 two\nfiles written: a.go, b.go\nlast prompt: fix the gate now please"

	cases := []struct {
		name  string
		limit int
		want  string
	}{
		{name: "under the limit", limit: 100, want: text},
		{name: "cut inside a line", limit: 11, want: "commits since the claim: a1 one; b2 two\nfiles written: a.go,"},
		{name: "no limit", limit: 0, want: text},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cutWords(text, tc.limit)
			if got != tc.want || words(got) > max(tc.limit, words(text)) {
				t.Errorf("a draft should keep whole words up to the limit, got %q", got)
			}
		})
	}
}
