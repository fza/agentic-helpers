package test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// OldHome lays out a home as the releases before the agent-neutral home left
// it, below a fresh folder it answers: `home/.claude` from the fixture
// migration/home, old caches below `cache`, the checkouts `checkouts/app`
// (repo_id github.com/acme/app) and `checkouts/plain`, and the mount root
// `Volumes` with nothing mounted.
func OldHome(t *testing.T) string {
	t.Helper()

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolving the test folder: %v", err)
	}

	FixtureTreeReplacing(t, "migration/home/claude", filepath.Join(root, "home", ".claude"),
		strings.NewReplacer("@ROOT@", root))
	FixtureTree(t, "migration/cache", filepath.Join(root, "cache"))

	WriteFile(t, filepath.Join(root, "checkouts", "app", ".git", "HEAD"), "ref: refs/heads/main\n")
	WriteFile(t, filepath.Join(root, "checkouts", "app", ".sdd", "config.yaml"), "repo_id: github.com/acme/app\n")
	WriteFile(t, filepath.Join(root, "checkouts", "plain", ".git", "HEAD"), "ref: refs/heads/main\n")

	err = os.MkdirAll(filepath.Join(root, "Volumes"), 0o755)
	if err != nil {
		t.Fatalf("creating the mount root: %v", err)
	}

	return root
}

// Executables is a process table naming each process by its executable:
// Paths are the agentic-* processes running, Err a table that cannot be read.
type Executables struct {
	Paths []string
	Err   error
}

func (fake Executables) Executables(context.Context, string) ([]string, error) {
	return fake.Paths, fake.Err
}

// Starts records every process a test would have started, starting none.
type Starts struct {
	mutex   sync.Mutex
	started [][]string
}

func (fake *Starts) Start(_ context.Context, path string, args ...string) error {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()

	fake.started = append(fake.started, append([]string{path}, args...))

	return nil
}

func (fake *Starts) Started() [][]string {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()

	return fake.started
}
