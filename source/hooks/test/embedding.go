package test

import (
	"context"
	"sync"

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
)

// SddEmbedding stands in for `sdd config get embedding`: it prints Block and
// counts each call, every one a process the real reader would start.
type SddEmbedding struct {
	Block string
	mutex sync.Mutex
	asked int
}

func (fake *SddEmbedding) EmbeddingConfig(_ context.Context, _ string) string {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()

	fake.asked++

	return fake.Block
}

func (fake *SddEmbedding) Asked() int {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()

	return fake.asked
}

// SettledEmbedders hands out fake for a settled configuration naming a
// provider and refuses one naming none, as embed.New does.
func SettledEmbedders(fake *Embedder) func(embed.Config) (embed.Embedder, error) {
	return func(config embed.Config) (embed.Embedder, error) {
		if config.Provider == "" {
			return nil, embed.ErrNotConfigured
		}

		return fake, nil
	}
}
