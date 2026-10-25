package test

import (
	"embed"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

//go:embed fixtures
var fixtures embed.FS

func Fixture(t *testing.T, name string) string {
	t.Helper()

	content, err := fixtures.ReadFile("fixtures/" + name)
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}

	return string(content)
}

// FixtureTree copies the fixture directory name, every file in it, to dest.
func FixtureTree(t *testing.T, name string, dest string) {
	t.Helper()

	root := "fixtures/" + name

	err := fs.WalkDir(fixtures, root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}

		content, err := fixtures.ReadFile(path)
		if err != nil {
			return err
		}

		WriteFile(t, filepath.Join(dest, filepath.FromSlash(strings.TrimPrefix(path, root+"/"))), string(content))

		return nil
	})
	if err != nil {
		t.Fatalf("copying fixture %s: %v", name, err)
	}
}
