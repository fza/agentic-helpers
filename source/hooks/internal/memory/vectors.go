package memory

import (
	"cmp"
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
	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/promptvector"
	"github.com/fza/agentic-helpers/source/hooks/internal/vectorseg"
)

// embedLimit bounds one call to the embedder in a hook, so a hook never hangs
// on a model server that accepts the connection and never answers.
const embedLimit = 20 * time.Second

var ErrNoEmbedder = errors.New("semantic search needs an embedder")

// legacyRow is one entry's vectors in vectors.gob, the whole-file store
// segments replace; it is read once, to migrate.
type legacyRow struct {
	Hash   string
	Chunks [][]float32
}

// embedder is the embedder the options choose, settled once per run: under
// sdd's block that costs one `sdd config get`.
func (mem *memory) embedder(ctx context.Context) (embed.Templated, error) {
	if mem.embedding == nil {
		options := mem.options()
		config, _ := embed.Settle(ctx, options.OwnEmbedding(mem.env.Getenv), options.EmbeddingFromSdd(), mem.env.SddConfig, mem.env.Base)
		mem.embedding = &config
	}

	embedder, err := mem.env.Embedders(*mem.embedding)
	if err != nil {
		return embed.Templated{}, fmt.Errorf("%w: %w", ErrNoEmbedder, err)
	}

	return embed.Template(embedder, *mem.embedding), nil
}

// embedLimitFor bounds one embedding call: a hook keeps its own short
// deadline, never longer than the configured timeout a command waits.
func (mem *memory) embedLimitFor() time.Duration {
	timeout := cmp.Or(mem.embedding.Timeout, pluginconfig.DefaultEmbeddingTimeout)
	if mem.inHook {
		return min(embedLimit, timeout)
	}

	return timeout
}

func contentHash(held *entry) string {
	sum := sha256.Sum256([]byte(held.title + "\n" + held.body))

	return hex.EncodeToString(sum[:])[:16]
}

// vectors opens the vector store of one embedder: a changed embedder starts a
// fresh store instead of mixing two vector spaces.
func (mem *memory) vectors(embedder embed.Templated) *vectorseg.Store {
	sum := sha256.Sum256([]byte(embedder.Fingerprint()))

	return vectorseg.Open(mem.env.Roots.Cache, filepath.Join(mem.cacheDir(), hex.EncodeToString(sum[:])[:12], "segments"))
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
func (mem *memory) fill(ctx context.Context, embedder embed.Templated, store *vectorseg.Store, folded *state, live vectorseg.Live) (*vectorseg.Snapshot, error) {
	// Taken before the snapshot, so a fill never embeds again what another
	// process landed while this one waited to start.
	release, taken, err := store.TryFill()
	if err != nil {
		return nil, err
	}

	if !taken {
		return store.Snapshot()
	}

	defer release()

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
	}

	if len(todo) == 0 {
		return snapshot, nil
	}

	_ = snapshot.Close()

	// Each request has its own deadline and its vectors are saved as it
	// answers, as sdd's index fill does: a re-embed of the whole store lands in
	// one call however many requests it takes, and a call the caller's own
	// deadline cuts short keeps what landed.
	for len(todo) > 0 {
		var texts []string

		size := 0
		for size < len(todo) && (size == 0 || len(texts)+len(todo[size].chunks) <= embed.RequestTexts) {
			texts = append(texts, todo[size].chunks...)
			size++
		}

		bounded, cancel := context.WithTimeout(ctx, mem.embedLimitFor())
		vectors, err := embedder.Documents(bounded, texts)
		cancel()

		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrNoEmbedder, err)
		}

		if len(vectors) != len(texts) {
			return nil, fmt.Errorf("%w: the embedder answered %d vectors for %d texts", ErrNoEmbedder, len(vectors), len(texts))
		}

		rows := make([]vectorseg.Row, 0, size)
		next := 0

		for _, item := range todo[:size] {
			rows = append(rows, vectorseg.Row{ID: item.id, Hash: item.hash, Chunks: vectors[next : next+len(item.chunks)]})
			next += len(item.chunks)
		}

		err = store.Write(rows, live)
		if err != nil {
			return nil, err
		}

		todo = todo[size:]
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
	embedder, err := mem.embedder(ctx)
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

	slots := promptvector.Dir(mem.env.Roots.Cache)

	cached, hit := promptvector.Load(ctx, slots, session, phrase, embedder.Fingerprint())

	query := [][]float32{cached}
	if !hit {
		bounded, cancel := context.WithTimeout(ctx, mem.embedLimitFor())
		defer cancel()

		query, err = embedder.Queries(bounded, []string{phrase})
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrNoEmbedder, err)
		}

		if len(query) != 1 {
			return nil, fmt.Errorf("%w: the embedder answered no vector for the query", ErrNoEmbedder)
		}

		if session != "" {
			_ = promptvector.Save(ctx, slots, session, phrase, embedder.Fingerprint(), query[0])
		}
	}

	return snapshot.Scores(query[0], live)
}
