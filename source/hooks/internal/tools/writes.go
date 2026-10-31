package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strconv"

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
)

// version is what a write lays down: the files, the spec and its history line.
type version struct {
	name   string
	number int
	files  []safefs.Walked
	spec   spec
	why    string
	vector describedVectors
}

// writeVersion lays a version into the own store and makes it current, then
// rewrites the index. The caller holds the lock. A version holding a credential
// is refused before anything is written, whichever verb writes it.
func (lib *library) writeVersion(ctx context.Context, next version) error {
	err := credentialInVersion(ctx, next)
	if err != nil {
		return err
	}

	store, err := lib.ownStore()
	if err != nil {
		return err
	}

	defer store.Close()

	folder, err := store.EnsureSub(next.name, 0o700)
	if err != nil {
		return err
	}

	defer folder.Close()

	err = folder.Mkdir(versionDir(next.number), 0o700)
	if err != nil {
		return err
	}

	target, err := folder.Sub(versionDir(next.number))
	if err != nil {
		return err
	}

	defer target.Close()

	next.spec.Name = next.name
	next.spec.Files = nil

	for _, file := range next.files {
		next.spec.Files = append(next.spec.Files, file.Name)

		err = writeInto(target, file)
		if err != nil {
			return err
		}
	}

	described, err := json.MarshalIndent(next.spec, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding tool.json: %w", err)
	}

	err = target.Create("tool.json", append(described, '\n'), 0o600)
	if err != nil {
		return err
	}

	err = appendJournal(folder, historyJournal, historyLine{
		V: next.number, By: lib.session, Role: lib.role(ctx), At: lib.stamp(), Why: next.why,
	})
	if err != nil {
		return err
	}

	err = folder.Replace("current", []byte(strconv.Itoa(next.number)+"\n"), 0o600)
	if err != nil {
		return err
	}

	return lib.reindexTool(next.name, next.vector)
}

// writeInto creates one walked file below target, its folders made on the way.
func writeInto(target *safefs.Dir, file safefs.Walked) error {
	folder := path.Dir(file.Name)
	if folder == "." {
		return target.Create(file.Name, file.Data, 0o600)
	}

	inner, err := target.EnsureSubPath(folder, 0o700)
	if err != nil {
		return err
	}

	defer inner.Close()

	return inner.Create(path.Base(file.Name), file.Data, 0o600)
}

// ownStore opens the own store, made where it is missing, every component
// checked.
func (lib *library) ownStore() (*safefs.Dir, error) {
	return safefs.EnsureAt(lib.env.Roots.Data, lib.ownDir(), 0o700)
}

// reindexTool rereads one tool's folder into the own index and writes it.
func (lib *library) reindexTool(name string, vector describedVectors) error {
	held, found := loadIndex(lib.ownDir())
	if !found {
		rebuilt, err := buildIndex(lib.ownDir())
		if err != nil {
			return err
		}

		held = rebuilt
	}

	root, err := safefs.OpenDir(lib.ownDir())
	if err != nil {
		return err
	}

	defer root.Close()

	folder, err := root.Sub(name)
	if err != nil {
		return err
	}

	defer folder.Close()

	row, err := readTool(folder, name)
	if err != nil {
		return err
	}

	held.Tools[name] = row
	delete(held.Merged, name)

	err = lib.writeIndex(held)
	if err != nil {
		return err
	}

	lib.keepVector(row.Described, vector)

	return nil
}

func (lib *library) stamp() string { return lib.now().Format("2006-01-02T15:04:05Z07:00") }

// embedOne embeds a tool's description in pieces of the vector store's cap,
// or answers nothing where no embedder does: the tool still lands, unranked
// by meaning until a fill embeds it.
func (lib *library) embedOne(ctx context.Context, described spec) (describedVectors, string) {
	embedder, err := lib.embedder(ctx)
	if err != nil {
		return describedVectors{}, ""
	}

	maxRunes, err := lib.storeCap(ctx, embedder, lib.vectorStore(embedder.Fingerprint()))
	if err != nil {
		return describedVectors{}, ""
	}

	pieces := embed.Pieces(describe(described), maxRunes)

	bounded, cancel := context.WithTimeout(ctx, lib.embedLimitFor())
	defer cancel()

	vectors, err := embedder.Capped(maxRunes).Documents(bounded, pieces)
	if err != nil || len(vectors) != len(pieces) {
		return describedVectors{}, ""
	}

	return describedVectors{Chunks: vectors, MaxRunes: maxRunes}, embedder.Fingerprint()
}
