package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/projectkey"
	"github.com/fza/agentic-helpers/source/hooks/internal/secrets"
)

var ErrLocked = errors.New("the store is locked")

// stores is every store a read may use, the own one first, each with its
// index loaded. AGENTIC_TOOLS_SCOPE=project keeps it to the own store.
func (lib *library) stores(ctx context.Context) []*store {
	if lib.reach != nil {
		return lib.reach
	}

	keys := []string{}
	if !lib.scopeProject() {
		keys = storeKeys(lib.root())
	}

	if !slices.Contains(keys, lib.key) {
		keys = append(keys, lib.key)
	}

	named := labels(keys)

	own := &store{key: lib.key, label: named[lib.key], path: lib.ownDir(), own: true}
	reach := []*store{own}

	for _, key := range keys {
		if key != lib.key {
			reach = append(reach, &store{key: key, label: named[key], path: projectkey.Dir(lib.root(), key)})
		}
	}

	for _, held := range reach {
		held.index = lib.indexOf(ctx, held)
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

// rebuild reads a store's folders, reusing every vector the cache holds for
// an unchanged description and embedding only what lacks one.
func (lib *library) rebuild(ctx context.Context, at string) (*index, error) {
	cache := lib.loadVectors()

	built, err := buildIndex(at, cache.rows)
	if err != nil {
		return nil, err
	}

	built.Fingerprint = cache.fingerprint

	var (
		missing []*indexed
		texts   []string
	)

	// A description holding a credential, left on disk by an earlier release or
	// another process, is never embedded: its tool stays unvectored.
	for _, name := range sortedKeys(built.Tools) {
		row := built.Tools[name]
		text := describe(row.spec())

		if len(row.Vector) == 0 && len(secrets.Scan(ctx, text, "")) == 0 {
			missing = append(missing, row)
			texts = append(texts, text)
		}
	}

	if len(missing) == 0 || cache.embedder == nil {
		return built, nil
	}

	bounded, cancel := context.WithTimeout(ctx, embedLimit)
	defer cancel()

	vectors, err := cache.embedder.Embed(bounded, texts)
	if err != nil || len(vectors) != len(missing) {
		return built, nil
	}

	for position, row := range missing {
		row.Vector = vectors[position]
		cache.rows[row.Described] = row.Vector
	}

	lib.saveVectors(cache)

	return built, nil
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

// vectorCache keeps every description's vector by the description's hash,
// outside every store, so a rebuild never embeds an unchanged description.
type vectorCache struct {
	fingerprint string
	embedder    interface {
		Embed(ctx context.Context, texts []string) ([][]float32, error)
	}
	rows map[string][]float32
}

func (lib *library) vectorsPath(fingerprint string) string {
	return filepath.Join(lib.env.CacheRoot, self, "vectors", describedSum(fingerprint)+".json")
}

func (lib *library) loadVectors() *vectorCache {
	cache := &vectorCache{rows: map[string][]float32{}}

	embedder, err := lib.embedder()
	if err != nil {
		return cache
	}

	cache.embedder, cache.fingerprint = embedder, embedder.Fingerprint()

	data, err := os.ReadFile(lib.vectorsPath(cache.fingerprint))
	if err == nil {
		_ = json.Unmarshal(data, &cache.rows)
	}

	if cache.rows == nil {
		cache.rows = map[string][]float32{}
	}

	return cache
}

func (lib *library) saveVectors(cache *vectorCache) {
	if cache.fingerprint == "" {
		return
	}

	data, err := json.Marshal(cache.rows)
	if err != nil {
		return
	}

	folder, err := ensureDir(filepath.Join(lib.env.CacheRoot, self), "vectors")
	if err != nil {
		return
	}

	defer folder.close()

	_ = folder.replace(filepath.Base(lib.vectorsPath(cache.fingerprint)), data)
}

// writeIndex replaces the own store's index; only the own store is written.
func (lib *library) writeIndex(held *index) error {
	data, err := json.Marshal(held)
	if err != nil {
		return fmt.Errorf("encoding the index: %w", err)
	}

	if len(data) > indexLimit {
		return fmt.Errorf("the index would pass %d bytes; merge tools before adding more", indexLimit)
	}

	store, err := lib.ownStore()
	if err != nil {
		return err
	}

	defer store.close()

	return store.replace("index.json", data)
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
	store, err := lib.ownStore()
	if err != nil {
		return fmt.Errorf("creating the store: %w", err)
	}

	defer store.close()

	file, err := store.appendTo(".lock")
	if err != nil {
		return fmt.Errorf("opening the store lock: %w", err)
	}

	defer func() { _ = file.Close() }()

	wait := lib.env.LockWait
	if wait == 0 {
		wait = 2 * time.Second
	}

	deadline := time.Now().Add(wait)

	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}

		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) || ctx.Err() != nil {
			return lockRefusal(store)
		}

		time.Sleep(25 * time.Millisecond)
	}

	defer func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }()

	_ = file.Truncate(0)
	_, _ = file.Write([]byte(strconv.Itoa(os.Getpid()) + "\n"))

	err = admit(ctx)
	if err != nil {
		return err
	}

	return write()
}

func lockRefusal(store *dir) error {
	data, err := store.read(".lock", currentLimit)
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
