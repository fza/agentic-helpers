package memory

import (
	"context"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
	"github.com/fza/agentic-helpers/source/hooks/internal/promptvector"
	"github.com/fza/agentic-helpers/source/hooks/internal/vectorseg"
)

// embedLimit bounds one call to the embedder, so a hook never hangs on a model
// server that accepts the connection and never answers.
const embedLimit = 20 * time.Second

var ErrNoEmbedder = errors.New("semantic search needs an embedder")

// legacyRow is one entry's vectors in vectors.gob, the whole-file store
// segments replace; it is read once, to migrate.
type legacyRow struct {
	Hash   string
	Chunks [][]float32
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
func (mem *memory) vectors(embedder embed.Embedder) *vectorseg.Store {
	sum := sha256.Sum256([]byte(embedder.Fingerprint()))

	return vectorseg.Open(filepath.Join(mem.cacheDir(), hex.EncodeToString(sum[:])[:12], "segments"))
}

// counted is which rows a search counts: an entry's rows embedded from its
// current content, or any row of an entry whose body was dropped, since
// nothing embeds it again.
func counted(folded *state) vectorseg.Live {
	current := map[string]string{}

	for _, held := range folded.entryList() {
		if held.dropped {
			current[held.id] = ""

			continue
		}

		current[held.id] = contentHash(held)
	}

	return func(id string, hash string) bool {
		want, live := current[id]

		return live && (want == "" || want == hash)
	}
}

// migrate moves vectors.gob, beside the segments, into one segment, so an
// existing store is never embedded again. A file that does not decode is a
// cache lost: its entries are embedded again.
func migrate(store *vectorseg.Store, live vectorseg.Live) error {
	legacy := filepath.Join(filepath.Dir(store.Dir()), "vectors.gob")

	file, err := os.Open(legacy)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("opening the vector store to migrate: %w", err)
	}

	decoded := map[string]legacyRow{}
	decodeErr := gob.NewDecoder(file).Decode(&decoded)
	_ = file.Close()

	if decodeErr == nil {
		rows := make([]vectorseg.Row, 0, len(decoded))
		for id, row := range decoded {
			rows = append(rows, vectorseg.Row{ID: id, Hash: row.Hash, Chunks: row.Chunks})
		}

		slices.SortFunc(rows, func(a vectorseg.Row, b vectorseg.Row) int { return strings.Compare(a.ID, b.ID) })

		err = store.Write(rows, live)
		if err != nil {
			return fmt.Errorf("migrating the vector store: %w", err)
		}
	}

	err = os.Remove(legacy)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing the migrated vector store: %w", err)
	}

	return nil
}

// fill embeds every entry the snapshot holds no rows of for its current
// content, and returns the snapshot to search: filing never waits on the
// embedder, so the next call reaching it catches up.
func (mem *memory) fill(ctx context.Context, embedder embed.Embedder, store *vectorseg.Store, folded *state, live vectorseg.Live) (*vectorseg.Snapshot, error) {
	snapshot, err := store.Snapshot()
	if err != nil {
		return nil, err
	}

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
		if snapshot.Holds(held.id, hash) {
			continue
		}

		chunks := embed.Chunks(held.title, held.body)
		todo = append(todo, pending{id: held.id, hash: hash, chunks: chunks})
		texts = append(texts, chunks...)
	}

	if len(todo) == 0 {
		return snapshot, nil
	}

	_ = snapshot.Close()

	bounded, cancel := context.WithTimeout(ctx, embedLimit)
	defer cancel()

	vectors, err := embedder.Embed(bounded, texts)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoEmbedder, err)
	}

	if len(vectors) != len(texts) {
		return nil, fmt.Errorf("%w: the embedder answered %d vectors for %d texts", ErrNoEmbedder, len(vectors), len(texts))
	}

	rows := make([]vectorseg.Row, 0, len(todo))
	next := 0

	for _, item := range todo {
		rows = append(rows, vectorseg.Row{ID: item.id, Hash: item.hash, Chunks: vectors[next : next+len(item.chunks)]})
		next += len(item.chunks)
	}

	err = store.Write(rows, live)
	if err != nil {
		return nil, err
	}

	return store.Snapshot()
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
	live := counted(folded)

	err = migrate(store, live)
	if err != nil {
		return nil, err
	}

	snapshot, err := mem.fill(ctx, embedder, store, folded, live)
	if err != nil {
		return nil, err
	}

	defer func() { _ = snapshot.Close() }()

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

	return snapshot.Scores(query[0], live)
}
