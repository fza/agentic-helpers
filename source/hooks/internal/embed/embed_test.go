package embed_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func newEmbedder(t *testing.T, cfg embed.Config, client *http.Client) embed.Embedder {
	t.Helper()
	embedder, err := embed.New(cfg, client)
	if err != nil {
		t.Fatalf("the embedder should be created: %v", err)
	}
	return embedder
}

func numberedTexts(count int) []string {
	texts := make([]string, count)
	for index := range texts {
		texts[index] = strconv.Itoa(index)
	}
	return texts
}

func TestRequestShape(t *testing.T) {
	cases := []struct {
		name          string
		cfg           embed.Config
		address       string
		authorization string
		body          string
	}{
		{
			name:          "openai on its default endpoint with a key",
			cfg:           embed.Config{Provider: "openai", Model: "text-embedding-3-small", APIKey: "secret"},
			address:       "https://api.openai.com/v1/embeddings",
			authorization: "Bearer secret",
			body:          `{"model":"text-embedding-3-small","input":["a","b"],"encoding_format":"float"}`,
		},
		{
			name:    "openai-compatible endpoint without a key, with dimensions",
			cfg:     embed.Config{Provider: "openai", Model: "nomic", Endpoint: "http://127.0.0.1:1234/", Dimensions: 256},
			address: "http://127.0.0.1:1234/v1/embeddings",
			body:    `{"model":"nomic","input":["a","b"],"encoding_format":"float","dimensions":256}`,
		},
		{
			name:    "openai endpoint already ending in v1",
			cfg:     embed.Config{Provider: "openai", Model: "nomic", Endpoint: "http://127.0.0.1:1234/v1"},
			address: "http://127.0.0.1:1234/v1/embeddings",
			body:    `{"model":"nomic","input":["a","b"],"encoding_format":"float"}`,
		},
		{
			name:    "openai endpoint ending in v1 and a slash",
			cfg:     embed.Config{Provider: "openai", Model: "nomic", Endpoint: "http://127.0.0.1:1234/v1/"},
			address: "http://127.0.0.1:1234/v1/embeddings",
			body:    `{"model":"nomic","input":["a","b"],"encoding_format":"float"}`,
		},
		{
			name:    "openai behind a gateway naming its own path",
			cfg:     embed.Config{Provider: "openai", Model: "nomic", Endpoint: "https://api.opper.ai/", Path: "/v3/compat/embeddings"},
			address: "https://api.opper.ai/v3/compat/embeddings",
			body:    `{"model":"nomic","input":["a","b"],"encoding_format":"float"}`,
		},
		{
			name:    "openai path without its leading slash",
			cfg:     embed.Config{Provider: "openai", Model: "nomic", Endpoint: "http://127.0.0.1:1234/v1", Path: "embed"},
			address: "http://127.0.0.1:1234/v1/embed",
			body:    `{"model":"nomic","input":["a","b"],"encoding_format":"float"}`,
		},
		{
			name:    "ollama ignores a path",
			cfg:     embed.Config{Provider: "ollama", Model: "nomic-embed-text", Path: "/v3/compat/embeddings"},
			address: "http://localhost:11434/api/embed",
			body:    `{"model":"nomic-embed-text","input":["a","b"]}`,
		},
		{
			name:    "ollama on its default endpoint ignores a key and asks for dimensions",
			cfg:     embed.Config{Provider: "ollama", Model: "nomic-embed-text", APIKey: "secret", Dimensions: 256},
			address: "http://localhost:11434/api/embed",
			body:    `{"model":"nomic-embed-text","input":["a","b"],"dimensions":256}`,
		},
		{
			name:    "ollama endpoint with a trailing slash",
			cfg:     embed.Config{Provider: "ollama", Model: "nomic-embed-text", Endpoint: "http://gpu-box:11434/"},
			address: "http://gpu-box:11434/api/embed",
			body:    `{"model":"nomic-embed-text","input":["a","b"]}`,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fake := &test.Endpoint{}
			embedder := newEmbedder(t, testCase.cfg, fake.Client(t))

			_, err := embedder.Embed(t.Context(), []string{"a", "b"})
			if err != nil {
				t.Fatalf("embedding should succeed: %v", err)
			}
			if len(fake.Requests) != 1 {
				t.Fatalf("two texts should go out in a single request, got %d", len(fake.Requests))
			}
			request := fake.Requests[0]
			if request.Method != http.MethodPost {
				t.Errorf("the request should be a POST, got %s", request.Method)
			}
			if request.Address != testCase.address {
				t.Errorf("the request should go to the provider's embedding path, got %s", request.Address)
			}
			if request.Headers.Get("Content-Type") != "application/json" {
				t.Errorf("the request should declare a JSON body, got %q", request.Headers.Get("Content-Type"))
			}
			if request.Headers.Get("Authorization") != testCase.authorization {
				t.Errorf("the bearer header should be sent exactly when a key is configured, got %q", request.Headers.Get("Authorization"))
			}
			var want map[string]any
			err = json.Unmarshal([]byte(testCase.body), &want)
			if err != nil {
				t.Fatalf("the expected body should be JSON: %v", err)
			}
			if !reflect.DeepEqual(request.Body, want) {
				t.Errorf("the request body should carry the provider's fields and nothing else, got %v", request.Body)
			}
		})
	}
}

