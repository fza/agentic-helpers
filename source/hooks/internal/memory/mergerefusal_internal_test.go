package memory

import (
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
)

func TestAMergeRefusalNamesWhatKeepsTheMergeFromAnAgent(t *testing.T) {
	offering := func(ids ...string) *home.Question {
		asked := &home.Question{}
		for _, id := range ids {
			asked.Stores = append(asked.Stores, home.Candidate{ID: id})
		}

		return asked
	}

	cases := []struct {
		name     string
		key      string
		question *home.Question
		from     string
		into     string
		refusal  string
	}{
		{
			name: "the own store with an offered one", key: "own", question: offering("graph"),
			from: "graph", into: "own",
		},
		{
			name: "the own store with an unoffered one", key: "own", question: offering("graph"),
			from: "own", into: "other",
			refusal: "other is not offered for this checkout; run the merge in a terminal",
		},
		{
			name: "an unoffered store into the own", key: "own", question: offering("graph"),
			from: "other", into: "own",
			refusal: "other is not offered for this checkout; run the merge in a terminal",
		},
		{
			name: "an offered store with an unoffered one, no own store on either side", key: "own", question: offering("graph"),
			from: "graph", into: "other",
			refusal: "other is not offered for this checkout; run the merge in a terminal",
		},
		{
			name: "an unoffered store into an offered one, no own store on either side", key: "own", question: offering("graph"),
			from: "other", into: "graph",
			refusal: "other is not offered for this checkout; run the merge in a terminal",
		},
		{
			name: "two offered stores and no own store", question: offering("first", "second"),
			from: "first", into: "second",
			refusal: "neither first nor second is this checkout's own store; run the merge in a terminal",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mem := &memory{key: tc.key, question: tc.question}

			got := mem.mergeRefusal(tc.from, tc.into)
			if got != tc.refusal {
				t.Errorf("the refusal should name what keeps the merge from an agent, got %q", got)
			}
		})
	}
}
