// Package project locates the checkout a hook works on.
package project

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

type CommonDirLookup interface {
	CommonDir(ctx context.Context, dir string) (string, error)
}

// Root is the main checkout, even for a session driven inside a worktree: the
// parent of the shared git directory. Outside a repository it is base itself.
func Root(ctx context.Context, base string, lookup CommonDirLookup) string {
	common, err := lookup.CommonDir(ctx, base)
	if err != nil || common == "" {
		return base
	}

	return filepath.Dir(common)
}

type GitCommonDir struct{}

func (GitCommonDir) CommonDir(ctx context.Context, dir string) (string, error) {
	command := exec.CommandContext(ctx, "git", "rev-parse", "--path-format=absolute", "--git-common-dir")
	command.Dir = dir

	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("asking git for the common directory of %s: %w", dir, err)
	}

	return strings.TrimSpace(string(output)), nil
}

// Remembered asks its lookup once per directory and answers every later ask
// from memory; a failed lookup is asked again. A process looks up the same
// checkout from many places, and each ask is a git spawn.
type Remembered struct {
	Lookup CommonDirLookup

	mutex  sync.Mutex
	common map[string]string
}

func (remembered *Remembered) CommonDir(ctx context.Context, dir string) (string, error) {
	remembered.mutex.Lock()
	known, found := remembered.common[dir]
	remembered.mutex.Unlock()

	if found {
		return known, nil
	}

	common, err := remembered.Lookup.CommonDir(ctx, dir)
	if err != nil {
		return "", err
	}

	remembered.mutex.Lock()
	defer remembered.mutex.Unlock()

	if remembered.common == nil {
		remembered.common = map[string]string{}
	}

	remembered.common[dir] = common

	return common, nil
}
