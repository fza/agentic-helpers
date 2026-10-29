package migration

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
)

const (
	stampLayout   = "20060102T150405.000000000Z"
	readingSuffix = ".ctx"
	readingName   = "context.json"
)

// metadata are the small files copied to the legacy backup before anything
// moves. Bulk data is not copied: a failed run renames it back.
var metadata = []string{writersName, "seats.json", "shelves.json", formatName}

// migrate moves every old key into a store, the session tree and the
// attachments into the home, fences the old trees, and writes `format.json`
// last. A failure before it reverses every change; the journal goes once the
// run completes or its undo holds.
func (env Env) migrate(ctx context.Context) error {
	log, err := env.openJournal()
	if err != nil {
		return err
	}

	defer log.close()

	err = env.moveAll(ctx, log, env.now())
	if err == nil {
		err = log.checkpoint()
	}

	if err == nil {
		err = env.Roots.WriteFormat()
	}

	if err == nil {
		err = log.checkpoint()
	}

	if errors.Is(err, errHalted) {
		return err
	}

	if err != nil {
		undoErr := rollback(log.steps)
		if undoErr != nil {
			return errors.Join(err, undoErr)
		}

		return errors.Join(err, os.Remove(env.journalPath()))
	}

	finish(log.steps)

	return os.Remove(env.journalPath())
}

func (env Env) moveAll(ctx context.Context, log *journal, stamp time.Time) error {
	before, err := os.ReadDir(env.Roots.Data)
	if err != nil {
		return fmt.Errorf("reading %s: %w", env.Roots.Data, err)
	}

	names := make([]string, 0, len(before))
	for _, entry := range before {
		names = append(names, entry.Name())
	}

	err = log.record(step{Kind: stepData, Path: env.Roots.Data, Names: names})
	if err != nil {
		return err
	}

	err = env.backUp(stamp)
	if err != nil {
		return err
	}

	projects := env.survey()
	placed := env.place(ctx, projects)
	resolver := home.Resolver{Roots: env.Roots, Now: env.Now}
	suffix := "." + stamp.Format(stampLayout)

	for _, held := range projects {
		if !held.hasData() {
			continue
		}

		target, err := env.storeFor(ctx, log, resolver, held.key, placed[held.key], stamp)
		if err != nil {
			return err
		}

		if held.memory != "" {
			err = log.move(held.memory, target(home.Memory), aside(held.memory, suffix))
			if err == nil {
				err = log.placeStub(held.memory)
			}
		}

		if err == nil && held.tools != "" {
			err = log.move(held.tools, target(home.Tools), aside(held.tools, suffix))
		}

		if err != nil {
			return err
		}
	}

	for _, root := range []string{env.toolsRoot(), env.writersRoot()} {
		err = log.placeFence(root, aside(root, suffix), env.Roots.Data)
		if err != nil {
			return err
		}
	}

	sessions := filepath.Join(env.memoryRoot(), "sessions")
	if isDir(sessions) {
		err = log.move(sessions, env.Roots.Sessions(agent), aside(sessions, suffix))
		if err == nil {
			err = log.nestReadings(env.Roots.Sessions(agent))
		}

		if err != nil {
			return err
		}
	}

	shared := filepath.Join(env.memoryRoot(), attachments)
	if !isDir(shared) {
		return nil
	}

	return log.move(shared, env.Roots.Shared(attachments), aside(shared, suffix))
}

// nestReadings turns each context reading an earlier release kept as
// `<session>.ctx` into `<session>/context.json`, where memory reads it now.
func (log *journal) nestReadings(dir string) error {
	readings, err := filepath.Glob(filepath.Join(dir, "*"+readingSuffix))
	if err != nil {
		return err
	}

	for _, reading := range readings {
		session := filepath.Join(dir, strings.TrimSuffix(filepath.Base(reading), readingSuffix))

		err = log.created(session)
		if err != nil {
			return err
		}

		err = os.Mkdir(session, 0o700)
		if err != nil {
			return fmt.Errorf("nesting the reading %s: %w", reading, err)
		}

		err = log.record(step{Kind: stepRename, From: reading, To: filepath.Join(session, readingName)})
		if err != nil {
			return err
		}

		err = os.Rename(reading, filepath.Join(session, readingName))
		if err != nil {
			return fmt.Errorf("nesting the reading %s: %w", reading, err)
		}
	}

	return nil
}

// aside is where a tree waits beside its old place, hidden from every walk
// of the old layout.
func aside(path string, suffix string) string {
	return filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+suffix)
}

// storeFor creates the store a key's data moves into: an ordinary store where
// it has checkouts, an unclaimed one otherwise. It answers where each
// component of it goes.
func (env Env) storeFor(ctx context.Context, log *journal, resolver home.Resolver, key string, placed placement,
	stamp time.Time,
) (func(string) string, error) {
	if len(placed.checkouts) == 0 {
		id, err := resolver.Unclaim(ctx, key, placed.lastKnown, stamp)
		if err != nil {
			return nil, fmt.Errorf("leaving %s unclaimed: %w", key, err)
		}

		return func(component string) string { return env.Roots.UnclaimedStore(id, component) }, nil
	}

	for _, checkout := range placed.checkouts {
		if checkout.CommonDir == "" {
			continue
		}

		err := log.record(pointerStep(filepath.Join(checkout.CommonDir, home.PointerName)))
		if err != nil {
			return nil, err
		}
	}

	id, err := resolver.Adopt(ctx, placed.checkouts, []string{key})
	if err != nil {
		return nil, fmt.Errorf("adopting %s: %w", key, err)
	}

	return func(component string) string { return env.Roots.Store(id, component) }, nil
}

// pointerStep records what the pointer at path holds before a run writes it.
func pointerStep(path string) step {
	earlier, err := os.ReadFile(path)
	if err != nil {
		return step{Kind: stepPointer, Path: path}
	}

	return step{Kind: stepPointer, Path: path, Content: earlier, Existed: true}
}

// removeCreated removes every entry of the data root but those named before:
// what a failed run created there.
func removeCreated(data string, before []string) error {
	now, err := os.ReadDir(data)
	if err != nil {
		return err
	}

	var errs []error

	for _, entry := range now {
		if slices.Contains(before, entry.Name()) {
			continue
		}

		errs = append(errs, os.RemoveAll(filepath.Join(data, entry.Name())))
	}

	return errors.Join(errs...)
}

// backUp copies every writers list, seat registry, shelf list and format of
// the old trees to `<data>/backup/legacy-<UTC stamp>/`.
func (env Env) backUp(stamp time.Time) error {
	target := filepath.Join(env.Roots.Data, "backup", "legacy-"+stamp.Format(stampLayout))

	for _, root := range []string{env.memoryRoot(), env.toolsRoot(), env.writersRoot()} {
		if !isDir(root) {
			continue
		}

		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if entry.IsDir() && strings.HasPrefix(entry.Name(), ".") && path != root {
				return filepath.SkipDir
			}

			if !entry.Type().IsRegular() || !slices.Contains(metadata, entry.Name()) {
				return nil
			}

			relative, err := filepath.Rel(env.claude(), path)
			if err != nil {
				return err
			}

			return copyFileInto(path, filepath.Join(target, relative))
		})
		if err != nil {
			return fmt.Errorf("backing up the old metadata: %w", err)
		}
	}

	return nil
}
