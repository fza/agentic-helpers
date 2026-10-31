package tools

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/secrets"
	"github.com/fza/agentic-helpers/source/hooks/internal/vectorseg"
)

// ErrNoCap is a vector store recording no cap when a search needs one: no
// fill has landed in it yet, since another process is still running the first.
var ErrNoCap = errors.New("the tool vectors record no input cap yet; their first fill is still running elsewhere, search again shortly")

// describedVectors is a description's vectors, one per piece of its text,
// each piece cut to MaxRunes, the cap of the vector store they go to.
type describedVectors struct {
	Chunks   [][]float32
	MaxRunes int
}

// describedText is a description to embed: its hash and its text.
type describedText struct {
	described string
	text      string
}

// vectorStore holds the vectors fingerprint's embedder answered, outside every
// store, one row per description keyed by its hash, so one vector serves every
// store naming a description and a rebuild never embeds an unchanged one.
func (lib *library) vectorStore(fingerprint string) *vectorseg.Store {
	store := vectorseg.Open(lib.env.Roots.Cache, filepath.Join(lib.env.Roots.SharedCache(home.Tools), "vectors", describedSum(fingerprint)))
	_ = lib.migrateVectors(store)

	return store
}

// migrateVectors removes the whole-file cache that preceded the segments. Its
// vectors were embedded uncut, so none of them counts under a cap: the next
// fill embeds every description again.
func (lib *library) migrateVectors(store *vectorseg.Store) error {
	legacy := store.Dir() + ".json"

	err := os.Remove(legacy)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing the vector cache that preceded the segments: %w", err)
	}

	return nil
}

func cappedRow(described string, vectors describedVectors) vectorseg.Row {
	return vectorseg.Row{ID: described, Hash: embed.RowHash(described, vectors.MaxRunes), Chunks: vectors.Chunks}
}

// named says whether some store on the machine names a description, whatever
// the read scope says, and its row was cut to one of caps: a vector no store
// names, or one cut to a cap the store has left, merges away.
func (lib *library) named(caps ...int) vectorseg.Live {
	keys := storeIDs(lib.env.Roots)
	if !slices.Contains(keys, lib.key) {
		keys = append(keys, lib.key)
	}

	described := map[string]bool{}

	for _, key := range keys {
		at := lib.env.Roots.Store(key, home.Tools)

		held, found := loadIndex(at)
		if !found {
			held, _ = buildIndex(at)
		}

		if held == nil {
			continue
		}

		for _, tool := range held.Tools {
			described[tool.Described] = true
		}
	}

	return func(id string, hash string) bool {
		if !described[id] {
			return false
		}

		for _, maxRunes := range caps {
			if hash == embed.RowHash(id, maxRunes) {
				return true
			}
		}

		return false
	}
}

// storeCap is the cap a single write into store cuts to: the one the store
// records, else the endpoint's calibrated cap, which the store then records.
func (lib *library) storeCap(ctx context.Context, embedder embed.Templated, store *vectorseg.Store) (int, error) {
	manifest, err := store.Manifest()
	if err != nil {
		return 0, err
	}

	if manifest.MaxRunes > 0 {
		return manifest.MaxRunes, nil
	}

	calibrating, cancel := context.WithTimeout(ctx, lib.embedLimitFor())
	target, err := embedder.MaxRunes(calibrating, lib.env.Roots.Cache)
	cancel()

	if err != nil {
		return 0, fmt.Errorf("calibrating the embedder: %w", err)
	}

	return target, store.SetManifest(vectorseg.Manifest{MaxRunes: target})
}

// keepVector files a tool's vectors once the own index names its description,
// so no merge running meanwhile finds it named by no store.
func (lib *library) keepVector(described string, vectors describedVectors) {
	// A vector exists only where an embedder was settled to make it.
	if len(vectors.Chunks) == 0 || lib.embedding == nil {
		return
	}

	embedder, err := lib.embedder(context.Background())
	if err != nil {
		return
	}

	store := lib.vectorStore(embedder.Fingerprint())

	manifest, err := store.Manifest()
	if err != nil {
		return
	}

	_ = store.Write([]vectorseg.Row{cappedRow(described, vectors)}, lib.named(manifest.MaxRunes, vectors.MaxRunes))
}

// scores is the cosine to query of each description wanted holds, among the
// vectors fingerprint's embedder answered at the cap their store records.
func (lib *library) scores(query []float32, fingerprint string, wanted map[string]bool) map[string]float32 {
	store := lib.vectorStore(fingerprint)

	manifest, err := store.Manifest()
	if err != nil || manifest.MaxRunes == 0 {
		return nil
	}

	snapshot, err := store.Snapshot()
	if err != nil {
		return nil
	}

	defer func() { _ = snapshot.Close() }()

	scores, err := snapshot.Scores(query, func(id string, hash string) bool {
		return wanted[id] && hash == embed.RowHash(id, manifest.MaxRunes)
	})
	if err != nil {
		return nil
	}

	return scores
}

