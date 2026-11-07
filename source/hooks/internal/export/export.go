// Package export writes every store on the machine into one archive of the
// `agent-memory-export` format (docs/export/format.md), `agentic-helpers
// export <file>`, and reads one back whole to check it (Verify), which
// `agentic-helpers uninstall` does too before it removes anything.
//
// An export only reads: it takes the snapshot every reader takes, writes no
// cache, and migrates nothing. A store it cannot read refuses the whole
// export, so no archive ever leaves a store out unsaid.
package export

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/memory"
	"github.com/fza/agentic-helpers/source/hooks/internal/migration"
	"github.com/fza/agentic-helpers/source/hooks/internal/tools"
)

const (
	FormatName = "agent-memory-export"
	Version    = 1
	Self       = "agentic-helpers"
)

var (
	ErrTargetExists = errors.New("the target exists")
	ErrUnmigrated   = errors.New("the old layout under ~/.claude still waits for its migration")
)

// Env is what an export reads: the home's roots, the migration environment
// telling whether the old layout still holds data the home does not,
// Producer naming the program and version the manifest records, and Now.
type Env struct {
	Roots     home.Roots
	Migration migration.Env
	Producer  string
	Now       func() time.Time
}

// Result is a written archive: where, how large, its manifest, and every
// attachment an entry names whose bytes the machine no longer held.
type Result struct {
	Path     string
	Size     int64
	Manifest Manifest
	Unstored []string
}

type storeRead struct {
	dir         home.StoreDir
	identity    home.Identity
	identityRaw []byte
	memory      *memory.StoreExport
	tools       *tools.LibraryExport
	// migratedFrom is, by component, the format a component was at where the
	// export read a migrated copy of it.
	migratedFrom map[string]int
}

// Write writes the archive to target through a temporary file beside it,
// reads that file back whole (Verify), and only then renames it into place.
// An existing target is refused unless force is set. Nothing is written
// where any store cannot be read.
func Write(ctx context.Context, env Env, target string, force bool) (Result, error) {
	target, err := filepath.Abs(target)
	if err != nil {
		return Result{}, fmt.Errorf("resolving %s: %w", target, err)
	}

	_, err = os.Lstat(target)
	if err == nil && !force {
		return Result{}, fmt.Errorf("%s: %w; --force replaces it", target, ErrTargetExists)
	}

	stores, err := readStores(ctx, env)
	if err != nil {
		return Result{}, err
	}

	exported := env.Now().UTC()
	built, unstored := plan(env, stores, exported)

	temporary, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".*.tmp")
	if err != nil {
		return Result{}, fmt.Errorf("creating the archive beside %s: %w", target, err)
	}

	placed := false

	defer func() {
		if !placed {
			_ = os.Remove(temporary.Name())
		}
	}()

	err = writeArchive(ctx, env, temporary, built, stores, exported)

	closeErr := temporary.Close()
	if err == nil {
		err = closeErr
	}

	if err != nil {
		return Result{}, fmt.Errorf("writing the archive: %w", err)
	}

	_, err = Verify(temporary.Name())
	if err != nil {
		return Result{}, err
	}

	err = place(temporary.Name(), target, force)
	if err != nil {
		return Result{}, err
	}

	placed = true

	info, err := os.Stat(target)
	if err != nil {
		return Result{}, fmt.Errorf("reading %s: %w", target, err)
	}

	return Result{Path: target, Size: info.Size(), Manifest: built, Unstored: unstored}, nil
}

// place moves the verified archive to target: through a hard link where no
// replacing is allowed, so a file another process put there meanwhile is
// refused rather than replaced.
func place(temporary string, target string, force bool) error {
	if force {
		err := os.Rename(temporary, target)
		if err != nil {
			return fmt.Errorf("placing the archive at %s: %w", target, err)
		}

		return nil
	}

	err := os.Link(temporary, target)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s: %w; --force replaces it", target, ErrTargetExists)
	}

	if err == nil {
		return os.Remove(temporary)
	}

	_, statErr := os.Lstat(target)
	if statErr == nil {
		return fmt.Errorf("%s: %w; --force replaces it", target, ErrTargetExists)
	}

	err = os.Rename(temporary, target)
	if err != nil {
		return fmt.Errorf("placing the archive at %s: %w", target, err)
	}

	return nil
}

