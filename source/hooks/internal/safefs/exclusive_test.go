package safefs_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestAnExclusiveRenameNeverReplaces(t *testing.T) {
	cases := []struct {
		name   string
		source func(t *testing.T, at string)
		target func(t *testing.T, at string)
	}{
		{
			name:   "a file onto a file",
			source: func(t *testing.T, at string) { t.Helper(); test.WriteFile(t, at, "moving") },
			target: func(t *testing.T, at string) { t.Helper(); test.WriteFile(t, at, "there") },
		},
		{
			name:   "a file onto a folder",
			source: func(t *testing.T, at string) { t.Helper(); test.WriteFile(t, at, "moving") },
			target: func(t *testing.T, at string) { t.Helper(); test.WriteFile(t, filepath.Join(at, "inside"), "there") },
		},
		{
			name:   "a folder onto a folder",
			source: func(t *testing.T, at string) { t.Helper(); test.WriteFile(t, filepath.Join(at, "inside"), "moving") },
			target: func(t *testing.T, at string) { t.Helper(); test.WriteFile(t, filepath.Join(at, "inside"), "there") },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			source, target := filepath.Join(root, "from", "entry"), filepath.Join(root, "to", "entry")
			tc.source(t, source)
			tc.target(t, target)
			before := test.Tree(t, root)

			err := safefs.RenameExclusive(root, source, target)

			if !errors.Is(err, fs.ErrExist) {
				t.Errorf("a move onto a name already there should be refused as existing, got %v", err)
			}

			if test.Tree(t, root) != before {
				t.Error("both entries should stay as they were")
			}
		})
	}
}

func TestAnExclusiveRenameMovesOntoAFreeName(t *testing.T) {
	root := t.TempDir()
	source, target := filepath.Join(root, "from", "notes.md"), filepath.Join(root, "to", "notes.md")
	test.WriteFile(t, source, "notes\n")
	test.WriteFile(t, filepath.Join(root, "to", ".keep"), "")

	err := safefs.RenameExclusive(root, source, target)

	if err != nil || test.Exists(t, source) || test.ReadFile(t, target) != "notes\n" {
		t.Errorf("the file should move to the free name and leave the old one, got %v", err)
	}
}

func TestAnExclusiveRenameRefusesAHardLinkedFile(t *testing.T) {
	root := t.TempDir()
	outside := test.Outside(t)
	source, target := filepath.Join(root, "from", "entry"), filepath.Join(root, "to", "entry")
	test.WriteFile(t, filepath.Join(root, "to", ".keep"), "")

	err := os.MkdirAll(filepath.Dir(source), 0o700)
	if err != nil {
		t.Fatalf("making the folder: %v", err)
	}

	err = os.Link(filepath.Join(outside, "keep.txt"), source)
	if err != nil {
		t.Fatalf("planting the hard link: %v", err)
	}

	err = safefs.RenameExclusive(root, source, target)

	if !errors.Is(err, safefs.ErrHardLinked) || test.Exists(t, target) || !test.Exists(t, source) {
		t.Errorf("a file with a second name should stay where it is, got %v", err)
	}
}