func TestBatching(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		texts    int
		batches  []int
	}{
		{name: "openai splits past a hundred texts", provider: "openai", texts: 250, batches: []int{100, 100, 50}},
		{name: "openai sends exactly a hundred at once", provider: "openai", texts: 100, batches: []int{100}},
		{name: "ollama splits past sixty-four texts", provider: "ollama", texts: 130, batches: []int{64, 64, 2}},
		{name: "ollama sends exactly sixty-four at once", provider: "ollama", texts: 64, batches: []int{64}},
		{name: "no texts send no request", provider: "ollama", texts: 0, batches: nil},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fake := &test.Endpoint{}
			embedder := newEmbedder(t, embed.Config{Provider: testCase.provider, Model: "model"}, fake.Client(t))

			vectors, err := embedder.Embed(t.Context(), numberedTexts(testCase.texts))
			if err != nil {
				t.Fatalf("embedding should succeed: %v", err)
			}
			var batches []int
			for _, request := range fake.Requests {
				batches = append(batches, len(request.Inputs))
			}
			if !reflect.DeepEqual(batches, testCase.batches) {
				t.Errorf("texts should be split at the provider's batch size, got %v", batches)
			}
			if len(vectors) != testCase.texts {
				t.Fatalf("there should be one vector per text, got %d", len(vectors))
			}
			for index, vector := range vectors {
				recovered := vector[0] / vector[1]
				if math.Abs(float64(recovered)-float64(index+1)) > 1e-3 {
					t.Fatalf("vector %d should belong to text %d, it belongs to text %v", index, index, recovered-1)
				}
			}
		})
	}
}

