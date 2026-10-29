package levenshtein_test

import (
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/levenshtein"
)

func TestDistanceCountsEdits(t *testing.T) {
	cases := []struct {
		a, b     string
		distance int
	}{
		{a: "", b: "", distance: 0},
		{a: "", b: "abc", distance: 3},
		{a: "kitten", b: "sitting", distance: 3},
		{a: "topic", b: "topics", distance: 1},
		{a: "über", b: "uber", distance: 1},
	}

	for _, tc := range cases {
		t.Run(tc.a+"/"+tc.b, func(t *testing.T) {
			if got := levenshtein.Distance(tc.a, tc.b); got != tc.distance {
				t.Errorf("the distance should count the fewest single-letter edits, got %d", got)
			}
		})
	}
}

func TestNearestPicksTheClosestCandidate(t *testing.T) {
	cases := []struct {
		name       string
		word       string
		candidates []string
		nearest    string
	}{
		{name: "closest", word: "entry_max_word", candidates: []string{"brief_note_limit", "entry_max_words"}, nearest: "entry_max_words"},
		{name: "case ignored", word: "HEAT", candidates: []string{"heap", "heat"}, nearest: "heat"},
		{name: "first among equals", word: "ab", candidates: []string{"aa", "bb"}, nearest: "aa"},
		{name: "no candidate", word: "x", nearest: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := levenshtein.Nearest(tc.word, tc.candidates); got != tc.nearest {
				t.Errorf("the nearest candidate should be the one fewest edits away, got %q", got)
			}
		})
	}
}
