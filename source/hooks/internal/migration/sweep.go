package migration

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/filelock"
	"github.com/fza/agentic-helpers/source/hooks/internal/mcpserver"
)

// processPrefix opens the name of every binary any release ships.
const processPrefix = "agentic-"

// oldCaches are the cache trees earlier releases kept beside the home's own
// cache root: only what can be rebuilt.
var oldCaches = []string{"agentic-memory", "agentic-tools"}

func (env Env) oldCacheTrees() []string {
	trees := make([]string, 0, len(oldCaches))
	for _, name := range oldCaches {
		trees = append(trees, filepath.Join(filepath.Dir(env.Roots.Cache), name))
	}

	return trees
}

// Owed says the migration is done and something it left for older releases
// still stands: a cache tree, a fence or the folder holding the stubs.
func Owed(env Env) bool {
	if !env.usable() || !exists(env.Roots.FormatFile()) {
		return false
	}

	return slices.ContainsFunc(env.oldCacheTrees(), isDir) || isFence(env.toolsRoot()) ||
		isFence(env.writersRoot()) || isDir(env.memoryRoot())
}

// olderRunning says a process of another release runs: an agentic-* binary
// outside the folder this one runs from. A table that cannot be read counts
// as one running.
func (env Env) olderRunning(ctx context.Context) bool {
	if env.Processes == nil || env.Executable == "" {
		return true
	}

	paths, err := env.Processes.Executables(ctx, processPrefix)
	if err != nil {
		return true
	}

	own := filepath.Dir(env.Executable)

	return slices.ContainsFunc(paths, func(path string) bool { return filepath.Dir(path) != own })
}

// Sweep removes the old caches, the fences and the stubs once no process of
// an older release runs, holding the migration lock exclusively; a lock held
// elsewhere leaves it to a later sweep. It answers whether it swept.
func Sweep(ctx context.Context, env Env) (bool, error) {
	if !Owed(env) || env.olderRunning(ctx) {
		return false, nil
	}

	lock, err := env.openLock()
	if err != nil {
		return false, err
	}

	defer func() { _ = lock.Close() }()

	held, err := filelock.TryExclusive(lock)
	if err != nil || !held {
		return false, err
	}

	defer func() { _ = filelock.Unlock(lock) }()

	var errs []error

	for _, tree := range env.oldCacheTrees() {
		if isDir(tree) {
			errs = append(errs, os.RemoveAll(tree))
		}
	}

	for _, fence := range []string{env.toolsRoot(), env.writersRoot()} {
		if isFence(fence) {
			errs = append(errs, os.Remove(fence))
		}
	}

	// An older session's hooks module keeps writing its context reading
	// below the old memory root while that root exists; nothing reads it.
	errs = append(errs, os.RemoveAll(filepath.Join(env.memoryRoot(), "sessions")), removeStubs(env.memoryRoot()))

	err = errors.Join(errs...)
	if err != nil {
		return false, fmt.Errorf("sweeping the old layout: %w", err)
	}

	return true, nil
}

// idleAfter is how long an MCP server's client stays quiet before the server
// sweeps: long enough that a session at work never waits on it.
const idleAfter = 2 * time.Minute

// Idle is the sweep as an MCP server's idle work, run silently.
func Idle(env Env) mcpserver.Idle {
	return mcpserver.Idle{After: idleAfter, Run: func(ctx context.Context) { _, _ = Sweep(ctx, env) }}
}

// removeStubs removes every stub below root, then every folder left empty,
// root too. Anything else stays where it is.
func removeStubs(root string) error {
	if !isDir(root) {
		return nil
	}

	var folders []string

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.IsDir() {
			return err
		}

		if isStub(path) {
			removeErr := os.RemoveAll(path)
			if removeErr != nil {
				return removeErr
			}

			return filepath.SkipDir
		}

		folders = append(folders, path)

		return nil
	})
	if err != nil {
		return err
	}

	for _, folder := range slices.Backward(folders) {
		listed, readErr := os.ReadDir(folder)
		if readErr == nil && len(listed) == 0 {
			_ = os.Remove(folder)
		}
	}

	return nil
}

// StartSweep starts the sweep in a process of its own, `agentic-helpers
// sweep` of this release, where one is owed, so the session ending never
// waits for it.
func StartSweep(ctx context.Context, env Env) error {
	if !Owed(env) || env.Starter == nil || env.Executable == "" {
		return nil
	}

	return env.Starter.Start(ctx, filepath.Join(filepath.Dir(env.Executable), self), "sweep")
}

// detached starts a process in a session of its own, its output discarded,
// and lets it run on once the caller exits.
type detached struct{}

func (detached) Start(_ context.Context, path string, args ...string) error {
	command := exec.Command(path, args...)
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	err := command.Start()
	if err != nil {
		return fmt.Errorf("starting %s: %w", path, err)
	}

	return command.Process.Release()
}
