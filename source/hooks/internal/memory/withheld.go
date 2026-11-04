package memory

import (
	"context"
	"math"
	"strings"
	"unicode"
)

func (mem *memory) recallScores(ctx context.Context, folded *state, live []*entry, prompt string, session string,
	withheld bool,
) (map[string]float32, error) {
	if withheld {
		return termScores(live, prompt), nil
	}

	return mem.semanticFor(ctx, folded, prompt, session)
}

// termScores sums, for each entry, the distinct words of the prompt its title
// and body hold, each weighed by its inverse document frequency over the live
// entries, ln(N/df): recall's scores for a prompt that never reaches the
// embedder. A word every entry holds, "the" or a project's own name, weighs
// nothing; a word one entry holds weighs most. The weights come from the
// entries recall already holds, in any language they are written in, where a
// fixed stopword list covers one language and none of a store's own common
// words.
func termScores(live []*entry, prompt string) map[string]float32 {
	words := map[string]bool{}

	for _, word := range strings.FieldsFunc(strings.ToLower(prompt), func(char rune) bool {
		return !unicode.IsLetter(char) && !unicode.IsDigit(char)
	}) {
		words[word] = true
	}

	texts := make([]string, len(live))
	for index, held := range live {
		texts[index] = strings.ToLower(held.title + "\n" + held.body)
	}

	scores := make(map[string]float32, len(live))
	holders := make([]int, 0, len(live))

	for word := range words {
		holders = holders[:0]

		for index, text := range texts {
			if strings.Contains(text, word) {
				holders = append(holders, index)
			}
		}

		weight := float32(math.Log(float64(len(live)) / float64(len(holders))))

		for _, index := range holders {
			scores[live[index].id] += weight
		}
	}

	return scores
}