func TestOpenAIAnswerIsOrderedByIndex(t *testing.T) {
	fake := &test.Endpoint{Answer: func(request test.EmbedRequest) (int, string) {
		return http.StatusOK, `{"data":[{"index":2,"embedding":[0,0,1]},{"index":0,"embedding":[1,0,0]},{"index":1,"embedding":[0,1,0]}]}`
	}}
	embedder := newEmbedder(t, embed.Config{Provider: "openai", Model: "model"}, fake.Client(t))

	vectors, err := embedder.Embed(t.Context(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("embedding should succeed: %v", err)
	}
	want := [][]float32{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}
	if !reflect.DeepEqual(vectors, want) {
		t.Errorf("vectors should follow the answer's index, not its order, got %v", vectors)
	}
}

func TestNormalization(t *testing.T) {
	cases := []struct {
		name   string
		answer []float32
		want   []float32
	}{
		{name: "a vector is scaled to unit length", answer: []float32{3, 4}, want: []float32{0.6, 0.8}},
		{name: "a unit vector stays as it is", answer: []float32{0, 1}, want: []float32{0, 1}},
		{name: "a zero vector stays zero", answer: []float32{0, 0}, want: []float32{0, 0}},
	}
	for _, testCase := range cases {
		for _, provider := range []string{"openai", "ollama"} {
			t.Run(provider+": "+testCase.name, func(t *testing.T) {
				fake := &test.Endpoint{Answer: func(request test.EmbedRequest) (int, string) {
					return http.StatusOK, test.EncodeEmbeddings(request, [][]float32{testCase.answer})
				}}
				embedder := newEmbedder(t, embed.Config{Provider: provider, Model: "model"}, fake.Client(t))

				vectors, err := embedder.Embed(t.Context(), []string{"a"})
				if err != nil {
					t.Fatalf("embedding should succeed: %v", err)
				}
				for index, component := range vectors[0] {
					difference := math.Abs(float64(component - testCase.want[index]))
					if math.IsNaN(difference) || difference > 1e-6 {
						t.Fatalf("the vector should come back L2-normalized, got %v", vectors[0])
					}
				}
			})
		}
	}
}

func TestAnswerErrors(t *testing.T) {
	long := strings.Repeat("x", 500)
	cases := []struct {
		name     string
		provider string
		status   int
		body     string
		message  string
	}{
		{
			name:     "a refused key names the status and the body",
			provider: "openai",
			status:   http.StatusUnauthorized,
			body:     `{"error":{"message":"bad key"}}` + "\n",
			message:  `embedding with https://api.openai.com: status 401: {"error":{"message":"bad key"}}`,
		},
		{
			name:     "an empty error body names the status alone",
			provider: "ollama",
			status:   http.StatusInternalServerError,
			message:  "embedding with http://localhost:11434: status 500",
		},
		{
			name:     "a long error body is cut",
			provider: "ollama",
			status:   http.StatusNotFound,
			body:     long,
			message:  "embedding with http://localhost:11434: status 404: " + long[:200],
		},
		{
			name:     "a redirect is not an answer",
			provider: "openai",
			status:   http.StatusNotModified,
			message:  "embedding with https://api.openai.com: status 304",
		},
		{
			name:     "an informational status is not an answer",
			provider: "openai",
			status:   http.StatusProcessing,
			body:     `{"data":[]}`,
			message:  `embedding with https://api.openai.com: status 102: {"data":[]}`,
		},
		{
			name:     "openai answering fewer vectors than texts",
			provider: "openai",
			status:   http.StatusOK,
			body:     `{"data":[{"index":0,"embedding":[1]}]}`,
			message:  "embedding with https://api.openai.com: 1 vectors for 2 texts",
		},
		{
			name:     "openai repeating an index",
			provider: "openai",
			status:   http.StatusOK,
			body:     `{"data":[{"index":0,"embedding":[1]},{"index":0,"embedding":[1]}]}`,
			message:  "embedding with https://api.openai.com: answer index 0 is out of range or repeated",
		},
		{
			name:     "openai naming an index past the batch",
			provider: "openai",
			status:   http.StatusOK,
			body:     `{"data":[{"index":0,"embedding":[1]},{"index":2,"embedding":[1]}]}`,
			message:  "embedding with https://api.openai.com: answer index 2 is out of range or repeated",
		},
		{
			name:     "openai naming a negative index",
			provider: "openai",
			status:   http.StatusOK,
			body:     `{"data":[{"index":-1,"embedding":[1]},{"index":0,"embedding":[1]}]}`,
			message:  "embedding with https://api.openai.com: answer index -1 is out of range or repeated",
		},
		{
			name:     "ollama answering fewer vectors than texts",
			provider: "ollama",
			status:   http.StatusOK,
			body:     `{"embeddings":[[1]]}`,
			message:  "embedding with http://localhost:11434: 1 vectors for 2 texts",
		},
		{
			name:     "an answer that is not JSON",
			provider: "ollama",
			status:   http.StatusOK,
			body:     `<html>`,
			message:  "embedding with http://localhost:11434: decoding the answer: invalid character '<' looking for beginning of value",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fake := &test.Endpoint{Answer: func(test.EmbedRequest) (int, string) {
				return testCase.status, testCase.body
			}}
			embedder := newEmbedder(t, embed.Config{Provider: testCase.provider, Model: "model"}, fake.Client(t))

			vectors, err := embedder.Embed(t.Context(), []string{"a", "b"})
			if err == nil {
				t.Fatalf("a bad answer should be an error, got vectors %v", vectors)
			}
			if err.Error() != testCase.message {
				t.Errorf("the error should say what failed and where, got %q", err.Error())
			}
		})
	}
}

func TestTransportError(t *testing.T) {
	for _, provider := range []string{"openai", "ollama"} {
		t.Run(provider, func(t *testing.T) {
			client := &http.Client{Transport: test.RoundTrip(func(*http.Request) (*http.Response, error) {
				return nil, fmt.Errorf("dial tcp 127.0.0.1:1235: connect: %w", syscall.ECONNREFUSED)
			})}
			embedder := newEmbedder(t, embed.Config{Provider: provider, Model: "model", Endpoint: "http://127.0.0.1:1235"}, client)

			_, err := embedder.Embed(t.Context(), []string{"a"})
			if !errors.Is(err, syscall.ECONNREFUSED) {
				t.Fatalf("the transport's cause should survive the wrapping, got %v", err)
			}
			if err.Error() != "embedding with http://127.0.0.1:1235: dial tcp 127.0.0.1:1235: connect: connection refused" {
				t.Errorf("the error should name the endpoint and the cause only, got %q", err.Error())
			}
		})
	}
}

