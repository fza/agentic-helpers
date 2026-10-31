package test

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"unicode/utf8"
)

// TokenLimit answers a fake embedding endpoint the way a model server with an
// input limit does: each input over Limit tokens is refused (Refuse) or cut to
// its head and embedded without a word, the way rapid-mlx and many OpenAI-
// compatible servers answer. Each answer reports the tokens counted, as
// OpenAI's usage.prompt_tokens or Ollama's prompt_eval_count, unless
// Unreported. Tokens are counted by Tokens.
type TokenLimit struct {
	Limit      int
	Refuse     bool
	Unreported bool
	mutex      sync.Mutex
	inputs     []string
	truncated  []string
}

// Tokens counts a text the way TokenLimit does: four ASCII runes to a token,
// every other rune a token of its own, as CJK text costs about one token a
// rune.
func Tokens(text string) int {
	ascii, other := 0, 0

	for _, char := range text {
		if char < utf8.RuneSelf {
			ascii++
		} else {
			other++
		}
	}

	return (ascii+3)/4 + other
}

// head is the longest prefix of text counting at most limit tokens.
func head(text string, limit int) string {
	runes := []rune(text)
	low, high := 0, len(runes)

	for low < high {
		middle := (low + high + 1) / 2
		if Tokens(string(runes[:middle])) <= limit {
			low = middle
		} else {
			high = middle - 1
		}
	}

	return string(runes[:low])
}

func (limit *TokenLimit) Answer(request EmbedRequest) (int, string) {
	limit.mutex.Lock()
	defer limit.mutex.Unlock()

	vectors := make([][]float32, len(request.Inputs))
	counted := 0

	for index, text := range request.Inputs {
		limit.inputs = append(limit.inputs, text)

		tokens := Tokens(text)
		if tokens > limit.Limit {
			if limit.Refuse {
				return http.StatusBadRequest, `{"error":"input exceeds the model's context length"}`
			}

			limit.truncated = append(limit.truncated, text)
			text = head(text, limit.Limit)
			tokens = limit.Limit
		}

		counted += tokens
		vectors[index] = bagOfWords(text)
	}

	if limit.Unreported {
		counted = 0
	}

	return http.StatusOK, EncodeCounted(request, vectors, counted)
}

// Inputs is every input the endpoint was sent, probes included.
func (limit *TokenLimit) Inputs() []string {
	limit.mutex.Lock()
	defer limit.mutex.Unlock()

	return append([]string(nil), limit.inputs...)
}

// Truncated is every input past the calibration probes the endpoint cut
// silently: the overflow probe is cut by design.
func (limit *TokenLimit) Truncated() []string {
	limit.mutex.Lock()
	defer limit.mutex.Unlock()

	var cut []string

	for _, text := range limit.truncated {
		if !IsProbe(text) {
			cut = append(cut, text)
		}
	}

	return cut
}

// Largest is the most tokens any input sent past the probes held: inputs of
// the probe texts are left out.
func (limit *TokenLimit) Largest() int {
	limit.mutex.Lock()
	defer limit.mutex.Unlock()

	largest := 0

	for _, text := range limit.inputs {
		if IsProbe(text) {
			continue
		}

		largest = max(largest, Tokens(text))
	}

	return largest
}

// IsProbe tells a calibration probe, a text of one repeated unit, from a text
// sent to be embedded.
func IsProbe(text string) bool {
	return strings.Trim(text, "word ") == "" || strings.Trim(text, "字") == ""
}

// EncodeCounted shapes vectors as EncodeEmbeddings does, with the tokens the
// provider counted: usage.prompt_tokens for OpenAI, prompt_eval_count for Ollama.
func EncodeCounted(request EmbedRequest, vectors [][]float32, tokens int) string {
	var answer any

	if strings.HasSuffix(request.Address, "/api/embed") {
		answer = map[string]any{"embeddings": vectors, "prompt_eval_count": tokens}
	} else {
		data := make([]map[string]any, len(vectors))
		for index, vector := range vectors {
			data[index] = map[string]any{"index": index, "embedding": vector}
		}

		answer = map[string]any{"data": data, "usage": map[string]any{"prompt_tokens": tokens, "total_tokens": tokens}}
	}

	encoded, _ := json.Marshal(answer)

	return string(encoded)
}
