package embed

import (
	"context"
	"fmt"
	"net/http"
)

const (
	ollamaEndpoint  = "http://localhost:11434"
	ollamaPath      = "/api/embed"
	ollamaBatchSize = 64
)

type ollama struct {
	endpoint   string
	model      string
	dimensions int
	client     *http.Client
}

// ollamaRequest asks for dimensions only where some are set: a server that
// cannot truncate ignores the field.
type ollamaRequest struct {
	Model      string   `json:"model"`
	Input      []string `json:"input"`
	Dimensions int      `json:"dimensions,omitempty"`
}

type ollamaAnswer struct {
	Embeddings      [][]float32 `json:"embeddings"`
	PromptEvalCount int         `json:"prompt_eval_count"`
}

func newOllama(cfg Config, client *http.Client) *ollama {
	return &ollama{
		endpoint:   endpointOr(cfg.Endpoint, ollamaEndpoint),
		model:      cfg.Model,
		dimensions: cfg.Dimensions,
		client:     client,
	}
}

func (embedder *ollama) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	vectors, err := inBatches(ctx, texts, ollamaBatchSize, embedder.embedBatch)
	if err != nil {
		return nil, fmt.Errorf("embedding with %s: %w", embedder.endpoint, err)
	}
	return vectors, nil
}

func (embedder *ollama) Fingerprint() string {
	return fmt.Sprintf("ollama/%s/%d", embedder.model, embedder.dimensions)
}

func (embedder *ollama) Route() (string, string) {
	return embedder.endpoint, ollamaPath
}

func (embedder *ollama) Measure(ctx context.Context, text string) (int, error) {
	answer, err := embedder.request(ctx, []string{text})
	if err != nil {
		return 0, fmt.Errorf("measuring with %s: %w", embedder.endpoint, err)
	}

	return answer.PromptEvalCount, nil
}

func (embedder *ollama) embedBatch(ctx context.Context, batch []string) ([][]float32, error) {
	answer, err := embedder.request(ctx, batch)
	if err != nil {
		return nil, err
	}
	return answer.Embeddings, nil
}

func (embedder *ollama) request(ctx context.Context, batch []string) (ollamaAnswer, error) {
	var answer ollamaAnswer
	err := post(ctx, embedder.client, embedder.endpoint+ollamaPath, "", ollamaRequest{Model: embedder.model, Input: batch, Dimensions: embedder.dimensions}, &answer)
	return answer, err
}
