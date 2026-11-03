package memory

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
)

func TestLandedScoresRankDirectAgainstMerge(t *testing.T) {
	cases := []struct {
		name          string
		direct, merge float64
		mergeWins     bool
	}{
		{name: "a direct named id over a merge at 0.70", direct: 1.0, merge: 0.70},
		{name: "a merge at 0.80 over a direct named id", direct: 1.0, merge: 0.80, mergeWins: true},
		{name: "a merge over a direct commit of equal strength", direct: 0.5, merge: 0.5, mergeWins: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if (landedScore(tc.merge, true) > landedScore(tc.direct, false)) != tc.mergeWins {
				t.Errorf("the merge share should decide only within its margin")
			}
		})
	}
}

func TestSimilarityStrengthStretchesAboveTheThreshold(t *testing.T) {
	for _, tc := range []struct {
		cosine float32
		want   float64
	}{{cosine: landedSimilarity, want: 0}, {cosine: 1, want: 1}, {cosine: 0.875, want: 0.5}} {
		if math.Abs(similarityStrength(tc.cosine)-tc.want) > 1e-6 {
			t.Errorf("a cosine of %v should stretch onto 0..1 above the threshold", tc.cosine)
		}
	}
}

func TestMatchLandingsRanksAmongMatches(t *testing.T) {
	filed := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	held := &entry{id: "m-aaaaaa", filed: filed}
	target := landedTarget{address: held.id, held: held, floor: filed, paths: []string{"README.md"}}

	commit := func(sha string, at time.Time, paths ...string) landedCommit {
		return landedCommit{sha: sha, at: at, subject: "Tidy", paths: paths}
	}

	older := commit("1111111111111111", filed.Add(time.Hour), "README.md")
	newer := commit("2222222222222222", filed.Add(2*time.Hour), "README.md")
	unmatched := commit("3333333333333333", filed.Add(3*time.Hour), "docs/other.md")

	cases := []struct {
		name     string
		landings []landing
		want     string
	}{
		{
			name: "equal scores report the newer commit",
			landings: []landing{
				{commit: older, evidence: []landedCommit{older}}, {commit: newer, evidence: []landedCommit{newer}},
			},
			want: newer.sha,
		},
		{
			name: "the merge share makes no match",
			landings: []landing{
				{commit: older, evidence: []landedCommit{older}}, {commit: unmatched, evidence: []landedCommit{unmatched}, merge: true},
			},
			want: older.sha,
		},
		{
			name:     "a path-only match scores half strength",
			landings: []landing{{commit: older, evidence: []landedCommit{older}}},
			want:     older.sha,
		},
	}

	mem := &memory{
		env:       Env{Embedders: func(embed.Config) (embed.Embedder, error) { return nil, embed.ErrNotConfigured }},
		embedding: &embed.Config{},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			found, _ := mem.matchLandings(context.Background(), []landedTarget{target}, tc.landings)
			if len(found) != 1 || found[0].Commit != tc.want {
				t.Fatalf("the highest score among the matches should be reported: %+v", found)
			}

			if tc.name == "a path-only match scores half strength" && math.Abs(found[0].Score-strengthWeight*strengthPath) > 1e-9 {
				t.Errorf("a path-only match should score at half strength: %+v", found[0])
			}
		})
	}
}
