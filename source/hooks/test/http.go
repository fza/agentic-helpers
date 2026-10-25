package test

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

type RoundTrip func(request *http.Request) (*http.Response, error)

func (answer RoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return answer(request)
}

// EmbedRequest is one request an embedding client sent, decoded.
type EmbedRequest struct {
	Method  string
	Address string
	Headers http.Header
	Body    map[string]any
	Inputs  []string
}

// Endpoint is a fake embedding endpoint behind an http.Client: it records
// every request and answers through Answer, AnswerByIndex where none is set.
type Endpoint struct {
	Requests []EmbedRequest
	Answer   func(request EmbedRequest) (int, string)
}

func (fake *Endpoint) Client(t *testing.T) *http.Client {
	t.Helper()

	return &http.Client{Transport: RoundTrip(func(request *http.Request) (*http.Response, error) {
		raw, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("the request body should be readable: %v", err)
		}

		var body map[string]any

		err = json.Unmarshal(raw, &body)
		if err != nil {
			t.Fatalf("the request body should be JSON: %v", err)
		}

		var payload struct {
			Input []string `json:"input"`
		}

		err = json.Unmarshal(raw, &payload)
		if err != nil {
			t.Fatalf("the request should carry its texts as input: %v", err)
		}

		exchange := EmbedRequest{
			Method: request.Method, Address: request.URL.String(), Headers: request.Header, Body: body,
			Inputs: payload.Input,
		}
		fake.Requests = append(fake.Requests, exchange)

		answer := fake.Answer
		if answer == nil {
			answer = AnswerByIndex
		}

		status, text := answer(exchange)

		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(text)),
			Request:    request,
		}, nil
	})}
}

// AnswerByIndex answers each text naming a number n with the vector (n+1, 1),
// so a test recovers which text a normalized vector belongs to from its ratio.
func AnswerByIndex(request EmbedRequest) (int, string) {
	vectors := make([][]float32, len(request.Inputs))

	for index, text := range request.Inputs {
		number, err := strconv.Atoi(text)
		if err != nil {
			number = 0
		}

		vectors[index] = []float32{float32(number + 1), 1}
	}

	return http.StatusOK, EncodeEmbeddings(request, vectors)
}

// EncodeEmbeddings shapes vectors the way the endpoint a request went to
// answers: Ollama's `/api/embed`, else OpenAI's.
func EncodeEmbeddings(request EmbedRequest, vectors [][]float32) string {
	var answer any

	if strings.HasSuffix(request.Address, "/api/embed") {
		answer = map[string]any{"embeddings": vectors}
	} else {
		data := make([]map[string]any, len(vectors))
		for index, vector := range vectors {
			data[index] = map[string]any{"index": index, "embedding": vector}
		}

		answer = map[string]any{"data": data}
	}

	encoded, _ := json.Marshal(answer)

	return string(encoded)
}

// FailingWriter refuses every write with Err.
type FailingWriter struct {
	Err error
}

func (writer FailingWriter) Write([]byte) (int, error) {
	return 0, writer.Err
}
