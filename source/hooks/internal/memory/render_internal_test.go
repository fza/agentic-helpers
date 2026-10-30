package memory

import "testing"

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
