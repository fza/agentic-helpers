package memory

import (
	"testing"
	"time"
)

func TestAPresentedKeyHoldsAcrossZones(t *testing.T) {
	filed := time.Date(2026, time.November, 1, 21, 9, 0, 0, time.UTC)

	cases := []struct {
		name string
		held entry
	}{
		{name: "a filing time", held: entry{status: statusLive}},
		{name: "a due time", held: entry{status: statusLive, due: filed.Add(time.Hour)}},
		{name: "a retirement", held: entry{status: statusRetired, transitions: []transition{{at: filed, because: "done"}}}},
		{name: "a supersede", held: entry{status: statusSuperseded, transitions: []transition{{at: filed, by: "m-next"}}}},
	}

	machine := time.Local

	t.Cleanup(func() { time.Local = machine })

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			held := testCase.held
			held.id, held.title, held.seat, held.kind, held.from, held.filed = "m-zone", "Zone entry", "main", "note", "agent", filed

			time.Local = time.FixedZone("EAST", 9*60*60)
			east, eastLine := presentedKey(tree{}, &held), line(tree{}, &held)

			time.Local = time.FixedZone("WEST", -5*60*60)
			west, westLine := presentedKey(tree{}, &held), line(tree{}, &held)

			if eastLine == westLine {
				t.Error("a printed line should show its stamps in the machine's zone")
			}

			if east != west {
				t.Error("a zone change should leave what the context holds unchanged")
			}
		})
	}
}

func TestPluralSpellsTheNounForItsCount(t *testing.T) {
	cases := []struct {
		count int
		noun  string
		want  string
	}{
		{count: 1, noun: "entry", want: "1 entry"},
		{count: 0, noun: "entry", want: "0 entries"},
		{count: 2, noun: "seat", want: "2 seats"},
		{count: 3, noun: "unused seat", want: "3 unused seats"},
	}

	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			got := plural(tc.count, tc.noun)
			if got != tc.want {
				t.Errorf("a count should take the noun's plural where it is not one, got %q", got)
			}
		})
	}
}
