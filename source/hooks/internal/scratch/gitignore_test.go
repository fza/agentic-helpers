package scratch_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/scratch"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func startWith(t *testing.T, root string, git *test.Git, home string) string {
	t.Helper()

	captured := &test.Captured{}
	env := scratch.Env{Root: root, Git: git, Home: home, Getenv: func(string) string { return "" }}

	code := scratch.Main(context.Background(), []string{"start"}, env, captured.Streams(payload(test.Session)))
	if code != 0 {
		t.Fatalf("start should succeed, got %d: %s", code, captured.Err.String())
	}

	return captured.Out.String()
}

func TestStartAddsTmpToTheGlobalExcludes(t *testing.T) {
	root := project(t)
	home := t.TempDir()
	global := filepath.Join(home, ".gitignore_global")
	git := &test.Git{ExcludesFile: global}

	said := startWith(t, root, git, home)

	held, err := os.ReadFile(global)
	if err != nil || string(held) != ".tmp/\n" {
		t.Errorf("start should append .tmp/ to git's global excludes file, got %q, %v", held, err)
	}

	if strings.Count(said, "agentic-scratch: added .tmp/ to "+global) != 1 {
		t.Errorf("the addition should be said once, got: %s", said)
	}

	if again := startWith(t, root, git, home); strings.Contains(again, "added .tmp/") {
		t.Errorf("a second start should add and say nothing, got: %s", again)
	}
}

func TestStartAddsNothingGitIgnoresAlready(t *testing.T) {
	root := project(t)
	home := t.TempDir()

	said := startWith(t, root, &test.Git{Ignored: []string{".tmp"}}, home)

	if said != "" || test.Exists(t, filepath.Join(home, ".config", "git", "ignore")) {
		t.Errorf("a .tmp/ git ignores already should add nothing, got: %s", said)
	}
}

func TestAProjectWithoutTmpRunsNoGit(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()

	said := startWith(t, root, &test.Git{}, home)

	if said != "" || test.Exists(t, filepath.Join(home, ".config", "git", "ignore")) {
		t.Errorf("a project without .tmp/ should be left alone, got: %s", said)
	}
}
