package test

import (
	"os"
	"path/filepath"
	"testing"
)

// GuardTree / Project / Tmp / Home / Outside are the directories a shell-guard
// test judges against. Project carries sub/ and link, a symlink to Outside,
// and .tmp/ where scratch is set.
type GuardTree struct {
	Project string
	Tmp     string
	Home    string
	Outside string
}

func NewGuardTree(t *testing.T, scratch bool) GuardTree {
	t.Helper()

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolving the test root: %v", err)
	}

	tree := GuardTree{
		Project: filepath.Join(root, "home", "project"),
		Tmp:     filepath.Join(root, "tmp"),
		Home:    filepath.Join(root, "home"),
		Outside: filepath.Join(root, "outside"),
	}

	directories := []string{filepath.Join(tree.Project, "sub"), tree.Tmp, tree.Outside}
	if scratch {
		directories = append(directories, filepath.Join(tree.Project, ".tmp"))
	}

	for _, directory := range directories {
		err = os.MkdirAll(directory, 0o755)
		if err != nil {
			t.Fatalf("creating %s: %v", directory, err)
		}
	}

	err = os.Symlink(tree.Outside, filepath.Join(tree.Project, "link"))
	if err != nil {
		t.Fatalf("linking outside: %v", err)
	}

	return tree
}
