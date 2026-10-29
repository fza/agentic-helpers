// Package test holds the fakes and helpers the hook tests share.
package test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
)

const (
	Session      = "c3fb65eb-dbdf-47a8-9e04-e0477053b2b6"
	OtherSession = "a1b2c3d4-0000-4000-8000-000000000000"
)

type CommonDir struct {
	Dir string
	Err error
}

func (fake CommonDir) CommonDir(context.Context, string) (string, error) {
	return fake.Dir, fake.Err
}

type Captured struct {
	Out bytes.Buffer
	Err bytes.Buffer
}

func (captured *Captured) Streams(stdin string) hookio.Streams {
	return hookio.Streams{In: strings.NewReader(stdin), Out: &captured.Out, Err: &captured.Err}
}

func WriteFile(t *testing.T, path string, content string) {
	t.Helper()

	err := os.MkdirAll(filepath.Dir(path), 0o755)
	if err != nil {
		t.Fatalf("creating the parent of %s: %v", path, err)
	}

	err = os.WriteFile(path, []byte(content), 0o644)
	if err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func ReadFile(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	return string(content)
}

func Age(t *testing.T, path string, age time.Duration) {
	t.Helper()

	then := time.Now().Add(-age)

	err := os.Chtimes(path, then, then)
	if err != nil {
		t.Fatalf("aging %s: %v", path, err)
	}
}

func Exists(t *testing.T, path string) bool {
	t.Helper()

	_, err := os.Lstat(path)

	return err == nil
}

// Tree describes every file under root by path and content hash, so a test can
// tell whether anything under it changed.
func Tree(t *testing.T, root string) string {
	t.Helper()

	var lines []string

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		relative, _ := filepath.Rel(root, path)
		sum := sha256.Sum256(content)
		lines = append(lines, relative+" "+hex.EncodeToString(sum[:8]))

		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("walking %s: %v", root, err)
	}

	slices.Sort(lines)

	return strings.Join(lines, "\n")
}

// Snapshot describes every entry under root, folders and links too, by path,
// kind, permissions and content hash, so a test can tell whether the tree
// stayed byte-identical.
func Snapshot(t *testing.T, root string) string {
	t.Helper()

	var lines []string

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}

		relative, _ := filepath.Rel(root, path)
		line := relative + " " + info.Mode().String()

		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, linkErr := os.Readlink(path)
			if linkErr != nil {
				return linkErr
			}

			line += " -> " + target
		case info.Mode().IsRegular():
			content, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}

			sum := sha256.Sum256(content)
			line += " " + hex.EncodeToString(sum[:])
		}

		lines = append(lines, line)

		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("walking %s: %v", root, err)
	}

	slices.Sort(lines)

	return strings.Join(lines, "\n")
}