// rebuild reads a store's folders and embeds every description no vector is
// held for. An embedder failing leaves the index whole, its tools unranked by
// meaning until a fill reaches them.
func (lib *library) rebuild(ctx context.Context, at string) (*index, error) {
	built, err := buildIndex(at)
	if err != nil {
		return nil, err
	}

	embedder, err := lib.embedder(ctx)
	if err != nil {
		return built, nil
	}

	_, _ = lib.fillVectors(ctx, embedder, indexTexts(built))

	return built, nil
}

func indexTexts(held ...*index) []describedText {
	var texts []describedText

	for _, one := range held {
		for _, name := range sortedKeys(one.Tools) {
			row := one.Tools[name]
			texts = append(texts, describedText{described: row.Described, text: describe(row.spec())})
		}
	}

	return texts
}

// fillVectors embeds every description wanted names that the store holds no
// row of at the endpoint's calibrated cap, and answers the cap the store
// records after, for a search to cut its query to. A store recording no cap
// takes the calibrated one at once; one recording another keeps it until
// every description is embedded again, as memory's fill does.
func (lib *library) fillVectors(ctx context.Context, embedder embed.Templated, wanted []describedText) (int, error) {
	store := lib.vectorStore(embedder.Fingerprint())

	// Another process filling this vector store leaves the embedding to it, as
	// sdd's lazy index fill does; taken before the snapshot, so nothing it
	// landed is embedded again.
	release, taken, err := store.TryFill()
	if err != nil {
		return 0, err
	}

	if !taken {
		manifest, err := store.Manifest()

		return manifest.MaxRunes, err
	}

	defer release()

	calibrating, cancel := context.WithTimeout(ctx, lib.embedLimitFor())
	target, err := embedder.MaxRunes(calibrating, lib.env.Roots.Cache)
	cancel()

	if err != nil {
		return 0, fmt.Errorf("calibrating the embedder: %w", err)
	}

	manifest, err := store.Manifest()
	if err != nil {
		return 0, err
	}

	if manifest.MaxRunes == 0 {
		manifest.MaxRunes = target

		err = store.SetManifest(manifest)
		if err != nil {
			return 0, err
		}
	}

	snapshot, err := store.Snapshot()
	if err != nil {
		return manifest.MaxRunes, err
	}

	type pending struct {
		described string
		pieces    []string
	}

	var todo []pending

	queued := map[string]bool{}

	// A description holding a credential, left on disk by an earlier release or
	// another process, is never embedded: its tool stays unvectored.
	for _, item := range wanted {
		if queued[item.described] || snapshot.Holds(item.described, embed.RowHash(item.described, target)) ||
			len(secrets.Scan(ctx, item.text, "")) > 0 {
			continue
		}

		queued[item.described] = true
		todo = append(todo, pending{described: item.described, pieces: embed.Pieces(item.text, target)})
	}

	_ = snapshot.Close()

	live := lib.named(target, manifest.MaxRunes)
	sized := embedder.Capped(target)

	// Each request has its own deadline and its vectors are saved as it
	// answers, as sdd's index fill does.
	for len(todo) > 0 {
		var texts []string

		size := 0
		for size < len(todo) && (size == 0 || len(texts)+len(todo[size].pieces) <= embed.RequestTexts) {
			texts = append(texts, todo[size].pieces...)
			size++
		}

		bounded, cancel := context.WithTimeout(ctx, lib.embedLimitFor())
		vectors, err := sized.Documents(bounded, texts)
		cancel()

		if err != nil {
			return manifest.MaxRunes, err
		}

		if len(vectors) != len(texts) {
			return manifest.MaxRunes, fmt.Errorf("the embedder answered %d vectors for %d texts", len(vectors), len(texts))
		}

		rows := make([]vectorseg.Row, 0, size)
		next := 0

		for _, item := range todo[:size] {
			chunks := vectors[next : next+len(item.pieces)]
			rows = append(rows, cappedRow(item.described, describedVectors{Chunks: chunks, MaxRunes: target}))
			next += len(item.pieces)
		}

		err = store.Write(rows, live)
		if err != nil {
			return manifest.MaxRunes, err
		}

		todo = todo[size:]
	}

	if manifest.MaxRunes != target {
		err = store.SetManifest(vectorseg.Manifest{MaxRunes: target})
		if err != nil {
			return manifest.MaxRunes, err
		}
	}

	return target, nil
}
