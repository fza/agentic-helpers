package export

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/fza/agentic-helpers/source/hooks/internal/formatsteps"
	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
)

// scratchDir holds, below the cache root, the copy of a store an export
// migrates: `<cache>/export-tmp/<id>/`, a home of its own with `data/` and
// `cache/`, so every step of the migration writes below it.
const scratchDir = "export-tmp"

// copyLimit bounds one file of a copied store: no store file comes near it.
const copyLimit = 4 << 30

// migrator brings a component of the store id below a scratch home to the
// format this release reads.
type migrator func(ctx context.Context, roots home.Roots, id string) error

// readComponent reads the component folder of a store for the archive. A
// format.json no release wrote refuses it, as a newer format does where read
// checks it. A folder at an older format than current is copied into the scratch home
// first and migrated there, and read from the copy: the store itself is
// never changed. It answers the format the store was at where it migrated a
// copy, else 0. The copy goes again however the read ends.
func readComponent(ctx context.Context, env Env, dir home.StoreDir, component string, current int, migrate migrator,
	read func(folder string) error,
) (migratedFrom int, err error) {
	source := filepath.Join(dir.Dir, component)

	format, err := formatsteps.Read(source)
	if err != nil {
		return 0, fmt.Errorf("reading the %s format: %w", component, err)
	}

	if format >= current {
		return 0, read(source)
	}

	err = removeScratch(env.Roots.Cache, dir.ID)
	if err != nil {
		return 0, err
	}

	missing := missingFolders(env.Roots.Cache)

	defer func() {
		err = errors.Join(err, removeScratch(env.Roots.Cache, dir.ID), removeMade(missing))
	}()

	scratch := home.Roots{
		Data:  filepath.Join(env.Roots.Cache, scratchDir, dir.ID, "data"),
		Cache: filepath.Join(env.Roots.Cache, scratchDir, dir.ID, "cache"),
	}
	copied := scratch.Store(dir.ID, component)

	err = copyTree(source, env.Roots.Cache, copied)
	if err != nil {
		return 0, fmt.Errorf("copying %s to migrate it: %w", source, err)
	}

	err = migrate(ctx, scratch, dir.ID)
	if err != nil {
		return 0, err
	}

	err = read(copied)
	if err != nil {
		return 0, err
	}

	return format, nil
}

// removeScratch removes the scratch copy of the store id, and the scratch
// folder once it holds no other; a copy that is not there is nothing to do.
func removeScratch(cache string, id string) error {
	held, err := safefs.At(cache, filepath.Join(cache, scratchDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("opening the export's scratch folder: %w", err)
	}

	err = held.RemoveAll(id)
	held.Close()

	if err != nil {
		return fmt.Errorf("removing the export's scratch copy: %w", err)
	}

	root, err := safefs.Root(cache)
	if err != nil {
		return nil
	}

	defer root.Close()

	_ = root.Remove(scratchDir)

	return nil
}

// missingFolders answers dir and each folder above it that does not exist,
// deepest first: the folders a write below dir makes, which an export
// removes again so it leaves the cache tree as it found it. A folder it
// cannot check ends the list: the copy reports what keeps it from writing.
func missingFolders(dir string) []string {
	var missing []string

	for folder := filepath.Clean(dir); ; folder = filepath.Dir(folder) {
		_, err := os.Lstat(folder)
		if !errors.Is(err, fs.ErrNotExist) {
			return missing
		}

		missing = append(missing, folder)

		if filepath.Dir(folder) == folder {
			return missing
		}
	}
}

// removeMade removes the folders missingFolders answered, deepest first. A
// folder something else wrote into since stays, and every folder above it.
func removeMade(made []string) error {
	for _, folder := range made {
		parent, err := safefs.Root(filepath.Dir(folder))
		if err != nil {
			return fmt.Errorf("removing the folders the export made: %w", err)
		}

		err = parent.Remove(filepath.Base(folder))
		parent.Close()

		switch {
		case err == nil, errors.Is(err, fs.ErrNotExist):
		case errors.Is(err, syscall.ENOTEMPTY), errors.Is(err, syscall.EEXIST):
			return nil
		default:
			return fmt.Errorf("removing %s, which the export made: %w", folder, err)
		}
	}

	return nil
}

// copyTree copies the folder from, its files and folders only, to the
// folder to below root, made where missing. A link or any other kind of
// entry refuses the copy: a store holds none.
func copyTree(from string, root string, to string) error {
	source, err := safefs.OpenDir(from)
	if err != nil {
		return err
	}

	defer source.Close()

	target, err := safefs.EnsureAt(root, to, 0o700)
	if err != nil {
		return err
	}

	defer target.Close()

	return copyInto(source, target)
}

func copyInto(source *safefs.Dir, target *safefs.Dir) error {
	listed, err := source.Entries()
	if err != nil {
		return err
	}

	for _, entry := range listed {
		name := entry.Name()

		switch {
		case entry.IsDir():
			err = copyFolder(source, target, name)
		case entry.Type().IsRegular():
			var data []byte

			data, err = source.Read(name, copyLimit)
			if err == nil {
				err = target.Create(name, data, 0o600)
			}
		default:
			err = fmt.Errorf("%s/%s is no regular file or folder", source.Path(), name)
		}

		if err != nil {
			return err
		}
	}

	return nil
}

func copyFolder(source *safefs.Dir, target *safefs.Dir, name string) error {
	from, err := source.Sub(name)
	if err != nil {
		return err
	}

	defer from.Close()

	to, err := target.EnsureSub(name, 0o700)
	if err != nil {
		return err
	}

	defer to.Close()

	return copyInto(from, to)
}
