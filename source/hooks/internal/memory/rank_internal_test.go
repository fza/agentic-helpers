package memory

import (
	"cmp"
	"slices"
	"testing"
	"time"
)

func TestEqualHeatKeepsTheKeptOrder(t *testing.T) {
	filed := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	mem := &memory{env: Env{Home: t.TempDir(), CacheRoot: t.TempDir(), Now: func() time.Time { return filed }}}
	scope := &viewScope{mem: mem, folded: newState()}

	kept := []*entry{
		{id: "m-bbbbbb", filed: filed, retention: "normal"},
		{id: "m-aaaaaa", filed: filed, retention: "normal"},
	}

	got := best(kept, len(kept), scope.order(kept, rankHeat))
	if got[0].id != "m-bbbbbb" {
		t.Error("entries of equal heat and filing time should keep the order they were kept in, as a stable sort does")
	}
}

func TestBestKeepsTheFirstOfAStableSort(t *testing.T) {
	scores := []int{3, 1, 4, 1, 5, 9, 2, 6, 5, 3, 5, 8, 9, 7, 9, 3, 2, 3, 8, 4}
	items := make([]int, len(scores))

	for index := range items {
		items[index] = index
	}

	compare := func(a, b int) int { return cmp.Or(cmp.Compare(scores[b], scores[a]), cmp.Compare(a, b)) }

	sorted := slices.Clone(items)
	slices.SortStableFunc(sorted, func(a, b int) int { return cmp.Compare(scores[b], scores[a]) })

	for count := 0; count <= len(items)+1; count++ {
		got := best(items, count, compare)
		if !slices.Equal(got, sorted[:min(count, len(sorted))]) {
			t.Errorf("the bounded heap should keep the first of a stable sort, ties in their order, for %d: got %v", count, got)
		}
	}
}
