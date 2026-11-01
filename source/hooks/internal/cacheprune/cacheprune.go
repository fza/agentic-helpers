// Package cacheprune deletes the vector stores no binary has used within the
// retention period, after sdd's automatic cache pruning: an embedding-model
// change starts a new store per fingerprint, and the old one would otherwise
// stay for good. A run starts at most once a day, detached, so no hook, call
// or command waits for it; each run records its outcome, and a failed one is
// told once.
package cacheprune

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/atomicfile"
	"github.com/fza/agentic-helpers/source/hooks/internal/filelock"
	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/mcpserver"
	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
	"github.com/fza/agentic-helpers/source/hooks/internal/vectorseg"
)

// StatusName is the run's record under the data root: when the last run
// started and finished, what it deleted, and its error.
const StatusName = "cache-prune.json"

const (
	lockName = "cache-prune.lock"
	runEvery = 24 * time.Hour
	// Command is the `agentic-helpers` subcommand a detached run executes.
	Command = "prune"
	helpers = "agentic-helpers"
	// statusLimit bounds the status read: it holds one run.
	statusLimit = 1 << 20
)

// Status is a run's record. Warned says the next SessionStart told its
// error already, so a failure is told once and never again until a run
// fails anew.
type Status struct {
	Started  string   `json:"started,omitempty"`
	Finished string   `json:"finished,omitempty"`
	Deleted  []string `json:"deleted,omitempty"`
	Error    string   `json:"error,omitempty"`
	Warned   bool     `json:"warned,omitempty"`
}

// Starter starts a process that outlives its caller.
type Starter interface {
	Start(ctx context.Context, path string, args ...string) error
}

func statusPath(roots home.Roots) string { return filepath.Join(roots.Data, StatusName) }

func readStatus(roots home.Roots) Status {
	var held Status

	file, err := os.Open(statusPath(roots))
	if err != nil {
		return held
	}

	defer func() { _ = file.Close() }()

	data := make([]byte, statusLimit)
	read, _ := file.Read(data)
	_ = json.Unmarshal(data[:read], &held)

	return held
}

func writeStatus(roots home.Roots, held Status) error {
	data, err := json.MarshalIndent(held, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the cache prune status: %w", err)
	}

	err = safefs.MkdirAll(roots.Data, roots.Data, 0o700)
	if err != nil {
		return fmt.Errorf("creating the data root: %w", err)
	}

	err = atomicfile.Write(roots.Data, statusPath(roots), data, 0o600)
	if err != nil {
		return fmt.Errorf("writing the cache prune status: %w", err)
	}

	return nil
}

// locked runs change on the status under its lock, taken without waiting:
// another process holding it is claiming or recording a run already.
func locked(roots home.Roots, change func(held *Status) bool) (bool, error) {
	err := safefs.MkdirAll(roots.Data, roots.Data, 0o700)
	if err != nil {
		return false, fmt.Errorf("creating the data root: %w", err)
	}

	file, err := safefs.Lock(roots.Data, filepath.Join(roots.Data, lockName), 0o600)
	if err != nil {
		return false, fmt.Errorf("opening the cache prune lock: %w", err)
	}

	defer func() { _ = file.Close() }()

	taken, err := filelock.TryExclusive(file)
	if err != nil || !taken {
		return false, err
	}

	defer func() { _ = filelock.Unlock(file) }()

	held := readStatus(roots)
	if !change(&held) {
		return false, nil
	}

	return true, writeStatus(roots, held)
}

// Claim takes the day's run: where no run started within the last day, it
// stamps now as the start and answers true, so of every process asking the
// first alone starts one.
func Claim(roots home.Roots, now time.Time) (bool, error) {
	return locked(roots, func(held *Status) bool {
		started, err := time.Parse(time.RFC3339, held.Started)
		if err == nil && now.Sub(started) < runEvery && !started.After(now) {
			return false
		}

		held.Started = now.UTC().Format(time.RFC3339)

		return true
	})
}

// Start claims the day's run and starts `agentic-helpers prune` beside
// executable, detached, so its caller never waits for it.
func Start(ctx context.Context, roots home.Roots, starter Starter, executable string, now time.Time) error {
	if starter == nil || executable == "" {
		return nil
	}

	claimed, err := Claim(roots, now)
	if err != nil || !claimed {
		return err
	}

	return starter.Start(ctx, filepath.Join(filepath.Dir(executable), helpers), Command)
}

