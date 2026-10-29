// Package home maps checkouts to the stores every agentic-helpers component
// keeps its per-project data in, one agent-neutral home for the machine:
// `<data>/stores/<id>/<component>/` for what is kept, `<cache>/stores/<id>/`
// for what is rebuilt at will, `<data>/sessions/<agent>/<session>/` for what
// one session leaves.
//
// A store describes itself in `identity.json`: the checkouts admitted to it and
// the identifiers a checkout is matched by. `<data>/index.json` is only a cache
// of every identity, rebuilt whenever it is missing or disagrees with one. The
// pointer `<git common dir>/agentic-store` names a checkout's store, so every
// worktree of a repository and a moved checkout keep theirs.
//
// Any process able to write the home can edit a store, so the pointer and
// every identity are bookkeeping, never a boundary: a value read here decides
// which store a call works on, never whether it may.
package home

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/fza/agentic-helpers/source/hooks/internal/atomicfile"
)

// Format is the layout of the home this release reads and writes.
const Format = 1

const (
	name       = "agentic-helpers"
	formatName = "format.json"
	storesDir  = "stores"
	// sessionsDir holds what one session of one agent leaves.
	sessionsDir  = "sessions"
	unclaimedDir = "unclaimed"
	indexName    = "index.json"
	identityName = "identity.json"
	lockName     = ".lock"
	// PointerName is the file in a git common directory naming its store.
	PointerName = "agentic-store"
)

// Components keep their data under these names within a store.
const (
	Memory = "memory"
	Tools  = "tools"
)

var ErrNewerHome = errors.New("the agentic-helpers home has a newer format")

// Roots are the two trees of the home: Data holds what is kept, Cache what is
// rebuilt at will.
type Roots struct {
	Data  string
	Cache string
}

// Locate settles both roots: AGENTIC_HELPERS_HOME, else `$XDG_DATA_HOME`, else
// `~/.local/share`, for the data; `$XDG_CACHE_HOME`, else `~/.cache`, for the
// cache. A relative XDG value is ignored, as the XDG specification asks.
func Locate(userHome string, getenv func(string) string) Roots {
	lookup := func(key string) string {
		if getenv == nil {
			return ""
		}

		return getenv(key)
	}

	data := lookup("AGENTIC_HELPERS_HOME")
	if !filepath.IsAbs(data) {
		data = filepath.Join(xdg(lookup("XDG_DATA_HOME"), filepath.Join(userHome, ".local", "share")), name)
	}

	return Roots{
		Data:  filepath.Clean(data),
		Cache: filepath.Join(xdg(lookup("XDG_CACHE_HOME"), filepath.Join(userHome, ".cache")), name),
	}
}

func xdg(value string, fallback string) string {
	if filepath.IsAbs(value) {
		return value
	}

	return fallback
}

// Store is where a component keeps a store's data.
func (roots Roots) Store(id string, component string) string {
	return filepath.Join(roots.Data, storesDir, id, component)
}

// StoreCache is where a component keeps what it rebuilds of a store's data.
func (roots Roots) StoreCache(id string, component string) string {
	return filepath.Join(roots.Cache, storesDir, id, component)
}

// Stores holds every store.
func (roots Roots) Stores() string {
	return filepath.Join(roots.Data, storesDir)
}

// Session is where one session of agent keeps what it leaves behind.
func (roots Roots) Session(agent string, session string) string {
	return filepath.Join(roots.Data, sessionsDir, agent, session)
}

// Sessions holds every session of agent.
func (roots Roots) Sessions(agent string) string {
	return filepath.Join(roots.Data, sessionsDir, agent)
}

// Shared is a component's data serving every store, as memory's attachments.
func (roots Roots) Shared(component string) string {
	return filepath.Join(roots.Data, component)
}

// SharedCache is a component's cache serving every store.
func (roots Roots) SharedCache(component string) string {
	return filepath.Join(roots.Cache, component)
}

func (roots Roots) unclaimed() string { return filepath.Join(roots.Data, unclaimedDir) }

func (roots Roots) index() string { return filepath.Join(roots.Data, indexName) }

func (roots Roots) storeDir(id string) string { return filepath.Join(roots.Data, storesDir, id) }

func (roots Roots) unclaimedStore(id string) string { return filepath.Join(roots.unclaimed(), id) }

type formatFile struct {
	Format int `json:"format"`
}

// CheckFormat refuses a home of a newer format than this release reads. A
// home without `format.json` has never been written by any release.
func (roots Roots) CheckFormat() error {
	data, err := os.ReadFile(filepath.Join(roots.Data, formatName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("reading the home's format: %w", err)
	}

	var held formatFile

	err = json.Unmarshal(data, &held)
	if err != nil {
		return fmt.Errorf("reading the home's format: %w", err)
	}

	if held.Format > Format {
		return fmt.Errorf("%w: %s has format %d; this release reads up to %d", ErrNewerHome, roots.Data, held.Format,
			Format)
	}

	return nil
}

// FormatFile is the home's `format.json`, whose presence marks a home every
// release of this layout has finished writing.
func (roots Roots) FormatFile() string {
	return filepath.Join(roots.Data, formatName)
}

// WriteFormat writes `format.json` where the home has none yet.
func (roots Roots) WriteFormat() error {
	return roots.ensureFormat()
}

// ensureFormat writes `format.json` where the home has none yet.
func (roots Roots) ensureFormat() error {
	path := roots.FormatFile()

	_, err := os.Stat(path)
	if err == nil {
		return nil
	}

	data, err := json.Marshal(formatFile{Format: Format})
	if err != nil {
		return fmt.Errorf("encoding the home's format: %w", err)
	}

	return atomicfile.Write(path, append(data, '\n'), 0o600)
}
