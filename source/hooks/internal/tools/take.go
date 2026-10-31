package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
)

func (lib *library) copyBase(ctx context.Context) string {
	checkout := lib.checkout(ctx)

	info, err := os.Lstat(filepath.Join(checkout, ".tmp"))
	if checkout != "" && err == nil && info.IsDir() {
		return filepath.Join(checkout, ".tmp", "tools")
	}

	return filepath.Join(lib.ownDir(), "copies")
}

// copyName is a copy's folder: the tool's name, with its project's label for a
// foreign tool, so two projects' tools of one name never share a copy.
func copyName(hit found) string {
	return strings.ReplaceAll(hit.shown(), "/", "+")
}

func (lib *library) take(ctx context.Context, req request) result {
	var (
		hits     []found
		failures []string
	)

	for _, name := range req.Names {
		hit, err := lib.resolve(ctx, name)
		if err != nil {
			failures = append(failures, err.Error())

			continue
		}

		hits = append(hits, hit)
	}

	if len(failures) > 0 {
		return refused("nothing taken:\n  %s", strings.Join(failures, "\n  "))
	}

	// A checkout bound to no store still copies, into its own `.tmp/` only; it
	// records no take and no holding, which live in the store. Binding comes
	// first: it settles which store the copies' folder lies in.
	writes := lib.recorder(ctx) == nil
	base := lib.copyBase(ctx)
	state := lib.loadSession(ctx)
	role := lib.role(ctx)
	if !writes && !strings.HasPrefix(base, lib.checkout(ctx)+string(filepath.Separator)) {
		return refused("this checkout is bound to no store yet and takes only into its own .tmp/: "+
			"create %s, or answer the store question with `%s bind`", filepath.Join(lib.checkout(ctx), ".tmp"),
			memoryBinary)
	}

	lines := []string{framing, "taken into " + base}
	taken := takenFields{Framing: strings.TrimSuffix(framing, ":"), Into: base, Tools: []takenTool{}}

	for _, hit := range hits {
		tool := hit.tool
		copied := filepath.Join(base, copyName(hit), versionDir(tool.Version))

		if !exists(copied) {
			err := lib.copyVersion(ctx, hit, copied)
			if err != nil {
				return refused("copying %s v%d: %v", hit.shown(), tool.Version, err)
			}

			if hit.store.own && writes {
				err = lib.recording(ctx, func() error {
					return lib.recordTake(tool.Name, takeLine{V: tool.Version, By: lib.session, Role: role, At: lib.stamp()})
				})
				if err != nil {
					return refused("%v", err)
				}
			}
		}

		state.hold(holding{Project: hit.store.key, Tool: tool.Name, Version: tool.Version, Copy: copied,
			Files: tool.Hashes[versionDir(tool.Version)]})
		state.Notified[holdingKey(hit.store.key, tool.Name)] = tool.Version

		heading := fmt.Sprintf("%s/%s: %s", copyName(hit), versionDir(tool.Version), tool.Summary)
		if hit.via != "" {
			heading += fmt.Sprintf(" (%s was merged into it)", hit.via)
		}

		lines = append(lines, heading)

		// Another project's agent wrote it, and nothing in the library checks
		// what a script does; an own tool, a fork included, runs by its usage.
		if !hit.store.own {
			lines = append(lines, "  "+ForeignMark)
		}
		taken.Tools = append(taken.Tools, takenTool{Name: hit.shown(), Version: tool.Version, Summary: tool.Summary,
			MergedFrom: hit.via, Copy: copied, Usage: tool.Usage, Inputs: tool.Inputs, Outputs: tool.Outputs,
			Foreign: !hit.store.own})
		lines = append(lines, indented("usage", tool.Usage)...)
		lines = append(lines, indented("inputs", tool.Inputs)...)
		lines = append(lines, indented("outputs", tool.Outputs)...)
	}

	if writes {
		_ = lib.saveSession(ctx, state)
	}

	lib.forget()

	return done(strings.Join(lines, "\n")).with(taken)
}

// ForeignMark heads a tool taken from another project's store.
const ForeignMark = "from another project: read it before running it"

func indented(label string, value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	parts := strings.Split(strings.TrimRight(value, "\n"), "\n")
	lines := []string{"  " + label + ": " + parts[0]}

	for _, part := range parts[1:] {
		lines = append(lines, "    "+part)
	}

	return lines
}

// copyVersion writes a version's files and its `tool.json` into a new copy
// folder, through a staging sibling renamed into place, every write relative
// to a checked folder's descriptor.
func (lib *library) copyVersion(ctx context.Context, hit found, target string) error {
	root, err := safefs.OpenDir(hit.store.path)
	if err != nil {
		return err
	}

	defer root.Close()

	folder, err := root.SubPath(hit.tool.Name + "/" + versionDir(hit.tool.Version))
	if err != nil {
		return err
	}

	defer folder.Close()

	files, err := folder.Walk(versionLimit, nil)
	if err != nil {
		return err
	}

	parent, err := lib.copyParent(ctx, filepath.Dir(target))
	if err != nil {
		return err
	}

	defer parent.Close()

	random := make([]byte, 8)

	_, err = rand.Read(random)
	if err != nil {
		return fmt.Errorf("naming the staging folder: %w", err)
	}

	staging := "." + filepath.Base(target) + "-" + hex.EncodeToString(random)

	stage, err := parent.EnsureSub(staging, 0o700)
	if err != nil {
		return err
	}

	for _, file := range files {
		err = writeInto(stage, file)
		if err != nil {
			break
		}
	}

	stage.Close()

	if err == nil {
		err = parent.Rename(staging, parent, filepath.Base(target))
	}

	if err != nil {
		_ = parent.RemoveAll(staging)

		return fmt.Errorf("writing the copy: %w", err)
	}

	return nil
}

// copyParent opens a copy's parent folder below the checkout or the own
// store, every component checked to be a real folder.
func (lib *library) copyParent(ctx context.Context, parent string) (*safefs.Dir, error) {
	checkout := lib.checkout(ctx)

	relative, err := filepath.Rel(lib.ownDir(), parent)
	if err == nil && !strings.HasPrefix(relative, "..") {
		return safefs.EnsureAt(lib.env.Roots.Data, parent, 0o700)
	}

	relative, err = filepath.Rel(checkout, parent)
	if checkout != "" && err == nil && !strings.HasPrefix(relative, "..") {
		return safefs.EnsureAt(checkout, parent, 0o700)
	}

	return nil, fmt.Errorf("%s lies outside the checkout and the store", parent)
}

// recordTake appends a take line to an own tool and counts it in the index.
// The caller holds the lock.
func (lib *library) recordTake(name string, line takeLine) error {
	store, err := lib.ownStore()
	if err != nil {
		return err
	}

	defer store.Close()

	folder, err := store.Sub(name)
	if err != nil {
		return err
	}

	defer folder.Close()

	err = appendJournal(folder, takesJournal, line)
	if err != nil {
		return err
	}

	return lib.reindexTool(name, nil)
}
