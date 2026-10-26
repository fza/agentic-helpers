package memory

import (
	"context"
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

// termScores counts, for each entry, the distinct words of the prompt its title
// and body hold: recall's scores for a prompt that never reaches the embedder.
func termScores(live []*entry, prompt string) map[string]float32 {
	words := map[string]bool{}

	for _, word := range strings.FieldsFunc(strings.ToLower(prompt), func(char rune) bool {
		return !unicode.IsLetter(char) && !unicode.IsDigit(char)
	}) {
		words[word] = true
	}

	scores := make(map[string]float32, len(live))

	for _, held := range live {
		text := strings.ToLower(held.title + "\n" + held.body)

		for word := range words {
			if strings.Contains(text, word) {
				scores[held.id]++
			}
		}
	}

	return scores
}
