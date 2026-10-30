package test

import (
	"cmp"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
)

// Releases stands for GitHub's releases API: every request is answered with
// Status (200 where zero) and a body naming Tag, or Body where set, or fails
// with Err.
type Releases struct {
	Tag    string
	Status int
	Body   string
	Err    error

	mutex    sync.Mutex
	requests []*http.Request
}

func (fake *Releases) Do(request *http.Request) (*http.Response, error) {
	fake.mutex.Lock()
	fake.requests = append(fake.requests, request)
	fake.mutex.Unlock()

	if fake.Err != nil {
		return nil, fake.Err
	}

	body := fake.Body
	if body == "" {
		encoded, _ := json.Marshal(map[string]string{"tag_name": fake.Tag, "name": fake.Tag})
		body = string(encoded)
	}

	return &http.Response{
		StatusCode: cmp.Or(fake.Status, http.StatusOK),
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}, nil
}

// Requests is every request the fake was sent.
func (fake *Releases) Requests() []*http.Request {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()

	return append([]*http.Request(nil), fake.requests...)
}
