package memory

import (
	"context"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/fza/agentic-helpers/source/hooks/internal/promptvector"
	"os"
	"path/filepath"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
)

// embedLimit bounds one call to the embedder, so a hook never hangs on a model
// server that accepts the connection and never answers.
const embedLimit = 20 * time.Second

var ErrNoEmbedder = errors.New("semantic search needs an embedder")

type vectorRow struct {
	Hash   string
	Chunks [][]float32
}

type vectorStore struct {
	path string
	rows map[string]vectorRow
}

func (mem *memory) embedder() (embed.Embedder, error) {
	options := mem.options()

	config := embed.Config{
		Provider:   options.EmbeddingProvider,
		Model:      options.EmbeddingModel,
		Endpoint:   options.EmbeddingEndpoint,
		Dimensions: options.EmbeddingDimensions,
	}

	if options.EmbeddingAPIKeyEnv != "" && mem.env.Getenv != nil {
		config.APIKey = mem.env.Getenv(options.EmbeddingAPIKeyEnv)
	}

	embedder, err := mem.env.Embedders(config)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoEmbedder, err)
	}

	return embedder, nil
}

func contentHash(held *entry) string {
	sum := sha256.Sum256([]byte(held.title + "\n" + held.body))

	return hex.EncodeToString(sum[:])[:16]
}

// vectors opens the vector store of one embedder: a changed embedder starts a
// fresh store instead of mixing two vector spaces.
func (mem *memory) vectors(embedder embed.Embedder) *vectorStore {
	sum := sha256.Sum256([]byte(embedder.Fingerprint()))
	path := filepath.Join(mem.cacheDir(), hex.EncodeToString(sum[:])[:12], "vectors.gob")

	store := &vectorStore{path: path, rows: map[string]vectorRow{}}

	file, err := os.Open(path)
	if err != nil {
		return store
	}

	defer func() { _ = file.Close() }()

	decoded := map[string]vectorRow{}

	err = gob.NewDecoder(file).Decode(&decoded)
	if err == nil {
		store.rows = decoded
	}

	return store
}

func (store *vectorStore) save() error {
	err := os.MkdirAll(filepath.Dir(store.path), 0o700)
	if err != nil {
		return fmt.Errorf("creating the vector store: %w", err)
	}

	temp := store.path + ".tmp"

	file, err := os.Create(temp)
	if err != nil {
		return fmt.Errorf("writing the vector store: %w", err)
	}

	err = gob.NewEncoder(file).Encode(store.rows)
	if err != nil {
		_ = file.Close()

		return fmt.Errorf("encoding the vector store: %w", err)
	}

	err = file.Close()
	if err != nil {
		return fmt.Errorf("writing the vector store: %w", err)
	}

	err = os.Rename(temp, store.path)
	if err != nil {
		return fmt.Errorf("replacing the vector store: %w", err)
	}

	return nil
}

// fill embeds every entry whose vectors are missing or older than its content:
// filing never waits on the embedder, so the next call reaching it catches up.
func (mem *memory) fill(ctx context.Context, embedder embed.Embedder, store *vectorStore, folded *state) error {
	type pending struct {
		id     string
		hash   string
		chunks []string
	}

	var todo []pending

	var texts []string

	for _, held := range folded.entryList() {
		if held.dropped {
			continue
		}

		hash := contentHash(held)
		if store.rows[held.id].Hash == hash {
			continue
		}

		chunks := embed.Chunks(held.title, held.body)
		todo = append(todo, pending{id: held.id, hash: hash, chunks: chunks})
		texts = append(texts, chunks...)
	}

	if len(todo) == 0 {
		return nil
	}

	bounded, cancel := context.WithTimeout(ctx, embedLimit)
	defer cancel()

	vectors, err := embedder.Embed(bounded, texts)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNoEmbedder, err)
	}

	if len(vectors) != len(texts) {
		return fmt.Errorf("%w: the embedder answered %d vectors for %d texts", ErrNoEmbedder, len(vectors), len(texts))
	}

	next := 0
	for _, item := range todo {
		store.rows[item.id] = vectorRow{Hash: item.hash, Chunks: vectors[next : next+len(item.chunks)]}
		next += len(item.chunks)
	}

	return store.save()
}

// semantic scores entries against a phrase by their best chunk.
func (mem *memory) semantic(ctx context.Context, folded *state, phrase string) (map[string]float32, error) {
	return mem.semanticFor(ctx, folded, phrase, "")
}

// semanticFor scores every live entry against phrase. A prompt names its
// session: the prompt's vector then comes from, or goes to, the slot
// agentic-tools shares, so the prompt is embedded once between them.
func (mem *memory) semanticFor(ctx context.Context, folded *state, phrase string, session string) (map[string]float32, error) {
	embedder, err := mem.embedder()
	if err != nil {
		return nil, err
	}

	store := mem.vectors(embedder)

	err = mem.fill(ctx, embedder, store, folded)
	if err != nil {
		return nil, err
	}

	slots := promptvector.Dir(mem.env.CacheRoot)

	cached, hit := promptvector.Load(slots, session, phrase, embedder.Fingerprint())

	query := [][]float32{cached}
	if !hit {
		bounded, cancel := context.WithTimeout(ctx, embedLimit)
		defer cancel()

		query, err = embedder.Embed(bounded, []string{phrase})
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrNoEmbedder, err)
		}

		if len(query) != 1 {
			return nil, fmt.Errorf("%w: the embedder answered no vector for the query", ErrNoEmbedder)
		}

		if session != "" {
			_ = promptvector.Save(slots, session, phrase, embedder.Fingerprint(), query[0])
		}
	}

	scores := map[string]float32{}

	for id, row := range store.rows {
		if _, live := folded.entries[id]; !live {
			continue
		}

		best := float32(-1)
		for _, chunk := range row.Chunks {
			score := embed.Cosine(query[0], chunk)
			if score > best {
				best = score
			}
		}

		if len(row.Chunks) > 0 {
			scores[id] = best
		}
	}

	return scores, nil
}