type contextMarker struct{}

func TestContextBoundsTheRequest(t *testing.T) {
	t.Run("the caller's context reaches the request", func(t *testing.T) {
		var carried any
		client := &http.Client{Transport: test.RoundTrip(func(request *http.Request) (*http.Response, error) {
			carried = request.Context().Value(contextMarker{})
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"embeddings":[[1]]}`))}, nil
		})}
		embedder := newEmbedder(t, embed.Config{Provider: "ollama", Model: "model"}, client)

		ctx := context.WithValue(t.Context(), contextMarker{}, "marked")
		_, err := embedder.Embed(ctx, []string{"a"})
		if err != nil {
			t.Fatalf("embedding should succeed: %v", err)
		}
		if carried != "marked" {
			t.Errorf("the request should run under the caller's context")
		}
	})

	t.Run("a cancelled context stops the default client before it dials", func(t *testing.T) {
		embedder := newEmbedder(t, embed.Config{Provider: "openai", Model: "model", Endpoint: "http://127.0.0.1:1"}, nil)

		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := embedder.Embed(ctx, []string{"a"})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("a cancelled context should end the embedding, got %v", err)
		}
	})
}

func TestNew(t *testing.T) {
	cases := []struct {
		name       string
		cfg        embed.Config
		unset      bool
		mentioning string
	}{
		{name: "no provider is not configured", cfg: embed.Config{Model: "model"}, unset: true},
		{name: "an unknown provider is named", cfg: embed.Config{Provider: "cohere", Model: "model"}, mentioning: `"cohere"`},
		{name: "a provider without a model", cfg: embed.Config{Provider: "openai"}, mentioning: "model"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := embed.New(testCase.cfg, nil)
			if err == nil {
				t.Fatalf("the configuration should be refused")
			}
			if errors.Is(err, embed.ErrNotConfigured) != testCase.unset {
				t.Errorf("only a missing provider should read as not configured, got %v", err)
			}
			if !strings.Contains(err.Error(), testCase.mentioning) {
				t.Errorf("the error should name what is wrong, got %q", err.Error())
			}
		})
	}
}

func TestFingerprint(t *testing.T) {
	cases := []struct {
		name        string
		cfg         embed.Config
		fingerprint string
	}{
		{name: "openai at the model's own size", cfg: embed.Config{Provider: "openai", Model: "text-embedding-3-small"}, fingerprint: "openai/text-embedding-3-small/0"},
		{name: "openai with dimensions", cfg: embed.Config{Provider: "openai", Model: "text-embedding-3-small", Dimensions: 512}, fingerprint: "openai/text-embedding-3-small/512"},
		{name: "ollama", cfg: embed.Config{Provider: "ollama", Model: "nomic-embed-text"}, fingerprint: "ollama/nomic-embed-text/0"},
		{name: "ollama with dimensions", cfg: embed.Config{Provider: "ollama", Model: "nomic-embed-text", Dimensions: 512}, fingerprint: "ollama/nomic-embed-text/512"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			embedder := newEmbedder(t, testCase.cfg, nil)
			if embedder.Fingerprint() != testCase.fingerprint {
				t.Errorf("the fingerprint should name provider, model and size, got %q", embedder.Fingerprint())
			}
		})
	}
}

func TestCosine(t *testing.T) {
	cases := []struct {
		name string
		a    []float32
		b    []float32
		want float32
	}{
		{name: "identical vectors", a: []float32{0.6, 0.8}, b: []float32{0.6, 0.8}, want: 1},
		{name: "orthogonal vectors", a: []float32{1, 0}, b: []float32{0, 1}, want: 0},
		{name: "opposite vectors", a: []float32{0.6, 0.8}, b: []float32{-0.6, -0.8}, want: -1},
		{name: "vectors of different lengths", a: []float32{1, 0}, b: []float32{1, 0, 0}, want: 0},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			similarity := embed.Cosine(testCase.a, testCase.b)
			if math.Abs(float64(similarity-testCase.want)) > 1e-6 {
				t.Errorf("the similarity should be the dot product of the vectors, got %v", similarity)
			}
		})
	}
}
