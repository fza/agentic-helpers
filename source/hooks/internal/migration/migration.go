// Package migration moves the layout earlier releases kept under `~/.claude`
// into the agent-neutral home (internal/home), once for the whole machine:
// every project's memory store, tool library and writers list, the session
// tree and the attachments. The first call of a release with the home runs it,
// holding the migration lock exclusively, while every other call takes the
// lock shared and so waits for it. `<data>/format.json` is written last: until
// it stands, a failure renames every moved tree back.
//
// Sessions still running an earlier release are fenced off from the old
// trees: each old memory store is left as a stub only a newer format reads,
// and the old tool library and writers lists become files naming the home.
// Once no process of an earlier release runs, the sweep removes the stubs,
// the fences and the old caches.
package migration

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/filelock"
	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/process"
	"github.com/fza/agentic-helpers/source/hooks/internal/project"
)

const (
	self = "agentic-helpers"
	// agent names the agent whose sessions earlier releases kept, under
	// `<data>/sessions/<agent>/` now.
	agent       = "claude"
	lockName    = ".migration.lock"
	failedDir   = "migration-failed"
	attachments = "attachments"
)

// Processes lists the executable of every process of this user whose name
// starts with prefix.
type Processes interface {
	Executables(ctx context.Context, prefix string) ([]string, error)
}

// Starter starts a process that outlives its caller.
type Starter interface {
	Start(ctx context.Context, path string, args ...string) error
}

// Env is everything the migration and the sweep read from outside. UserHome
// holds the old layout under `.claude`; Executable is the running binary,
// whose folder holds this release's binaries; MountRoots are the folders
// removable volumes are mounted below.
type Env struct {
	UserHome   string
	Roots      home.Roots
	Lookup     project.CommonDirLookup
	Git        home.Git
	Processes  Processes
	Starter    Starter
	Executable string
	SessionID  string
	Now        func() time.Time
	MountRoots []string

	// rename moves a tree, os.Rename unless a test makes it fail as across
	// volumes; halt stops a run at a checkpoint, as a kill would.
	rename func(string, string) error
	halt   func() bool
}

// Machine is the environment of the running process.
func Machine(userHome string, getenv func(string) string) Env {
	executable, err := os.Executable()
	if err != nil {
		executable = ""
	}

	return Env{
		UserHome:   userHome,
		Roots:      home.Locate(userHome, getenv),
		Lookup:     project.GitCommonDir{},
		Git:        process.Git{},
		Processes:  process.Table{},
		Starter:    detached{},
		Executable: executable,
		SessionID:  getenv("CLAUDE_CODE_SESSION_ID"),
		Now:        time.Now,
		MountRoots: mountRoots(runtime.GOOS),
	}
}

func mountRoots(goos string) []string {
	if goos == "darwin" {
		return []string{"/Volumes"}
	}

	return []string{"/media", "/run/media", "/mnt"}
}

func (env Env) now() time.Time {
	if env.Now == nil {
		return time.Now().UTC()
	}

	return env.Now().UTC()
}

func (env Env) claude() string { return filepath.Join(env.UserHome, ".claude") }

func (env Env) memoryRoot() string { return filepath.Join(env.claude(), "agentic-memory") }

func (env Env) toolsRoot() string { return filepath.Join(env.claude(), "agentic-tools") }

func (env Env) writersRoot() string { return filepath.Join(env.claude(), self) }

// usable says the environment names absolute places: an empty home would
// make every old path relative to the working directory.
func (env Env) usable() bool {
	return filepath.IsAbs(env.UserHome) && filepath.IsAbs(env.Roots.Data) && filepath.IsAbs(env.Roots.Cache)
}

// pending says a migration may be due: an old tree is still a folder, or a
// run left its journal.
func (env Env) pending() bool {
	return env.oldLayout() || exists(env.journalPath())
}

// settled says the home is done: `format.json` stands and no run left its
// journal.
func (env Env) settled() bool {
	return exists(env.Roots.FormatFile()) && !exists(env.journalPath())
}

// oldLayout says an old tree is still a folder: a store, a library or a
// writers list may wait in it.
func (env Env) oldLayout() bool {
	for _, root := range []string{env.memoryRoot(), env.toolsRoot(), env.writersRoot()} {
		info, err := os.Lstat(root)
		if err == nil && info.IsDir() {
			return true
		}
	}

	return false
}

func exists(path string) bool {
	_, err := os.Lstat(path)

	return err == nil
}

// Prepare migrates the old layout where that is due and says once per
// session when it fails. Every entry point calls it before it reads the home.
func Prepare(ctx context.Context, env Env, stderr io.Writer) {
	_, err := Run(ctx, env)
	if err != nil {
		env.reportOnce(stderr, err)
	}
}

// Run migrates the old layout where an old tree exists and the home has no
// `format.json`, answering whether this call did. A journal a killed run left
// is settled first: undone, or finished where `format.json` stands. A call
// finding nothing to do still waits for a migration another call is running.
func Run(ctx context.Context, env Env) (bool, error) {
	if !env.usable() || !env.pending() {
		return false, nil
	}

	lock, err := env.openLock()
	if err != nil {
		return false, err
	}

	defer func() { _ = lock.Close() }()

	err = filelock.Shared(lock)
	if err != nil {
		return false, fmt.Errorf("taking the migration lock: %w", err)
	}

	done := env.settled()

	err = filelock.Unlock(lock)
	if err != nil || done {
		return false, err
	}

	err = filelock.Exclusive(lock)
	if err != nil {
		return false, fmt.Errorf("taking the migration lock: %w", err)
	}

	defer func() { _ = filelock.Unlock(lock) }()

	if exists(env.journalPath()) {
		err = env.recoverJournal()
		if err != nil {
			return false, err
		}
	}

	if exists(env.Roots.FormatFile()) || !env.oldLayout() {
		return false, nil
	}

	err = env.migrate(ctx)
	if err != nil {
		return false, err
	}

	return true, nil
}

func (env Env) openLock() (*os.File, error) {
	err := os.MkdirAll(env.Roots.Data, 0o700)
	if err != nil {
		return nil, fmt.Errorf("creating %s: %w", env.Roots.Data, err)
	}

	file, err := os.OpenFile(filepath.Join(env.Roots.Data, lockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening the migration lock: %w", err)
	}

	return file, nil
}

// reportOnce prints the failure the first time a session meets it; every
// call goes on with the old layout, which the failure left working.
func (env Env) reportOnce(stderr io.Writer, cause error) {
	marker := filepath.Join(env.Roots.Cache, failedDir, cmp.Or(env.SessionID, "no-session"))

	_, err := os.Stat(marker)
	if err == nil {
		return
	}

	_, _ = fmt.Fprintf(stderr, "%s: migrating to %s failed: %v; nothing moved, and the next call tries again\n", self,
		env.Roots.Data, cause)

	err = os.MkdirAll(filepath.Dir(marker), 0o700)
	if err == nil {
		_ = os.WriteFile(marker, nil, 0o600)
	}
}

func isDir(path string) bool {
	info, err := os.Lstat(path)

	return err == nil && info.IsDir()
}

func notExist(err error) bool { return errors.Is(err, fs.ErrNotExist) }
