package test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// GitDirOf answers a checkout's git common directory as `<checkout>/.git`,
// walking up from the asked path, so several checkouts in one test each
// answer their own; a worktree names its main checkout's in Worktrees.
type GitDirOf struct {
	Worktrees map[string]string
}

func (fake GitDirOf) CommonDir(_ context.Context, dir string) (string, error) {
	for at := filepath.Clean(dir); ; at = filepath.Dir(at) {
		main, isWorktree := fake.Worktrees[at]
		if isWorktree {
			return filepath.Join(main, ".git"), nil
		}

		info, err := os.Stat(filepath.Join(at, ".git"))
		if err == nil && info.IsDir() {
			return filepath.Join(at, ".git"), nil
		}

		if filepath.Dir(at) == at {
			return "", errors.New("fatal: not a git repository")
		}
	}
}
