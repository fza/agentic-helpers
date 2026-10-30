package embed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// textMark is where a template takes the text, as sdd's templates mark it.
const textMark = "{text}"

// Templated embeds queries and documents each through its own template, the
// way the model was trained to tell them apart, and names its vectors by
// everything that shapes them.
type Templated struct {
	raw      Embedder
	query    string
	document string
}

func Template(raw Embedder, config Config) Templated {
	return Templated{raw: raw, query: config.QueryTemplate, document: config.DocumentTemplate}
}

// Queries embeds search phrases and prompts.
func (embedder Templated) Queries(ctx context.Context, texts []string) ([][]float32, error) {
	return embedder.raw.Embed(ctx, fill(embedder.query, texts))
}

// Documents embeds what is searched: entry chunks and tool descriptions.
func (embedder Templated) Documents(ctx context.Context, texts []string) ([][]float32, error) {
	return embedder.raw.Embed(ctx, fill(embedder.document, texts))
}

// Fingerprint names the vector space: the provider's model and dimensions,
// and a hash of both templates, since a template changes every vector it
// shapes. The endpoint stays out: the same model moved to another port
// answers the same vectors.
func (embedder Templated) Fingerprint() string {
	sum := sha256.Sum256([]byte(embedder.query + "\x00" + embedder.document))

	return embedder.raw.Fingerprint() + "/t" + hex.EncodeToString(sum[:])[:8]
}

func fill(template string, texts []string) []string {
	if template == "" || !strings.Contains(template, textMark) {
		return texts
	}

	filled := make([]string, len(texts))
	for index, text := range texts {
		filled[index] = strings.ReplaceAll(template, textMark, text)
	}

	return filled
}
