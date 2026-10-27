package memory

import (
	"cmp"
	"container/heap"
	"slices"
	"strings"
)

// order is a section's ranking as a comparison of two kept entries by their
// place among the kept, the earlier first among equals, as a stable sort
// keeps them: hottest first, the newest first among equals, for rank(heat);
// newest first otherwise. Each entry's heat is worked out once.
func (scope *viewScope) order(kept []*entry, rank string) func(a, b int) int {
	if rank == rankInDegree {
		linked := make([]int, len(kept))
		for index, held := range kept {
			linked[index] = len(held.linkedFrom(false))
		}

		return func(a, b int) int {
			return cmp.Or(cmp.Compare(linked[b], linked[a]), kept[b].filed.Compare(kept[a].filed), cmp.Compare(a, b))
		}
	}

	if rank == rankHeat {
		heats := make([]float64, len(kept))
		for index, held := range kept {
			heats[index] = scope.heat(held)
		}

		return func(a, b int) int {
			return cmp.Or(cmp.Compare(heats[b], heats[a]), kept[b].filed.Compare(kept[a].filed), cmp.Compare(a, b))
		}
	}

	return func(a, b int) int {
		return cmp.Or(kept[b].filed.Compare(kept[a].filed), strings.Compare(kept[a].id, kept[b].id), cmp.Compare(a, b))
	}
}

// best is the first count of items in the order compare gives their indices.
// A heap bounded to count keeps them, so ranking a section cut by n costs no
// sort of everything it kept.
func best[T any](items []T, count int, compare func(a, b int) int) []T {
	kept := &boundedHeap{compare: compare}

	for index := range items {
		switch {
		case len(kept.indices) < count:
			heap.Push(kept, index)
		case count > 0 && compare(index, kept.indices[0]) < 0:
			kept.indices[0] = index
			heap.Fix(kept, 0)
		}
	}

	slices.SortFunc(kept.indices, compare)

	ordered := make([]T, 0, len(kept.indices))
	for _, index := range kept.indices {
		ordered = append(ordered, items[index])
	}

	return ordered
}

// boundedHeap holds indices with the one ranked last on top, the first to go
// when a better one comes.
type boundedHeap struct {
	indices []int
	compare func(a, b int) int
}

func (held *boundedHeap) Len() int {
	return len(held.indices)
}

func (held *boundedHeap) Less(i, j int) bool {
	return held.compare(held.indices[i], held.indices[j]) > 0
}

func (held *boundedHeap) Swap(i, j int) {
	held.indices[i], held.indices[j] = held.indices[j], held.indices[i]
}

func (held *boundedHeap) Push(value any) {
	held.indices = append(held.indices, value.(int))
}

func (held *boundedHeap) Pop() any {
	last := held.indices[len(held.indices)-1]
	held.indices = held.indices[:len(held.indices)-1]

	return last
}
