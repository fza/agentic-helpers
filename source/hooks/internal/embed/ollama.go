package embed

import (
	"context"
	"fmt"
	"net/http"
)

const (
	ollamaEndpoint  = "http://localhost:11434"
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
	Embeddings [][]float32 `json:"embeddings"`
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

func (embedder *ollama) embedBatch(ctx context.Context, batch []string) ([][]float32, error) {
	var answer ollamaAnswer
	err := post(ctx, embedder.client, embedder.endpoint+"/api/embed", "", ollamaRequest{Model: embedder.model, Input: batch, Dimensions: embedder.dimensions}, &answer)
	if err != nil {
		return nil, err
	}
	return answer.Embeddings, nil
}
