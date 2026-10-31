// Package embed turns text into L2-normalized vectors through an OpenAI-compatible
// or an Ollama embedding endpoint, and cuts an entry into the chunks it embeds.
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Config struct {
	Provider string
	Model    string
	Endpoint string
	// Path is the OpenAI-compatible route joined to Endpoint, `/v1/embeddings`
	// where empty; Ollama keeps `/api/embed`.
	Path       string
	Dimensions int
	APIKey     string
	// Timeout bounds one embedding call of a command or MCP tool.
	Timeout time.Duration
	// QueryTemplate / DocumentTemplate wrap a query and a document, `{text}`
	// marking where the text goes; Template applies them.
	QueryTemplate    string
	DocumentTemplate string
}

var ErrNotConfigured = errors.New("no embedding provider configured")

type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Fingerprint() string
}

const errorBodyLimit = 200

func New(cfg Config, client *http.Client) (Embedder, error) {
	if cfg.Provider == "" {
		return nil, ErrNotConfigured
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("no embedding model configured for %q", cfg.Provider)
	}
	if client == nil {
		client = http.DefaultClient
	}
	switch cfg.Provider {
	case "openai":
		return newOpenAI(cfg, client), nil
	case "ollama":
		return newOllama(cfg, client), nil
	default:
		return nil, fmt.Errorf("unknown embedding provider %q (supported: openai, ollama)", cfg.Provider)
	}
}

func Cosine(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	var sum float32
	for index := range a {
		sum += a[index] * b[index]
	}
	return sum
}

func endpointOr(configured string, fallback string) string {
	if configured == "" {
		return fallback
	}
	return strings.TrimRight(configured, "/")
}

// RequestTexts is the most texts one request carries with either provider, so
// a caller saving vectors per group of that size saves them per request.
const RequestTexts = min(openAIBatchSize, ollamaBatchSize)

func inBatches(
	ctx context.Context,
	texts []string,
	size int,
	embedBatch func(ctx context.Context, batch []string) ([][]float32, error),
) ([][]float32, error) {
	vectors := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += size {
		batch := texts[start:min(start+size, len(texts))]
		batchVectors, err := embedBatch(ctx, batch)
		if err != nil {
			return nil, err
		}
		if len(batchVectors) != len(batch) {
			return nil, fmt.Errorf("%d vectors for %d texts", len(batchVectors), len(batch))
		}
		for _, vector := range batchVectors {
			vectors = append(vectors, normalized(vector))
		}
	}
	return vectors, nil
}

func normalized(vector []float32) []float32 {
	var squares float64
	for _, component := range vector {
		squares += float64(component) * float64(component)
	}
	if squares == 0 {
		return vector
	}
	norm := math.Sqrt(squares)
	result := make([]float32, len(vector))
	for index, component := range vector {
		result[index] = float32(float64(component) / norm)
	}
	return result
}

func post(ctx context.Context, client *http.Client, address string, apiKey string, payload any, answer any) (err error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encoding the request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building the request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}

	response, err := client.Do(request)
	if err != nil {
		// url.Error repeats the method and the full address ahead of the cause; the
		// caller already names the endpoint, so only the cause is kept.
		var urlError *url.Error
		if errors.As(err, &urlError) {
			return urlError.Err
		}
		return err
	}
	defer func() {
		err = errors.Join(err, response.Body.Close())
	}()

	if response.StatusCode < 200 || response.StatusCode > 299 {
		excerpt, _ := io.ReadAll(io.LimitReader(response.Body, errorBodyLimit))
		excerpt = bytes.TrimSpace(excerpt)
		if len(excerpt) == 0 {
			return fmt.Errorf("status %d", response.StatusCode)
		}
		return fmt.Errorf("status %d: %s", response.StatusCode, excerpt)
	}

	err = json.NewDecoder(response.Body).Decode(answer)
	if err != nil {
		return fmt.Errorf("decoding the answer: %w", err)
	}
	return nil
}
