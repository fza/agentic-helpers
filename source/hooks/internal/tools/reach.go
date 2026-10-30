package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
)

var ErrLocked = errors.New("the store is locked")

// stores is every store a read may use, the own one first, each with its
// index loaded. A store of a format newer than this release reads is left
// out; the own one then reads as empty, and every write to it is refused. AGENTIC_TOOLS_SCOPE=project keeps it to the own store.
func (lib *library) stores(ctx context.Context) []*store {
	if lib.reach != nil {
		return lib.reach
	}

	keys := []string{}
	if !lib.scopeProject() {
		keys = storeIDs(lib.env.Roots)
	}

	if !slices.Contains(keys, lib.key) {
		keys = append(keys, lib.key)
	}

	named := labels(lib.storeNames(keys))

	own := &store{key: lib.key, label: named[lib.key], path: lib.ownDir(), own: true}
	reach := []*store{own}

	for _, key := range keys {
		at := lib.env.Roots.Store(key, home.Tools)
		if key != lib.key && newer(at) == "" {
			reach = append(reach, &store{key: key, label: named[key], path: at})
		}
	}

	for _, held := range reach {
		held.index = emptyIndex()
		if newer(held.path) == "" {
			held.index = lib.indexOf(ctx, held)
		}
	}

	lib.reach = reach

	return reach
}

func (lib *library) own(ctx context.Context) *store {
	return lib.stores(ctx)[0]
}

// forget drops the loaded stores, so the next read sees what a write left.
func (lib *library) forget() { lib.reach = nil }

// indexOf is a store's index as stored, else rebuilt from its tool folders;
// the own store's rebuilt index is written back under the lock.
func (lib *library) indexOf(ctx context.Context, held *store) *index {
	loaded, found := loadIndex(held.path)
	if found {
		return loaded
	}

	rebuilt, err := lib.rebuild(ctx, held.path)
	if err != nil {
		return emptyIndex()
	}

	if held.own && exists(held.path) {
		_ = lib.recording(ctx, func() error { return lib.writeIndex(rebuilt) })
	}

	return rebuilt
}

const embedLimit = 20 * time.Second

func sortedKeys[V any](held map[string]V) []string {
	keys := make([]string, 0, len(held))
	for key := range held {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}

// writeIndex replaces the own store's index.
func (lib *library) writeIndex(held *index) error {
	store, err := lib.ownStore()
	if err != nil {
		return err
	}

	defer store.Close()

	return writeIndexInto(store, held)
}

// writeIndexInto replaces a store's index. Only a write to the own store and
// a prune to make room write one.
func writeIndexInto(store *safefs.Dir, held *index) error {
	data, err := json.Marshal(held)
	if err != nil {
		return fmt.Errorf("encoding the index: %w", err)
	}

	if len(data) > indexLimit {
		return fmt.Errorf("the index would pass %d bytes; merge tools before adding more", indexLimit)
	}

	return store.Replace("index.json", data, 0o600)
}

// locked runs write holding the own store's lock, waiting at most LockWait.
// The lock file records its holder's pid, which a refusal names.
func (lib *library) locked(ctx context.Context, write func() error) error {
	return lib.lockedAs(ctx, lib.writer, write)
}

// recording runs a record (a take line, an index a read rebuilt) under the
// lock, claiming nothing: a record is no reason to own a project.
func (lib *library) recording(ctx context.Context, write func() error) error {
	return lib.lockedAs(ctx, lib.recorder, write)
}

func (lib *library) lockedAs(ctx context.Context, admit func(context.Context) error, write func() error) error {
	// The own store's folder is made only once the checkout is bound to it.
	err := admit(ctx)
	if err != nil {
		return err
	}

	store, err := lib.ownStore()
	if err != nil {
		return fmt.Errorf("creating the store: %w", err)
	}

	defer store.Close()

	wait := lib.env.LockWait
	if wait == 0 {
		wait = 2 * time.Second
	}

	unlock, err := lockStore(ctx, store, wait)
	if err != nil {
		return err
	}

	defer unlock()

	err = prepareStore(ctx, store, lib.ownDir(), lib.env.Roots.StoreCache(lib.key, home.Tools))
	if err != nil {
		return fmt.Errorf("bringing the store to this release's format: %w", err)
	}

	err = write()
	if err != nil {
		return err
	}

	lib.forget()

	stores := lib.stores(ctx)
	refreshLedger(ctx, stores[0], origins(stores))

	return nil
}

// lockStore takes a store's lock, waiting at most wait, and answers how to let
// it go.
func lockStore(ctx context.Context, store *safefs.Dir, wait time.Duration) (func(), error) {
	file, err := store.Append(".lock", 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening the store lock: %w", err)
	}

	deadline := time.Now().Add(wait)

	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}

		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) || ctx.Err() != nil {
			_ = file.Close()

			return nil, lockRefusal(store)
		}

		time.Sleep(25 * time.Millisecond)
	}

	_ = file.Truncate(0)
	_, _ = file.Write([]byte(strconv.Itoa(os.Getpid()) + "\n"))

	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}

func lockRefusal(store *safefs.Dir) error {
	data, err := store.Read(".lock", currentLimit)
	if err == nil {
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err == nil && pid > 0 {
			return fmt.Errorf("%w by pid %d; try again once it finishes", ErrLocked, pid)
		}
	}

	return fmt.Errorf("%w; try again once its writer finishes", ErrLocked)
}

// found is one tool a name reached.
type found struct {
	store *store
	tool  *indexed
	// via names the merged name the call gave, where it reached a survivor.
	via string
}

func (hit found) shown() string {
	if hit.store.own {
		return hit.tool.Name
	}

	return hit.tool.Name + "@" + hit.store.label
}

// resolve reaches a tool by a bare name (own first, then a unique foreign one)
// or by `name@label`. A merged name reaches its survivor.
func (lib *library) resolve(ctx context.Context, given string) (found, error) {
	name, label, qualified := strings.Cut(given, "@")

	var candidates []*store

	for _, held := range lib.stores(ctx) {
		if !qualified || held.label == label {
			candidates = append(candidates, held)
		}
	}

	if qualified && len(candidates) == 0 {
		return found{}, fmt.Errorf("no project is labelled %q; `%s list` names them", label, self)
	}

	var hits []found

	for _, held := range candidates {
		hit, reached := reach(held, name)
		if !reached {
			continue
		}

		if held.own || qualified {
			return hit, nil
		}

		hits = append(hits, hit)
	}

	switch len(hits) {
	case 0:
		return found{}, fmt.Errorf("no tool %s; `%s find --term %s` searches for one", given, self, name)
	case 1:
		return hits[0], nil
	}

	var named []string
	for _, hit := range hits {
		named = append(named, hit.shown())
	}

	return found{}, fmt.Errorf("%s names %s; give one of them", name, strings.Join(named, " and "))
}

func reach(held *store, name string) (found, bool) {
	tool, present := held.tool(name)
	if present {
		return found{store: held, tool: tool}, true
	}

	survivor, merged := held.index.Merged[name]
	if !merged {
		return found{}, false
	}

	tool, present = held.tool(survivor)

	return found{store: held, tool: tool, via: name}, present
}

// isMissing says whether err is a file being absent rather than refused.
func isMissing(err error) bool { return errors.Is(err, fs.ErrNotExist) }
