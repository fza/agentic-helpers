package memory

import (
	"context"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
	"github.com/fza/agentic-helpers/source/hooks/test"
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

func TestATodosItemsAreRankedAgainstTheWholeCommit(t *testing.T) {
	filed := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	held := &entry{id: "m-aaaaaa", filed: filed}
	other := &entry{id: "m-bbbbbb", filed: filed}

	plain := landedCommit{
		sha: "1111111111111111", at: filed.Add(time.Hour), subject: "Tidy", message: "Tidy\n\nlanded-work",
		paths: []string{"README.md"},
	}
	byPath := landedCommit{
		sha: "2222222222222222", at: filed.Add(time.Hour), subject: "Tidy", message: "Tidy",
		paths: []string{"internal/landed-work.go"},
	}

	cases := []struct {
		name    string
		commit  landedCommit
		cosines []float32
		// apart files the second item under another todo.
		apart bool
		want  []string
	}{
		{name: "the closest item clear of its runner-up is flagged", commit: plain, cosines: []float32{0.67, 0.56}, want: []string{"m-aaaaaa#one"}},
		{name: "a runner-up within the margin of the closest item flags nothing", commit: plain, cosines: []float32{0.62, 0.60}},
		{name: "the closest item below the floor flags nothing", commit: plain, cosines: []float32{0.52, 0.30}},
		{name: "a lone item above the floor is flagged", commit: plain, cosines: []float32{0.58}, want: []string{"m-aaaaaa#one"}},
		{name: "a changed file name counts as the commit's text", commit: byPath, cosines: []float32{0.30, 0.66}, want: []string{"m-aaaaaa#two"}},
		{
			name: "the items of two todos never rank against each other", commit: plain, cosines: []float32{0.62, 0.60}, apart: true,
			want: []string{"m-aaaaaa#one", "m-bbbbbb#two"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			table := &test.VectorTable{Rows: []test.TableRow{{Key: "landed-work", Vector: test.Reference()}}}

			var targets []landedTarget

			for index, cosine := range tc.cosines {
				text := []string{"item one", "item two"}[index]
				table.Rows = append(table.Rows, test.TableRow{Key: text, Vector: test.Toward(cosine, 3+index)})

				todo := held
				if tc.apart && index == 1 {
					todo = other
				}

				targets = append(targets, landedTarget{
					address: todo.id + "#" + []string{"one", "two"}[index], held: todo, item: true, floor: filed, text: text,
				})
			}

			mem := &memory{
				env:       Env{Embedders: func(embed.Config) (embed.Embedder, error) { return table, nil }},
				embedding: &embed.Config{},
			}

			found, unjudged := mem.matchLandings(context.Background(), targets, []landing{{commit: tc.commit, evidence: []landedCommit{tc.commit}}})
			if len(unjudged) > 0 {
				t.Fatalf("a working embedder should judge every commit: %v", unjudged)
			}

			var flagged []string
			for _, mapping := range found {
				flagged = append(flagged, mapping.Target)
			}

			if !slices.Equal(flagged, tc.want) {
				t.Errorf("only the todo's item clearly closest to the whole commit should be flagged: %+v", found)
			}
		})
	}
}
