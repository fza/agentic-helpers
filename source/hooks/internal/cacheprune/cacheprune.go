// Package cacheprune deletes the vector stores no binary has used within the
// retention period, after sdd's automatic cache pruning: an embedding-model
// change starts a new store per fingerprint, and the old one would otherwise
// stay for good. The same run deletes the release folders of the plugin's
// data directory no session may still run, which the launcher would
// otherwise keep for every release. A run starts at most once a day, detached, so no hook, call
// or command waits for it; each run records its outcome, and a failed one is
// told once.
package cacheprune

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	lockName    = "cache-prune.lock"
	runLockName = "cache-prune.run.lock"
	// retryDenied is how soon a run some store was denied to is claimed
	// again, so a sandboxed session's claim never uses up the day.
	retryDenied = time.Hour
	// RunLimit bounds a whole run, so a detached run never outlives a
	// stalled filesystem.
	RunLimit = 5 * time.Minute
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
	// Denied counts the stores the run was not allowed to touch; a run with
	// any is claimed again an hour later.
	Denied int `json:"denied,omitempty"`
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

	data, err := io.ReadAll(io.LimitReader(file, statusLimit))
	if err != nil {
		return Status{}
	}

	_ = json.Unmarshal(data, &held)

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

// ErrRunning is a run another process is making.
var ErrRunning = errors.New("a cache prune is running")

// runLock takes the machine-wide run lock without waiting.
func runLock(roots home.Roots) (func(), error) {
	err := safefs.MkdirAll(roots.Data, roots.Data, 0o700)
	if err != nil {
		return nil, fmt.Errorf("creating the data root: %w", err)
	}

	file, err := safefs.Lock(roots.Data, filepath.Join(roots.Data, runLockName), 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening the cache prune run lock: %w", err)
	}

	taken, err := filelock.TryExclusive(file)
	if err != nil || !taken {
		_ = file.Close()

		if err != nil {
			return nil, fmt.Errorf("locking the cache prune run: %w", err)
		}

		return nil, ErrRunning
	}

	return func() {
		_ = filelock.Unlock(file)
		_ = file.Close()
	}, nil
}

// Claim takes the day's run: where no run started within the last day, it
// stamps now as the start and answers true, so of every process asking the
// first alone starts one.
// A caller pays one read of the status for it: the lock is taken only on the
// day a run is due.
func Claim(roots home.Roots, now time.Time) (bool, error) {
	due := func(held Status) bool {
		started, err := time.Parse(time.RFC3339, held.Started)

		return err != nil || now.Sub(started) >= runEvery || started.After(now)
	}

	if !due(readStatus(roots)) {
		return false, nil
	}

	return locked(roots, func(held *Status) bool {
		if !due(*held) {
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
	root   string
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
		found = append(found, held{store: vectorseg.Open(roots.Cache, filepath.Join(tools, name)), root: roots.Cache,
			parent: tools, name: name})
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

			found = append(found, held{store: vectorseg.Open(roots.Cache, segments), root: roots.Cache, parent: memory,
				name: name})
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
// now, skipping a store another process holds, and every release folder no
// session may still run (pruneReleases), then records the run. A store goes
// in a lock session of its own, so a write never waits behind the run.
func Run(ctx context.Context, roots home.Roots, retention time.Duration, releases Releases, now time.Time) Status {
	result := Status{Started: now.UTC().Format(time.RFC3339)}

	// One run at a time on the machine: a run started by hand beside the
	// daily one, or a second process claiming across a clock change, exits
	// at once.
	release, err := runLock(roots)
	if errors.Is(err, ErrRunning) {
		return result
	}

	if errors.Is(err, fs.ErrPermission) {
		result.Denied++

		return result
	}

	if err != nil {
		result.Error = err.Error()

		return result
	}

	defer release()

	var errs []error

	err = tombs(roots)

	switch {
	case errors.Is(err, fs.ErrPermission):
		result.Denied++
	case err != nil:
		errs = append(errs, err)
	}

	for _, candidate := range stores(roots) {
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())

			break
		}

		deleted, err := prune(candidate, now.Add(-retention))

		switch {
		// A run inside a sandbox, such as a board job's, may not touch the
		// cache: that store is skipped, not failed, and the day's run is
		// retried from a session that may.
		case errors.Is(err, fs.ErrPermission):
			result.Denied++
		case err != nil:
			errs = append(errs, err)
		}

		if deleted {
			result.Deleted = append(result.Deleted, filepath.Join(candidate.parent, candidate.name))
		}
	}

	deleted, err := pruneReleases(ctx, releases, now)
	result.Deleted = append(result.Deleted, deleted...)

	switch {
	case errors.Is(err, fs.ErrPermission):
		result.Denied++
	case err != nil:
		errs = append(errs, err)
	}

	result.Finished = now.UTC().Format(time.RFC3339)

	joined := errors.Join(errs...)
	if joined != nil {
		result.Error = joined.Error()
	}

	_, err = locked(roots, func(stored *Status) bool {
		started := stored.Started
		*stored = result
		stored.Started = cmpOr(started, result.Started)

		if result.Denied > 0 {
			stored.Started = now.Add(retryDenied - runEvery).UTC().Format(time.RFC3339)
		}

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

// tombPrefix names a store moved aside for removal; a run removes every
// folder carrying it, so one a crash left goes with the next run.
const tombPrefix = ".pruned-"

// prune judges the store's last use under its hold only, so a write that
// stamped it before the hold keeps it; a look outside the hold as well would
// only hide this one from every test. Under the hold the store is only moved
// aside, and it is removed once the hold is released, so the lock is held
// for one rename however large the store, as sdd does.
func prune(candidate held, cutoff time.Time) (bool, error) {
	release, err := candidate.store.TryHold()
	if errors.Is(err, vectorseg.ErrHeld) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	last, known := candidate.store.LastUse()
	if !known || !last.Before(cutoff) {
		release()

		return false, nil
	}

	tomb := tombPrefix + candidate.name

	err = safefs.Rename(candidate.root, filepath.Join(candidate.parent, candidate.name), filepath.Join(candidate.parent, tomb))

	release()

	if err != nil {
		return false, fmt.Errorf("moving the vector store aside: %w", err)
	}

	return true, removeTomb(candidate.parent, tomb)
}

func removeTomb(parentPath string, tomb string) error {
	parent, err := safefs.OpenDir(parentPath)
	if err != nil {
		return fmt.Errorf("opening %s: %w", parentPath, err)
	}

	defer parent.Close()

	err = parent.RemoveAll(tomb)
	if err != nil {
		return fmt.Errorf("deleting the vector store: %w", err)
	}

	return nil
}

// tombs removes every store a run moved aside and did not remove.
func tombs(roots home.Roots) error {
	parents := []string{filepath.Join(roots.SharedCache(home.Tools), "vectors")}
	for _, id := range folders(filepath.Join(roots.Cache, "stores")) {
		parents = append(parents, roots.StoreCache(id, home.Memory))
	}

	var errs []error

	for _, parent := range parents {
		listed, _ := os.ReadDir(parent)

		for _, entry := range listed {
			if entry.IsDir() && strings.HasPrefix(entry.Name(), tombPrefix) {
				errs = append(errs, removeTomb(parent, entry.Name()))
			}
		}
	}

	return errors.Join(errs...)
}

// Warning is the line a failed run owes, once: empty where the last run
// succeeded or its failure was told already.
// The status is read without the lock first, so a SessionStart after a
// clean run pays one read.
func Warning(roots home.Roots) string {
	line := ""

	unread := readStatus(roots)
	if unread.Error == "" || unread.Warned {
		return line
	}

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
