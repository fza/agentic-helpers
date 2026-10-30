package embed

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

const (
	openAIEndpoint  = "https://api.openai.com"
	openAIBatchSize = 100
)

type openAI struct {
	endpoint   string
	model      string
	dimensions int
	apiKey     string
	client     *http.Client
}

type openAIRequest struct {
	Model          string   `json:"model"`
	Input          []string `json:"input"`
	EncodingFormat string   `json:"encoding_format"`
	Dimensions     int      `json:"dimensions,omitempty"`
}

type openAIAnswer struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

func newOpenAI(cfg Config, client *http.Client) *openAI {
	return &openAI{
		endpoint:   endpointOr(cfg.Endpoint, openAIEndpoint),
		model:      cfg.Model,
		dimensions: cfg.Dimensions,
		apiKey:     cfg.APIKey,
		client:     client,
	}
}

func (embedder *openAI) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	vectors, err := inBatches(ctx, texts, openAIBatchSize, embedder.embedBatch)
	if err != nil {
		return nil, fmt.Errorf("embedding with %s: %w", embedder.endpoint, err)
	}
	return vectors, nil
}

func (embedder *openAI) Fingerprint() string {
	return fmt.Sprintf("openai/%s/%d", embedder.model, embedder.dimensions)
}

func (embedder *openAI) address() string {
	if strings.HasSuffix(embedder.endpoint, "/v1") {
		return embedder.endpoint + "/embeddings"
	}
	return embedder.endpoint + "/v1/embeddings"
}

func (embedder *openAI) embedBatch(ctx context.Context, batch []string) ([][]float32, error) {
	payload := openAIRequest{
		Model:          embedder.model,
		Input:          batch,
		EncodingFormat: "float",
		Dimensions:     embedder.dimensions,
	}
	var answer openAIAnswer
	err := post(ctx, embedder.client, embedder.address(), embedder.apiKey, payload, &answer)
	if err != nil {
		return nil, err
	}
	if len(answer.Data) != len(batch) {
		return nil, fmt.Errorf("%d vectors for %d texts", len(answer.Data), len(batch))
	}

	vectors := make([][]float32, len(batch))
	for _, datum := range answer.Data {
		if datum.Index < 0 || datum.Index >= len(batch) || vectors[datum.Index] != nil {
			return nil, fmt.Errorf("answer index %d is out of range or repeated", datum.Index)
		}
		vectors[datum.Index] = datum.Embedding
	}
	return vectors, nil
}
