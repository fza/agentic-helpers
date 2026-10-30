package migration

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestACopyAcrossVolumesKeepsEveryFileFolderAndLink(t *testing.T) {
	root := t.TempDir()
	from := filepath.Join(root, "from")
	to := filepath.Join(root, "to")

	test.FixtureTree(t, "migration/home/claude/agentic-tools", from)

	err := os.Symlink("index.json", filepath.Join(from, "github.com", "acme", "app", "linked"))
	if err != nil {
		t.Fatalf("linking: %v", err)
	}

	err = os.Chmod(filepath.Join(from, "github.com", "acme", "app", "index.json"), 0o640)
	if err != nil {
		t.Fatalf("changing a mode: %v", err)
	}

	err = copyChecked(filepath.Dir(to), from, to, func() error { return nil })
	if err != nil {
		t.Fatalf("copying: %v", err)
	}

	source, _ := describe(from)
	copied, _ := describe(to)

	if !bytes.Equal(source, copied) || test.Snapshot(t, from) != test.Snapshot(t, to) {
		t.Error("a copy should hold every file, folder and link of its source, with their modes")
	}
}

func TestTheDescriptionOfATreeTellsADifferenceApart(t *testing.T) {
	root := t.TempDir()
	test.WriteFile(t, filepath.Join(root, "a", "file"), "one")
	test.WriteFile(t, filepath.Join(root, "b", "file"), "two")

	first, _ := describe(filepath.Join(root, "a"))
	second, _ := describe(filepath.Join(root, "b"))

	if bytes.Equal(first, second) {
		t.Error("trees differing in one file's content should be described apart")
	}
}