func readStores(ctx context.Context, env Env) ([]storeRead, error) {
	if env.Migration.Unmigrated() {
		return nil, fmt.Errorf("%w; any other agentic-helpers command migrates it, such as agentic-helpers check", ErrUnmigrated)
	}

	err := env.Roots.CheckFormat()
	if err != nil {
		return nil, err
	}

	dirs, err := env.Roots.StoreDirs()
	if err != nil {
		return nil, err
	}

	stores := make([]storeRead, 0, len(dirs))

	for _, dir := range dirs {
		read, err := readStore(ctx, env, dir)
		if err != nil {
			return nil, fmt.Errorf("reading the store %s: %w", dir.ID, err)
		}

		stores = append(stores, read)
	}

	return stores, nil
}

func readStore(ctx context.Context, env Env, dir home.StoreDir) (storeRead, error) {
	identity, raw, err := home.ReadIdentityFile(dir.Dir)
	if err != nil {
		return storeRead{}, err
	}

	read := storeRead{dir: dir, identity: identity}

	var compact bytes.Buffer

	err = json.Compact(&compact, raw)
	if err != nil {
		return storeRead{}, fmt.Errorf("reading its identity: %w", err)
	}

	read.identityRaw = compact.Bytes()

	read.migratedFrom = map[string]int{}

	if isDir(filepath.Join(dir.Dir, home.Memory)) {
		from, err := readComponent(ctx, env, dir, home.Memory, memory.StoreFormat, memory.MigrateCopy,
			func(folder string) error {
				exported, err := memory.Export(ctx, env.Roots, folder)
				read.memory = &exported

				return err
			})
		if err != nil {
			return storeRead{}, err
		}

		if from > 0 {
			read.migratedFrom[home.Memory] = from
		}
	}

	if isDir(filepath.Join(dir.Dir, home.Tools)) {
		from, err := readComponent(ctx, env, dir, home.Tools, tools.LibraryFormat, tools.MigrateCopy,
			func(folder string) error {
				exported, err := tools.Export(ctx, folder)
				read.tools = &exported

				return err
			})
		if err != nil {
			return storeRead{}, err
		}

		if from > 0 {
			read.migratedFrom[home.Tools] = from
		}
	}

	return read, nil
}

func isDir(dir string) bool {
	info, err := os.Lstat(dir)

	return err == nil && info.IsDir()
}

// plan is the manifest the stores make, and a line per attachment an entry
// names that the machine no longer holds.
func plan(env Env, stores []storeRead, exported time.Time) (Manifest, []string) {
	built := Manifest{
		Format: FormatName, Version: Version, Exported: exported.Format(time.RFC3339Nano), Producer: env.Producer,
		Stores: []ManifestStore{}, Attachments: &ManifestAttachments{},
	}

	stored := map[string]bool{}

	var unstored []string

	for _, read := range stores {
		one := ManifestStore{ID: read.dir.ID, Name: read.identity.Name(), Unclaimed: read.dir.Unclaimed,
			Formats: map[string]int{}, MigratedFrom: read.migratedFrom, Counts: map[string]int{}}

		if read.memory != nil {
			held := read.memory
			one.Formats[home.Memory] = held.Format
			one.Counts["entries"], one.Counts["seats"], one.Counts["shelves"] = len(held.Entries), len(held.Seats), len(held.Shelves)
			one.Counts["state"], one.Counts["names"], one.Counts["topic-merges"] = len(held.State), len(held.Names), len(held.TopicMerges)
			one.Counts["slices"] = len(held.Slices)

			for _, attachment := range held.Attachments {
				if !attachment.Stored {
					unstored = append(unstored, fmt.Sprintf("%s (%s) in %s: %s", attachment.Name, attachment.SHA256,
						one.Name, attachment.Problem))

					continue
				}

				if !stored[attachment.SHA256] {
					stored[attachment.SHA256] = true
					built.Attachments.Files++
					built.Attachments.Bytes += attachment.Size
				}
			}
		}

		if read.tools != nil {
			one.Formats[home.Tools] = read.tools.Format
			one.Counts["tools"] = len(read.tools.Tools)

			for _, tool := range read.tools.Tools {
				one.Counts["tool_versions"] += len(tool.Versions)
			}
		}

		built.Stores = append(built.Stores, one)
	}

	return built, unstored
}

