// Package levenshtein measures how far apart two words are by edit distance,
// to name the word a mistyped one probably means.
package levenshtein

import "strings"

func Distance(a, b string) int {
	left, right := []rune(a), []rune(b)
	previous := make([]int, len(right)+1)

	for index := range previous {
		previous[index] = index
	}

	for i := 1; i <= len(left); i++ {
		current := make([]int, len(right)+1)
		current[0] = i

		for j := 1; j <= len(right); j++ {
			cost := 1
			if left[i-1] == right[j-1] {
				cost = 0
			}

			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}

		previous = current
	}

	return previous[len(right)]
}

// Nearest is the candidate closest to word, ignoring case, the first listed
// among equals; empty where there is no candidate.
func Nearest(word string, candidates []string) string {
	best, distance := "", -1

	for _, candidate := range candidates {
		apart := Distance(strings.ToLower(word), strings.ToLower(candidate))
		if distance < 0 || apart < distance {
			best, distance = candidate, apart
		}
	}

	return best
}
