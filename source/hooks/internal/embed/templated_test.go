package embed_test

import (
	"context"
	"slices"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
)

// recording keeps every text it is asked to embed.
type recording struct {
	fingerprint string
	texts       []string
}

func (raw *recording) Embed(_ context.Context, texts []string) ([][]float32, error) {
	raw.texts = append(raw.texts, texts...)

	return make([][]float32, len(texts)), nil
}

func (raw *recording) Fingerprint() string {
	return raw.fingerprint
}

func TestQueriesAndDocumentsGoThroughTheirOwnTemplate(t *testing.T) {
	raw := &recording{}
	embedder := embed.Template(raw, embed.Config{QueryTemplate: "query: {text}", DocumentTemplate: "text: {text}"})

	_, _ = embedder.Queries(context.Background(), []string{"find this"})
	_, _ = embedder.Documents(context.Background(), []string{"an entry", "a tool"})

	if !slices.Equal(raw.texts, []string{"query: find this", "text: an entry", "text: a tool"}) {
		t.Errorf("a query and a document should each be wrapped in their template, got %q", raw.texts)
	}
}

func TestNoTemplateEmbedsTheTextAsItIs(t *testing.T) {
	raw := &recording{}
	embedder := embed.Template(raw, embed.Config{QueryTemplate: "without the mark"})

	_, _ = embedder.Queries(context.Background(), []string{"plain"})
	_, _ = embedder.Documents(context.Background(), []string{"also plain"})

	if !slices.Equal(raw.texts, []string{"plain", "also plain"}) {
		t.Errorf("a template without {text}, or none, should leave the text as it is, got %q", raw.texts)
	}
}

func TestTheFingerprintFollowsTheTemplatesButNotTheEndpoint(t *testing.T) {
	raw := &recording{fingerprint: "openai/model/0"}
	base := embed.Template(raw, embed.Config{Endpoint: "http://127.0.0.1:1235", QueryTemplate: "q: {text}"})
	moved := embed.Template(raw, embed.Config{Endpoint: "http://127.0.0.1:9999", QueryTemplate: "q: {text}"})
	query := embed.Template(raw, embed.Config{QueryTemplate: "query: {text}"})
	document := embed.Template(raw, embed.Config{QueryTemplate: "q: {text}", DocumentTemplate: "d: {text}"})

	if base.Fingerprint() != moved.Fingerprint() {
		t.Error("moving the endpoint should keep the fingerprint")
	}

	if base.Fingerprint() == query.Fingerprint() || base.Fingerprint() == document.Fingerprint() {
		t.Error("changing either template should change the fingerprint")
	}
}
