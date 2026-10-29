package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strconv"
)

// version is what a write lays down: the files, the spec and its history line.
type version struct {
	name   string
	number int
	files  []walked
	spec   spec
	why    string
	vector []float32
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

	defer store.close()

	folder, err := store.ensureSub(next.name)
	if err != nil {
		return err
	}

	defer folder.close()

	err = folder.root.Mkdir(versionDir(next.number), 0o700)
	if err != nil {
		return fmt.Errorf("creating %s/%s: %w", folder.path, versionDir(next.number), err)
	}

	target, err := folder.sub(versionDir(next.number))
	if err != nil {
		return err
	}

	defer target.close()

	next.spec.Name = next.name
	next.spec.Files = nil

	for _, file := range next.files {
		next.spec.Files = append(next.spec.Files, file.name)

		err = writeInto(target, file)
		if err != nil {
			return err
		}
	}

	described, err := json.MarshalIndent(next.spec, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding tool.json: %w", err)
	}

	err = target.create("tool.json", append(described, '\n'))
	if err != nil {
		return err
	}

	err = appendLine(folder, "history.jsonl", historyLine{
		V: next.number, By: lib.session, Role: lib.role(ctx), At: lib.stamp(), Why: next.why,
	})
	if err != nil {
		return err
	}

	err = folder.replace("current", []byte(strconv.Itoa(next.number)+"\n"))
	if err != nil {
		return err
	}

	return lib.reindexTool(next.name, next.vector)
}

// writeInto creates one walked file below target, its folders made on the way.
func writeInto(target *dir, file walked) error {
	folder := path.Dir(file.name)
	if folder == "." {
		return target.create(file.name, file.data)
	}

	inner, err := target.ensureSubPath(folder)
	if err != nil {
		return err
	}

	defer inner.close()

	return inner.create(path.Base(file.name), file.data)
}

// ownStore opens the own store, made where it is missing, every component
// checked.
func (lib *library) ownStore() (*dir, error) {
	return ensureDir(lib.env.Roots.Stores(), lib.ownRel())
}

// reindexTool rereads one tool's folder into the own index and writes it.
func (lib *library) reindexTool(name string, vector []float32) error {
	held, found := loadIndex(lib.ownDir())
	if !found {
		rebuilt, err := buildIndex(lib.ownDir())
		if err != nil {
			return err
		}

		held = rebuilt
	}

	root, err := openDir(lib.ownDir())
	if err != nil {
		return err
	}

	defer root.close()

	folder, err := root.sub(name)
	if err != nil {
		return err
	}

	defer folder.close()

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

func appendLine(folder *dir, name string, value any) error {
	at := path.Join(folder.path, name)

	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encoding a line of %s: %w", name, err)
	}

	file, err := folder.appendTo(name)
	if err != nil {
		return err
	}

	_, err = file.Write(append(data, '\n'))
	closeErr := file.Close()

	if err != nil {
		return fmt.Errorf("appending to %s: %w", at, err)
	}

	if closeErr != nil {
		return fmt.Errorf("closing %s: %w", at, closeErr)
	}

	return nil
}

func (lib *library) stamp() string { return lib.now().Format("2006-01-02T15:04:05Z07:00") }

// embedOne embeds a tool's description, or answers nothing where no embedder
// does: the tool still lands, unranked by meaning until a rebuild embeds it.
func (lib *library) embedOne(ctx context.Context, described spec) ([]float32, string) {
	embedder, err := lib.embedder()
	if err != nil {
		return nil, ""
	}

	bounded, cancel := context.WithTimeout(ctx, embedLimit)
	defer cancel()

	vectors, err := embedder.Embed(bounded, []string{describe(described)})
	if err != nil || len(vectors) != 1 {
		return nil, ""
	}

	return vectors[0], embedder.Fingerprint()
}