// archiveWriter adds members to a pax tar inside a gzip stream.
type archiveWriter struct {
	tar      *tar.Writer
	modified time.Time
}

func (writer archiveWriter) add(name string, data []byte) error {
	err := writer.tar.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg, Name: name, Size: int64(len(data)), Mode: 0o600, ModTime: writer.modified,
		Format: tar.FormatPAX,
	})
	if err != nil {
		return fmt.Errorf("adding %s: %w", name, err)
	}

	_, err = writer.tar.Write(data)
	if err != nil {
		return fmt.Errorf("adding %s: %w", name, err)
	}

	return nil
}

// addLines adds a JSON Lines member, one line per record; one with no record
// is left out, as the format allows.
func (writer archiveWriter) addLines(name string, records []any) error {
	if len(records) == 0 {
		return nil
	}

	var lines bytes.Buffer

	encoder := json.NewEncoder(&lines)
	encoder.SetEscapeHTML(false)

	for _, record := range records {
		err := encoder.Encode(record)
		if err != nil {
			return fmt.Errorf("encoding %s: %w", name, err)
		}
	}

	return writer.add(name, lines.Bytes())
}

func asRecords[T any](held []T) []any {
	records := make([]any, len(held))
	for index, record := range held {
		records[index] = record
	}

	return records
}

func writeArchive(ctx context.Context, env Env, file *os.File, built Manifest, stores []storeRead, exported time.Time) error {
	compressed := gzip.NewWriter(file)
	writer := archiveWriter{tar: tar.NewWriter(compressed), modified: exported}

	manifest, err := plainJSON(built)
	if err != nil {
		return err
	}

	err = writer.add(manifestName, manifest)
	if err != nil {
		return err
	}

	attachments := map[string]memory.ExportAttachment{}

	for _, read := range stores {
		err = ctx.Err()
		if err != nil {
			return err
		}

		err = writeStore(writer, read)
		if err != nil {
			return err
		}

		if read.memory != nil {
			for _, attachment := range read.memory.Attachments {
				if attachment.Stored {
					attachments[attachment.SHA256] = attachment
				}
			}
		}
	}

	for _, sha := range slices.Sorted(maps.Keys(attachments)) {
		data, err := memory.ReadAttachment(env.Roots, attachments[sha])
		if err != nil {
			return err
		}

		err = writer.add(attachmentsDir+sha, data)
		if err != nil {
			return err
		}
	}

	err = writer.tar.Close()
	if err != nil {
		return err
	}

	err = compressed.Close()
	if err != nil {
		return err
	}

	return file.Sync()
}

type toolMeta struct {
	Name       string `json:"name"`
	Current    int    `json:"current,omitempty"`
	MergedInto string `json:"merged_into,omitempty"`
}

func writeStore(writer archiveWriter, read storeRead) error {
	prefix := storesDir + read.dir.ID + "/"

	err := writer.add(prefix+identityName, read.identityRaw)
	if err != nil {
		return err
	}

	if read.memory != nil {
		held := read.memory

		for _, file := range []struct {
			name    string
			records []any
		}{
			{"seats.jsonl", asRecords(held.Seats)},
			{"shelves.jsonl", asRecords(held.Shelves)},
			{"entries.jsonl", asRecords(held.Entries)},
			{"names.jsonl", asRecords(held.Names)},
			{"topic-merges.jsonl", asRecords(held.TopicMerges)},
			{"state.jsonl", held.State},
		} {
			err = writer.addLines(prefix+file.name, file.records)
			if err != nil {
				return err
			}
		}

		for _, slice := range held.Slices {
			err = writer.add(prefix+"slices/"+slice.Seat+".txt", slice.Data)
			if err != nil {
				return err
			}
		}
	}

	if read.tools == nil {
		return nil
	}

	for _, tool := range read.tools.Tools {
		err = writeTool(writer, prefix+"tools/"+tool.Name+"/", tool)
		if err != nil {
			return err
		}
	}

	return nil
}