// Idle runs base, then starts the day's run, as an MCP server's idle work: a
// machine working only through long-running servers still prunes.
func Idle(base mcpserver.Idle, roots home.Roots, starter Starter, executable string, now func() time.Time) mcpserver.Idle {
	return mcpserver.Idle{After: base.After, Run: func(ctx context.Context) {
		if base.Run != nil {
			base.Run(ctx)
		}

		_ = Start(ctx, roots, starter, executable, now())
	}}
}

// held is one vector store on the machine and the folder its removal takes.
type held struct {
	store  *vectorseg.Store
	parent string
	name   string
}

// stores is every vector store on the machine: the tool library's, one per
// embedder below the shared tools cache, and memory's, one per embedder below
// each store's memory cache.
func stores(roots home.Roots) []held {
	var found []held

	tools := filepath.Join(roots.SharedCache(home.Tools), "vectors")
	for _, name := range folders(tools) {
		found = append(found, held{store: vectorseg.Open(roots.Cache, filepath.Join(tools, name)), parent: tools, name: name})
	}

	storesRoot := filepath.Join(roots.Cache, "stores")
	for _, id := range folders(storesRoot) {
		memory := roots.StoreCache(id, home.Memory)

		for _, name := range folders(memory) {
			segments := filepath.Join(memory, name, "segments")

			info, err := os.Lstat(segments)
			if err != nil || !info.IsDir() {
				continue
			}

			found = append(found, held{store: vectorseg.Open(roots.Cache, segments), parent: memory, name: name})
		}
	}

	return found
}

func folders(at string) []string {
	listed, err := os.ReadDir(at)
	if err != nil {
		return nil
	}

	var names []string

	for _, entry := range listed {
		if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
			names = append(names, entry.Name())
		}
	}

	slices.Sort(names)

	return names
}

// Run deletes every vector store no binary used within retention before
// now, skipping a store another process holds, then records the run. A store
// goes in a lock session of its own, so a write never waits behind the run.
func Run(ctx context.Context, roots home.Roots, retention time.Duration, now time.Time) Status {
	result := Status{Started: now.UTC().Format(time.RFC3339)}

	var errs []error

	for _, candidate := range stores(roots) {
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())

			break
		}

		deleted, err := prune(candidate, now.Add(-retention))
		if err != nil {
			errs = append(errs, err)
		}

		if deleted {
			result.Deleted = append(result.Deleted, filepath.Join(candidate.parent, candidate.name))
		}
	}

	result.Finished = now.UTC().Format(time.RFC3339)

	joined := errors.Join(errs...)
	if joined != nil {
		result.Error = joined.Error()
	}

	_, err := locked(roots, func(stored *Status) bool {
		started := stored.Started
		*stored = result
		stored.Started = cmpOr(started, result.Started)

		return true
	})
	if err != nil && result.Error == "" {
		result.Error = err.Error()
	}

	return result
}

func cmpOr(first string, second string) string {
	if first != "" {
		return first
	}

	return second
}

func prune(candidate held, cutoff time.Time) (bool, error) {
	last, known := candidate.store.LastUse()
	if !known || !last.Before(cutoff) {
		return false, nil
	}

	release, err := candidate.store.TryHold()
	if errors.Is(err, vectorseg.ErrHeld) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	defer release()

	// A use between the first look and the lock refreshed the stamp.
	last, known = candidate.store.LastUse()
	if !known || !last.Before(cutoff) {
		return false, nil
	}

	parent, err := safefs.OpenDir(candidate.parent)
	if err != nil {
		return false, fmt.Errorf("opening %s: %w", candidate.parent, err)
	}

	defer parent.Close()

	err = parent.RemoveAll(candidate.name)
	if err != nil {
		return false, fmt.Errorf("deleting the vector store: %w", err)
	}

	return true, nil
}

// Warning is the line a failed run owes, once: empty where the last run
// succeeded or its failure was told already.
func Warning(roots home.Roots) string {
	line := ""

	_, _ = locked(roots, func(held *Status) bool {
		if held.Error == "" || held.Warned {
			return false
		}

		held.Warned = true
		line = fmt.Sprintf("cache prune failed at %s: %s; see %s", held.Finished, held.Error, statusPath(roots))

		return true
	})

	return line
}
