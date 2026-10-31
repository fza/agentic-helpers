package memory

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

// ErrNoCap is a vector store recording no cap when a search needs one: its
// first fill has not landed, since another process is still running it.
var ErrNoCap = errors.New("the vector store records no input cap yet; its first fill is still running elsewhere, search again shortly")

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

// rowHash names an entry's rows by its content and the cap its chunks were
// cut to: rows of another cap never pass as current.
func rowHash(held *entry, maxRunes int) string {
	return embed.RowHash(held.title+"\n"+held.body, maxRunes)
}

// vectors opens the vector store of one embedder: a changed embedder starts a
// fresh store instead of mixing two vector spaces.
func (mem *memory) vectors(embedder embed.Templated) *vectorseg.Store {
	sum := sha256.Sum256([]byte(embedder.Fingerprint()))

	return vectorseg.Open(mem.env.Roots.Cache, filepath.Join(mem.cacheDir(), hex.EncodeToString(sum[:])[:12], "segments"))
}

// counted is which rows count under any of caps: an entry's rows embedded
// from its current content cut to one of them, or any row of an entry whose
// body was dropped, since nothing embeds it again. A search counts the
// manifest's cap; a fill moving the store to a new cap keeps both, so a
// search meanwhile still finds the rows of the old one.
func counted(folded *state, caps ...int) vectorseg.Live {
	current := map[string][]string{}

	for _, held := range folded.entryList() {
		if held.dropped {
			current[held.id] = nil

			continue
		}

		for _, maxRunes := range caps {
			current[held.id] = append(current[held.id], rowHash(held, maxRunes))
		}
	}

	return func(id string, hash string) bool {
		want, live := current[id]

		return live && (want == nil || slices.Contains(want, hash))
	}
}

// migrate removes vectors.gob, the whole-file store beside the segments. Its
// rows were embedded uncut, so none of them counts under a cap: the next fill
// embeds its entries again.
func migrate(store *vectorseg.Store) error {
	legacy := filepath.Join(filepath.Dir(store.Dir()), "vectors.gob")

	err := os.Remove(legacy)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing the vector store that preceded the segments: %w", err)
	}

	return nil
}

// fill embeds every entry the snapshot holds no rows of for its current
// content at the endpoint's calibrated cap, and returns the snapshot to
// search: filing never waits on the embedder, so the next call reaching it
// catches up. A store recording no cap takes the calibrated one at once,
// since none of its rows counts under any other; one recording another cap
// keeps it until every entry is embedded again, then records the new one.
func (mem *memory) fill(ctx context.Context, embedder embed.Templated, store *vectorseg.Store, folded *state) (*vectorseg.Snapshot, error) {
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

	calibrating, cancel := context.WithTimeout(ctx, mem.embedLimitFor())
	target, err := embedder.MaxRunes(calibrating, mem.env.Roots.Cache)
	cancel()

	if err != nil {
		return nil, fmt.Errorf("%w: calibrating the embedder: %w", ErrNoEmbedder, err)
	}

	manifest, err := store.Manifest()
	if err != nil {
		return nil, err
	}

	if manifest.MaxRunes == 0 {
		manifest.MaxRunes = target

		err = store.SetManifest(manifest)
		if err != nil {
			return nil, err
		}
	}

	live := counted(folded, target, manifest.MaxRunes)
	sized := embedder.Capped(target)

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

		hash := rowHash(held, target)
		if snapshot.Holds(held.id, hash) {
			continue
		}

		chunks := embed.Chunks(held.title, held.body, target)
		todo = append(todo, pending{id: held.id, hash: hash, chunks: chunks})
	}

	if len(todo) == 0 && manifest.MaxRunes == target {
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
		vectors, err := sized.Documents(bounded, texts)
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

	if manifest.MaxRunes != target {
		err = store.SetManifest(vectorseg.Manifest{MaxRunes: target})
		if err != nil {
			return nil, err
		}
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

	err = migrate(store)
	if err != nil {
		return nil, err
	}

	snapshot, err := mem.fill(ctx, embedder, store, folded)
	if err != nil {
		return nil, err
	}

	defer func() { _ = snapshot.Close() }()

	// The fill may have moved the store to a new cap; the search cuts its
	// query to, and counts the rows of, whichever cap the store records now.
	manifest, err := store.Manifest()
	if err != nil {
		return nil, err
	}

	if manifest.MaxRunes == 0 {
		return nil, ErrNoCap
	}

	live := counted(folded, manifest.MaxRunes)
	slots := promptvector.Dir(mem.env.Roots.Cache)
	slotKey := promptvector.Key(embedder.Fingerprint(), manifest.MaxRunes)

	cached, hit := promptvector.Load(ctx, slots, session, phrase, slotKey)

	query := [][]float32{cached}
	if !hit {
		bounded, cancel := context.WithTimeout(ctx, mem.embedLimitFor())
		defer cancel()

		query, err = embedder.Capped(manifest.MaxRunes).Queries(bounded, []string{phrase})
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrNoEmbedder, err)
		}

		if len(query) != 1 {
			return nil, fmt.Errorf("%w: the embedder answered no vector for the query", ErrNoEmbedder)
		}

		if session != "" {
			_ = promptvector.Save(ctx, slots, session, phrase, slotKey, query[0])
		}
	}

	return snapshot.Scores(query[0], live)
}
