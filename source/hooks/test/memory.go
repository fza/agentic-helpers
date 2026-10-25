package test

import (
	"bytes"
	"context"
	"errors"
	"hash/fnv"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
)

// Git answers the three questions agentic-memory asks git: the checkout's top
// level, its HEAD commit and its branch.
type Git struct {
	Top    string
	Commit string
	Branch string
}

func (fake *Git) Output(_ context.Context, _ string, args ...string) (string, error) {
	switch strings.Join(args, " ") {
	case "rev-parse --show-toplevel":
		return fake.Top + "\n", nil
	case "rev-parse --short=8 HEAD":
		return fake.Commit + "\n", nil
	case "rev-parse --abbrev-ref HEAD":
		return fake.Branch + "\n", nil
	}

	return "", errors.New("unexpected git call")
}

// Embedder maps a text to a bag of its words hashed into a small vector, so
// texts sharing words score close and the result is deterministic.
type Embedder struct {
	Err   error
	mutex sync.Mutex
	calls int
}

const embedDims = 64

func (fake *Embedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	fake.mutex.Lock()
	fake.calls++
	fake.mutex.Unlock()

	if fake.Err != nil {
		return nil, fake.Err
	}

	vectors := make([][]float32, 0, len(texts))
	for _, text := range texts {
		vectors = append(vectors, bagOfWords(text))
	}

	return vectors, nil
}

func (fake *Embedder) Calls() int {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()

	return fake.calls
}

func (fake *Embedder) Fingerprint() string {
	return "fake/64"
}

func bagOfWords(text string) []float32 {
	vector := make([]float32, embedDims)

	for _, word := range strings.Fields(strings.ToLower(text)) {
		word = strings.Trim(word, ".,:;()'\"`")
		if len(word) < 3 || word == "entry" {
			continue
		}

		hash := fnv.New32a()
		_, _ = hash.Write([]byte(word))
		vector[hash.Sum32()%embedDims]++
	}

	norm := 0.0
	for _, value := range vector {
		norm += float64(value * value)
	}

	if norm > 0 {
		scale := float32(1 / math.Sqrt(norm))
		for index := range vector {
			vector[index] *= scale
		}
	}

	return vector
}

// Factory hands out the fake, or refuses like an unconfigured provider.
func (fake *Embedder) Factory(configured bool) func(embed.Config) (embed.Embedder, error) {
	return func(embed.Config) (embed.Embedder, error) {
		if !configured {
			return nil, embed.ErrNotConfigured
		}

		return fake, nil
	}
}

type Clock struct {
	mutex sync.Mutex
	now   time.Time
}

func NewClock(start time.Time) *Clock {
	return &Clock{now: start}
}

func (clock *Clock) Now() time.Time {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()

	clock.now = clock.now.Add(time.Second)

	return clock.now
}

func (clock *Clock) Advance(by time.Duration) {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()

	clock.now = clock.now.Add(by)
}

// Random is an endless deterministic byte stream for entry ids.
func Random() *bytes.Reader {
	data := make([]byte, 1<<16)
	state := uint32(2463534242)

	for index := range data {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		data[index] = byte(state)
	}

	return bytes.NewReader(data)
}
