package tools

import "testing"

func lifted(value float64) *float64 {
	return &value
}

func TestRankValueOrdersAsFindSorts(t *testing.T) {
	ordered := []struct {
		name string
		hit  ranked
	}{
		{"a term hit with the most lifted cosine", ranked{term: true, score: lifted(maxLifted)}},
		{"a term hit with a lower cosine", ranked{term: true, score: lifted(0.6)}},
		{"a term hit with a cosine below zero", ranked{term: true, score: lifted(-0.2)}},
		{"a term hit below the floor", ranked{term: true}},
		{"a hit by meaning past the most a lift reaches", ranked{score: lifted(maxLifted + 0.01)}},
		{"a hit by meaning", ranked{score: lifted(0.8)}},
		{"a hit by meaning lower down", ranked{score: lifted(0.5)}},
		{"a hit by meaning below zero", ranked{score: lifted(-0.1)}},
	}

	for index, below := range ordered[1:] {
		above := ordered[index]

		if below.hit.rankValue() > above.hit.rankValue() {
			t.Errorf("%s should never value above %s, as find sorts them", below.name, above.name)
		}
	}
}

func TestPercentOfScoresAHitAgainstTheBest(t *testing.T) {
	cases := []struct {
		name  string
		value float64
		best  float64
		want  int
	}{
		{"the best hit", 1.7, 1.7, 100},
		{"half the best", 0.85, 1.7, 50},
		{"a share rounding up", 2, 3, 67},
		{"a best of zero", 0, 0, 100},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := percentOf(tc.value, tc.best); got != tc.want {
				t.Errorf("a hit's score should be its rank value in whole percent of the best, got %d", got)
			}
		})
	}
}
