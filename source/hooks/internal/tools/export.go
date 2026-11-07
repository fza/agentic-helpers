package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/formatsteps"
	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
)

var ErrUnreadableTool = errors.New("a tool folder this release cannot read")

// LibraryFormat is the tool library format this release reads and writes.
const LibraryFormat = libraryFormat

// MigrateCopy brings the tool library of the store id below roots to
// LibraryFormat through the library's own step table. roots is a scratch
// home an export copied the library into: every step writes below it, the
// session slots of its cache included, and never below the machine's home.
func MigrateCopy(ctx context.Context, roots home.Roots, id string) error {
	at := roots.Store(id, home.Tools)

	store, err := safefs.OpenDir(at)
	if err != nil {
		return err
	}

	defer store.Close()

	err = prepareStore(ctx, store, roots, at, roots.StoreCache(id, home.Tools))
	if err != nil {
		return fmt.Errorf("migrating a copy of the tool library of %s: %w", id, err)
	}

	return nil
}

// LibraryExport is one store's tool library as the `agent-memory-export`
// archive holds it (docs/export/format.md), its tools by name.
type LibraryExport struct {
	Format int
	Tools  []ToolExport
}

// ToolExport is one tool folder: its current version or the tool it was
// merged into, its journals' lines as stored, and every version it still
// stores.
type ToolExport struct {
	Name       string
	Current    int
	MergedInto string
	History    []json.RawMessage
	Takes      []json.RawMessage
	Pruned     []json.RawMessage
	Versions   []ToolVersion
}

// ToolVersion is one stored version folder, its `tool.json` first and the
// rest of its files sorted by path, byte for byte.
type ToolVersion struct {
	Number int
	Files  []safefs.Walked
}

// Export reads the library at dir for the archive, as its own reads do and
// without its lock, writing nothing: a format older than this release's
// reads through the same journal reader that serves its legacy lines. A tool
// folder the library itself would skip, without `current` or a readable
// current version, refuses the export rather than leaving the tool out.
func Export(ctx context.Context, dir string) (LibraryExport, error) {
	format, err := formatsteps.Check(dir, dir, libraryFormat)
	if err != nil {
		return LibraryExport{}, err
	}

	exported := LibraryExport{Format: format}

	store, err := safefs.OpenDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return exported, nil
	}

	if err != nil {
		return LibraryExport{}, err
	}

	defer store.Close()

	listed, err := store.Entries()
	if err != nil {
		return LibraryExport{}, err
	}

	for _, entry := range listed {
		name := entry.Name()
		if !entry.IsDir() || name == "sessions" || name == "copies" || !namePattern.MatchString(name) || len(name) > nameMax {
			continue
		}

		err = ctx.Err()
		if err != nil {
			return LibraryExport{}, err
		}

		tool, err := exportTool(store, name)
		if err != nil {
			return LibraryExport{}, fmt.Errorf("%s/%s: %w: %w", dir, name, ErrUnreadableTool, err)
		}

		exported.Tools = append(exported.Tools, tool)
	}

	slices.SortFunc(exported.Tools, func(one, other ToolExport) int { return strings.Compare(one.Name, other.Name) })

	return exported, nil
}

func exportTool(store *safefs.Dir, name string) (ToolExport, error) {
	folder, err := store.Sub(name)
	if err != nil {
		return ToolExport{}, err
	}

	defer folder.Close()

	tool := ToolExport{Name: name}

	survivor, err := folder.Read("merged", currentLimit+nameMax)
	if err == nil {
		tool.MergedInto = strings.TrimSpace(string(survivor))

		return tool, nil
	}

	raw, err := folder.Read("current", currentLimit)
	if err != nil {
		return ToolExport{}, err
	}

	current, valid := parseCurrent(raw)
	if !valid {
		return ToolExport{}, errors.New("current names no version")
	}

	tool.Current = current

	for _, held := range []struct {
		journal journal
		lines   *[]json.RawMessage
	}{{historyJournal, &tool.History}, {takesJournal, &tool.Takes}, {prunedJournal, &tool.Pruned}} {
		lines, err := readJournal[json.RawMessage](folder, held.journal)
		if err != nil {
			return ToolExport{}, err
		}

		for _, line := range lines {
			var compact bytes.Buffer

			err = json.Compact(&compact, line)
			if err != nil {
				return ToolExport{}, err
			}

			*held.lines = append(*held.lines, compact.Bytes())
		}
	}

	tool.Versions, err = exportVersions(folder)
	if err != nil {
		return ToolExport{}, err
	}

	if !slices.ContainsFunc(tool.Versions, func(version ToolVersion) bool { return version.Number == current }) {
		return ToolExport{}, fmt.Errorf("current names %s, which is not stored", versionDir(current))
	}

	return tool, nil
}

func exportVersions(folder *safefs.Dir) ([]ToolVersion, error) {
	listed, err := folder.Entries()
	if err != nil {
		return nil, err
	}

	var versions []ToolVersion

	for _, entry := range listed {
		digits, isVersion := strings.CutPrefix(entry.Name(), "v")
		number, err := strconv.Atoi(digits)

		if !isVersion || err != nil || number <= 0 || !entry.IsDir() || versionDir(number) != entry.Name() {
			continue
		}

		version, err := folder.Sub(entry.Name())
		if err != nil {
			return nil, err
		}

		files, err := version.Walk(versionLimit, nil)
		version.Close()

		if err != nil {
			return nil, err
		}

		if !slices.ContainsFunc(files, func(file safefs.Walked) bool { return file.Name == "tool.json" }) {
			return nil, fmt.Errorf("%s has no tool.json", entry.Name())
		}

		slices.SortFunc(files, func(one, other safefs.Walked) int {
			switch {
			case one.Name == "tool.json":
				return -1
			case other.Name == "tool.json":
				return 1
			}

			return strings.Compare(one.Name, other.Name)
		})

		versions = append(versions, ToolVersion{Number: number, Files: files})
	}

	slices.SortFunc(versions, func(one, other ToolVersion) int { return one.Number - other.Number })

	return versions, nil
}
