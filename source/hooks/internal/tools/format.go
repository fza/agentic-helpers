package tools

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/formatsteps"
	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
	"github.com/fza/agentic-helpers/source/hooks/internal/space"
)

// libraryFormat is a tool store's layout: each tool's history, takes and
// pruned versions as journals of typed records, each session's state in the
// cache's slot file, and the size ledger. A store without format.json is
// format 1: JSON-lines files beside each tool and a JSON file per session
// under `sessions/`.
const libraryFormat = 2

// prepareStore brings the store at at to libraryFormat through its step
// table (internal/formatsteps); every caller holds the store's lock already.
func prepareStore(ctx context.Context, store *safefs.Dir, roots home.Roots, at string, cache string) error {
	return formatsteps.Run(ctx, libraryComponent(store, roots, at, cache))
}

func libraryComponent(store *safefs.Dir, roots home.Roots, at string, cache string) formatsteps.Component {
	return formatsteps.Component{
		Name: at, Dir: at, Root: roots.Data,
		Lock: func(context.Context) (func(), error) { return func() {}, nil },
		Steps: []formatsteps.Step{{From: 1, To: libraryFormat, Ops: func(context.Context) ([]formatsteps.Operation, error) {
			return journalSteps(store, roots, at, cache), nil
		}}},
	}
}

// journalSteps move a format-1 store to format 2: every journal is written
// beside its legacy file, a journal already there kept, the sessions go into
// the slot file, a session the slot file already holds not written again, and
// only then do the legacy files go.
func journalSteps(store *safefs.Dir, roots home.Roots, at string, cache string) []formatsteps.Operation {
	return []formatsteps.Operation{
		{Name: "converting the journals", Apply: func(context.Context) error {
			return eachFolder(store, func(folder *safefs.Dir) error {
				err := convertJournals(folder)
				if err != nil {
					return fmt.Errorf("converting %s: %w", folder.Path(), err)
				}

				return nil
			})
		}},
		{Name: "taking in the session files", Apply: func(ctx context.Context) error {
			return slots.Open(roots.Cache, filepath.Join(cache, "sessions.slots"), sessionSlotWidth).Import(ctx, legacySessions(at))
		}},
		{Name: "removing the legacy files", Apply: func(context.Context) error {
			return eachFolder(store, func(folder *safefs.Dir) error {
				for _, held := range journals {
					err := removeLegacy(folder, held)
					if err != nil {
						return err
					}
				}

				return nil
			})
		}},
		{Name: "removing the session files", Apply: func(context.Context) error {
			return store.RemoveAll("sessions")
		}},
	}
}

func eachFolder(store *safefs.Dir, change func(folder *safefs.Dir) error) error {
	folders, err := toolFolders(store)
	if err != nil {
		return err
	}

	defer closeAll(folders)

	for _, folder := range folders {
		err = change(folder)
		if err != nil {
			return err
		}
	}

	return nil
}

func closeAll(folders []*safefs.Dir) {
	for _, folder := range folders {
		folder.Close()
	}
}

// toolFolders opens every tool folder of the store.
func toolFolders(store *safefs.Dir) ([]*safefs.Dir, error) {
	listed, err := store.Entries()
	if err != nil {
		return nil, err
	}

	var folders []*safefs.Dir

	for _, entry := range listed {
		name := entry.Name()
		if !entry.IsDir() || strings.HasPrefix(name, ".") || name == "sessions" || name == "copies" {
			continue
		}

		folder, err := store.Sub(name)
		if err != nil {
			continue
		}

		folders = append(folders, folder)
	}

	return folders, nil
}

func legacySessions(at string) []slots.Legacy {
	paths, _ := filepath.Glob(filepath.Join(at, "sessions", "*.json"))

	legacy := make([]slots.Legacy, 0, len(paths))

	for _, path := range paths {
		session := strings.TrimSuffix(filepath.Base(path), ".json")
		if sessionShape.MatchString(session) {
			legacy = append(legacy, slots.Legacy{Path: path, Key: "session/" + session})
		}
	}

	return legacy
}

// newer says why a store of a format this release does not read is left
// alone, empty for one it reads.
func newer(at string) string {
	_, err := home.CheckComponent(at, libraryFormat)
	if errors.Is(err, home.ErrNewerFormat) {
		return err.Error()
	}

	return ""
}

// refreshLedger rewrites the store's size ledger: every file below it, each
// file the index hashes for a stored version counted as stored at the size
// the index recorded, and held where the version is not prunable.
func refreshLedger(ctx context.Context, held *store, named map[forkOrigin]bool) {
	files := map[string]space.Account{}

	for _, version := range versionsOf(held, named) {
		for _, hash := range held.index.Tools[version.tool].Hashes[versionDir(version.v)] {
			files[version.tool+"/"+versionDir(version.v)+"/"+hash.Name] = space.Account{
				Counted: true, Held: !version.prunable, Bytes: min(max(hash.Size, 0), toolLimit),
			}
		}
	}

	_ = space.Refresh(ctx, held.root, held.path, func(name string) space.Account {
		account, counted := files[name]
		if !counted {
			return space.Account{Bytes: -1}
		}

		return account
	})
}

// ledgerMissing says the store keeps no size ledger yet, or is of an earlier
// format: one the SessionStart pass writes.
func ledgerMissing(at string) bool {
	version, err := home.ComponentFormat(at)
	if err != nil || version < libraryFormat {
		return true
	}

	_, err = os.Lstat(filepath.Join(at, space.LedgerName))

	return err != nil
}

// prepare brings the own store to libraryFormat before the call reads it, so
// a session's state an earlier release kept is in its slot by the time the
// call loads it. A store it cannot lock now is moved by the next write.
func (lib *library) prepare(ctx context.Context) {
	if lib.key == "" || !exists(lib.ownDir()) {
		return
	}

	version, err := home.ComponentFormat(lib.ownDir())
	if err != nil || version >= libraryFormat {
		return
	}

	store, err := safefs.OpenDir(lib.ownDir())
	if err != nil {
		return
	}

	defer store.Close()

	unlock, err := lockStore(ctx, store, cmp.Or(lib.env.LockWait, 2*time.Second))
	if err != nil {
		return
	}

	defer unlock()

	_ = prepareStore(ctx, store, lib.env.Roots, lib.ownDir(), lib.env.Roots.StoreCache(lib.key, home.Tools))
}