func writeTool(writer archiveWriter, prefix string, tool tools.ToolExport) error {
	meta, err := plainJSON(toolMeta{Name: tool.Name, Current: tool.Current, MergedInto: tool.MergedInto})
	if err != nil {
		return err
	}

	err = writer.add(prefix+"meta.json", meta)
	if err != nil {
		return err
	}

	for _, journal := range []struct {
		name  string
		lines []json.RawMessage
	}{{"history.jsonl", tool.History}, {"takes.jsonl", tool.Takes}, {"pruned.jsonl", tool.Pruned}} {
		if len(journal.lines) == 0 {
			continue
		}

		var lines bytes.Buffer

		for _, line := range journal.lines {
			lines.Write(line)
			lines.WriteByte('\n')
		}

		err = writer.add(prefix+journal.name, lines.Bytes())
		if err != nil {
			return err
		}
	}

	for _, version := range tool.Versions {
		folder := prefix + "v" + strconv.Itoa(version.Number) + "/"

		for _, file := range version.Files {
			err = writer.add(folder+file.Name, file.Data)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func plainJSON(value any) ([]byte, error) {
	var encoded bytes.Buffer

	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)

	err := encoder.Encode(value)
	if err != nil {
		return nil, err
	}

	return bytes.TrimSuffix(encoded.Bytes(), []byte("\n")), nil
}

// Summary is what `export` prints: the archive, then a line per store, then
// the attachments, then each attachment it could not hold.
func Summary(result Result) []string {
	lines := []string{fmt.Sprintf("%s export: wrote %s (%d bytes)", Self, result.Path, result.Size)}

	for _, store := range result.Manifest.Stores {
		name := store.Name
		if store.Unclaimed {
			name += ", unclaimed"
		}

		var parts []string

		if _, held := store.Formats[home.Memory]; held {
			parts = append(parts, fmt.Sprintf("%d entries, %d seats, %d shelves, %d state lines, %d slices",
				store.Counts["entries"], store.Counts["seats"], store.Counts["shelves"], store.Counts["state"],
				store.Counts["slices"]))
		}

		if _, held := store.Formats[home.Tools]; held {
			parts = append(parts, fmt.Sprintf("%d tools, %d versions", store.Counts["tools"], store.Counts["tool_versions"]))
		}

		if len(parts) == 0 {
			parts = []string{"no memory, no tools"}
		}

		for _, component := range slices.Sorted(maps.Keys(store.MigratedFrom)) {
			parts = append(parts, fmt.Sprintf("%s read from a copy migrated from format %d", component,
				store.MigratedFrom[component]))
		}

		lines = append(lines, fmt.Sprintf("  %s (%s): %s", name, store.ID, strings.Join(parts, "; ")))
	}

	if len(result.Manifest.Stores) == 0 {
		lines = append(lines, "  no stores on this machine")
	}

	if result.Manifest.Attachments != nil {
		lines = append(lines, fmt.Sprintf("  attachments: %d files, %d bytes", result.Manifest.Attachments.Files,
			result.Manifest.Attachments.Bytes))
	}

	for _, missing := range result.Unstored {
		lines = append(lines, "  not stored on this machine, so not in the archive: "+missing)
	}

	return lines
}

// memberPath reports whether name is a member path the format allows:
// relative, slash-separated, with no empty, `.` or `..` segment.
func memberPath(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") || path.Clean(name) != name {
		return false
	}

	return !slices.ContainsFunc(strings.Split(name, "/"), func(segment string) bool {
		return segment == "" || segment == "." || segment == ".."
	})
}
