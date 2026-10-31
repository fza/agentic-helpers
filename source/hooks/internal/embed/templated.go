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
// everything that shapes them. A cap set through Capped cuts every text to
// it before the template wraps it.
type Templated struct {
	raw      Embedder
	query    string
	document string
	maxRunes int
}

func Template(raw Embedder, config Config) Templated {
	return Templated{raw: raw, query: config.QueryTemplate, document: config.DocumentTemplate}
}

// Capped cuts every text it embeds to maxRunes runes, the cap of the vector
// store the vectors go to or are compared with.
func (embedder Templated) Capped(maxRunes int) Templated {
	embedder.maxRunes = maxRunes

	return embedder
}

// Queries embeds search phrases and prompts.
func (embedder Templated) Queries(ctx context.Context, texts []string) ([][]float32, error) {
	return embedder.raw.Embed(ctx, fill(embedder.query, embedder.cut(texts)))
}

// Documents embeds what is searched: entry chunks and tool descriptions.
func (embedder Templated) Documents(ctx context.Context, texts []string) ([][]float32, error) {
	return embedder.raw.Embed(ctx, fill(embedder.document, embedder.cut(texts)))
}

// Fingerprint names the vector space: the provider's model and dimensions,
// and a hash of both templates, since a template changes every vector it
// shapes. The endpoint, its path and the cap stay out: the same model behind
// another route answers the same vectors, and a row's hash carries its cap.
func (embedder Templated) Fingerprint() string {
	sum := sha256.Sum256([]byte(embedder.query + "\x00" + embedder.document))

	return embedder.raw.Fingerprint() + "/t" + hex.EncodeToString(sum[:])[:8]
}

// MaxRunes is the cap a fill sizes its texts to: the calibration stored for
// this endpoint below the cache root, measured now where none is. An embedder
// that cannot measure gets FallbackRunes, as a provider reporting no count
// does. A probe that fails, or a stored calibration that does not read, is an
// error: no fill guesses a cap.
func (embedder Templated) MaxRunes(ctx context.Context, cache string) (int, error) {
	measurer, measures := embedder.raw.(Measurer)
	if !measures {
		return FallbackRunes, nil
	}

	endpoint, path := measurer.Route()
	key := CalibrationKey(embedder.Fingerprint(), endpoint, path)

	calibration, err := LoadOrCalibrate(ctx, documentMeasurer{measurer: measurer, template: embedder.document}, cache, key)
	if err != nil {
		return 0, err
	}

	return calibration.MaxRunes, nil
}

// documentMeasurer probes through the document template, as sdd probes with a
// document's purpose, so both tools measure the same request.
type documentMeasurer struct {
	measurer Measurer
	template string
}

func (probe documentMeasurer) Measure(ctx context.Context, text string) (int, error) {
	return probe.measurer.Measure(ctx, fill(probe.template, []string{text})[0])
}

func (probe documentMeasurer) Route() (string, string) {
	return probe.measurer.Route()
}

func (embedder Templated) cut(texts []string) []string {
	if embedder.maxRunes <= 0 {
		return texts
	}

	cut := make([]string, len(texts))
	for index, text := range texts {
		cut[index] = Cut(text, embedder.maxRunes)
	}

	return cut
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
