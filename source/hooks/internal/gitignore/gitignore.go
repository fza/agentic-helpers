// Package gitignore keeps a file a piece writes out of every repository by
// adding its pattern to git's global excludes file, the one git reads for
// every repository on the machine.
package gitignore

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type Git interface {
	Output(ctx context.Context, dir string, args ...string) (string, error)
}

// Global is the machine's global excludes file as git resolves it, for the
// user whose home and environment these are.
type Global struct {
	Git    Git
	Home   string
	Getenv func(string) string
}

// Ensure adds pattern to the global excludes file where git, run in dir, does
// not ignore path already by any rule. It returns the line a piece prints
// about the addition, empty where nothing was added.
func (global Global) Ensure(ctx context.Context, piece, dir, path, pattern string) (string, error) {
	_, err := global.Git.Output(ctx, dir, "check-ignore", "-q", path)
	if err == nil {
		return "", nil
	}

	file := global.file(ctx, dir)
	if file == "" {
		return "", nil
	}

	held, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("reading %s: %w", file, err)
	}

	scanner := bufio.NewScanner(bytes.NewReader(held))
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == pattern {
			return "", nil
		}
	}

	line := pattern + "\n"
	if len(held) > 0 && !bytes.HasSuffix(held, []byte("\n")) {
		line = "\n" + line
	}

	err = os.MkdirAll(filepath.Dir(file), 0o755)
	if err != nil {
		return "", fmt.Errorf("creating %s: %w", filepath.Dir(file), err)
	}

	opened, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("opening %s: %w", file, err)
	}

	_, err = opened.WriteString(line)
	closeErr := opened.Close()

	if err != nil {
		return "", fmt.Errorf("writing %s: %w", file, err)
	}

	if closeErr != nil {
		return "", fmt.Errorf("writing %s: %w", file, closeErr)
	}

	return fmt.Sprintf("%s: added %s to %s, so git ignores it in every repository", piece, pattern, file), nil
}

// file is `core.excludesFile` from the global git config, a leading `~/`
// expanded as git expands it, else git's default under the XDG config home.
// No home and no setting leaves no file to write.
func (global Global) file(ctx context.Context, dir string) string {
	set, err := global.Git.Output(ctx, dir, "config", "--global", "--get", "core.excludesFile")
	configured := strings.TrimSpace(set)

	if err == nil && configured != "" {
		rest, found := strings.CutPrefix(configured, "~/")
		if !found {
			return configured
		}

		if global.Home == "" {
			return ""
		}

		return filepath.Join(global.Home, rest)
	}

	xdg := ""
	if global.Getenv != nil {
		xdg = global.Getenv("XDG_CONFIG_HOME")
	}

	if xdg != "" {
		return filepath.Join(xdg, "git", "ignore")
	}

	if global.Home == "" {
		return ""
	}

	return filepath.Join(global.Home, ".config", "git", "ignore")
}
