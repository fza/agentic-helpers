package embed

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

const (
	openAIEndpoint  = "https://api.openai.com"
	openAIPath      = "/v1/embeddings"
	openAIBatchSize = 100
)

type openAI struct {
	endpoint   string
	path       string
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
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
	} `json:"usage"`
}

func newOpenAI(cfg Config, client *http.Client) *openAI {
	endpoint := endpointOr(cfg.Endpoint, openAIEndpoint)

	return &openAI{
		endpoint:   endpoint,
		path:       openAIRoute(endpoint, cfg.Path),
		model:      cfg.Model,
		dimensions: cfg.Dimensions,
		apiKey:     cfg.APIKey,
		client:     client,
	}
}

// openAIRoute is the configured path, else `/v1/embeddings`. An endpoint
// already ending in `/v1` with no path set keeps the `/embeddings` it was
// given before the path was configurable, so no existing setting breaks.
func openAIRoute(endpoint string, path string) string {
	if path != "" {
		return "/" + strings.TrimLeft(path, "/")
	}

	if strings.HasSuffix(endpoint, "/v1") {
		return "/embeddings"
	}

	return openAIPath
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

func (embedder *openAI) Route() (string, string) {
	return embedder.endpoint, embedder.path
}

func (embedder *openAI) Measure(ctx context.Context, text string) (int, error) {
	_, tokens, err := embedder.request(ctx, []string{text})
	if err != nil {
		return 0, fmt.Errorf("measuring with %s: %w", embedder.endpoint, err)
	}

	return tokens, nil
}

func (embedder *openAI) embedBatch(ctx context.Context, batch []string) ([][]float32, error) {
	vectors, _, err := embedder.request(ctx, batch)

	return vectors, err
}

func (embedder *openAI) request(ctx context.Context, batch []string) ([][]float32, int, error) {
	payload := openAIRequest{
		Model:          embedder.model,
		Input:          batch,
		EncodingFormat: "float",
		Dimensions:     embedder.dimensions,
	}
	var answer openAIAnswer
	err := post(ctx, embedder.client, embedder.endpoint+embedder.path, embedder.apiKey, payload, &answer)
	if err != nil {
		return nil, 0, err
	}
	if len(answer.Data) != len(batch) {
		return nil, 0, fmt.Errorf("%d vectors for %d texts", len(answer.Data), len(batch))
	}

	vectors := make([][]float32, len(batch))
	for _, datum := range answer.Data {
		if datum.Index < 0 || datum.Index >= len(batch) || vectors[datum.Index] != nil {
			return nil, 0, fmt.Errorf("answer index %d is out of range or repeated", datum.Index)
		}
		vectors[datum.Index] = datum.Embedding
	}
	return vectors, answer.Usage.PromptTokens, nil
}
