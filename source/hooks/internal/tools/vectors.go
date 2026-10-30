package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/secrets"
	"github.com/fza/agentic-helpers/source/hooks/internal/vectorseg"
)

// vectorStore holds the vectors fingerprint's embedder answered, outside every
// store, one row per description keyed by its hash, so one vector serves every
// store naming a description and a rebuild never embeds an unchanged one.
func (lib *library) vectorStore(fingerprint string) *vectorseg.Store {
	store := vectorseg.Open(filepath.Join(lib.env.Roots.SharedCache(home.Tools), "vectors", describedSum(fingerprint)))
	_ = lib.migrateVectors(store)

	return store
}

// migrateVectors folds the whole-file cache that preceded the segments into
// one segment, embedding nothing.
func (lib *library) migrateVectors(store *vectorseg.Store) error {
	legacy := store.Dir() + ".json"

	data, err := os.ReadFile(legacy)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("reading the vector cache to migrate: %w", err)
	}

	decoded := map[string][]float32{}
	if json.Unmarshal(data, &decoded) == nil {
		err = store.Write(vectorRows(decoded), lib.named())
		if err != nil {
			return fmt.Errorf("migrating the vector cache: %w", err)
		}
	}

	err = os.Remove(legacy)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing the migrated vector cache: %w", err)
	}

	return nil
}

func vectorRows(vectors map[string][]float32) []vectorseg.Row {
	rows := make([]vectorseg.Row, 0, len(vectors))

	for _, described := range sortedKeys(vectors) {
		if len(vectors[described]) > 0 {
			rows = append(rows, vectorseg.Row{ID: described, Hash: described, Chunks: [][]float32{vectors[described]}})
		}
	}

	return rows
}

// named says whether some store on the machine names a description, whatever
// the read scope says: a vector no store names merges away.
func (lib *library) named() vectorseg.Live {
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

	return func(id string, _ string) bool { return described[id] }
}

// keepVector files a tool's vector once the own index names its description,
// so no merge running meanwhile finds it named by no store.
func (lib *library) keepVector(described string, vector []float32) {
	// A vector exists only where an embedder was settled to make it.
	if len(vector) == 0 || lib.embedding == nil {
		return
	}

	embedder, err := lib.embedder(context.Background())
	if err != nil {
		return
	}

	_ = lib.vectorStore(embedder.Fingerprint()).Write(vectorRows(map[string][]float32{described: vector}), lib.named())
}

// scores is the cosine to query of each description wanted holds, among the
// vectors fingerprint's embedder answered.
func (lib *library) scores(query []float32, fingerprint string, wanted map[string]bool) map[string]float32 {
	snapshot, err := lib.vectorStore(fingerprint).Snapshot()
	if err != nil {
		return nil
	}

	defer func() { _ = snapshot.Close() }()

	scores, err := snapshot.Scores(query, func(id string, _ string) bool { return wanted[id] })
	if err != nil {
		return nil
	}

	return scores
}

// rebuild reads a store's folders and embeds every description no vector is
// held for.
func (lib *library) rebuild(ctx context.Context, at string) (*index, error) {
	built, err := buildIndex(at)
	if err != nil {
		return nil, err
	}

	embedder, err := lib.embedder(ctx)
	if err != nil {
		return built, nil
	}

	store := lib.vectorStore(embedder.Fingerprint())

	snapshot, err := store.Snapshot()
	if err != nil {
		return built, nil
	}

	var (
		missing []string
		texts   []string
	)

	// A description holding a credential, left on disk by an earlier release or
	// another process, is never embedded: its tool stays unvectored.
	for _, name := range sortedKeys(built.Tools) {
		row := built.Tools[name]
		text := describe(row.spec())

		if !snapshot.Holds(row.Described, row.Described) && len(secrets.Scan(ctx, text, "")) == 0 {
			missing = append(missing, row.Described)
			texts = append(texts, text)
		}
	}

	_ = snapshot.Close()

	if len(missing) == 0 {
		return built, nil
	}

	bounded, cancel := context.WithTimeout(ctx, lib.embedLimitFor())
	defer cancel()

	vectors, err := embedder.Documents(bounded, texts)
	if err != nil || len(vectors) != len(missing) {
		return built, nil
	}

	embedded := map[string][]float32{}
	for position, described := range missing {
		embedded[described] = vectors[position]
	}

	_ = store.Write(vectorRows(embedded), lib.named())

	return built, nil
}
