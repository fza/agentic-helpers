package test

import (
	"os"
	"path/filepath"
	"testing"
)

// Planting plants a link where a writer under test goes, aimed at a tree
// outside the writer's root.
type Planting struct {
	Name string
	// Folder says the link stands one folder up from the written file, so
	// every write below it is refused, a replacement included.
	Folder bool
	Plant  func(t *testing.T, target string, outside string)
}

// Plantings are the links a process able to write below a root can plant:
// a symlink at the file, a symlink at its folder, a hard link at the file.
func Plantings() []Planting {
	return []Planting{
		{
			Name: "a symlink at the file",
			Plant: func(t *testing.T, target string, outside string) {
				t.Helper()
				plantIn(t, target)
				link(t, os.Symlink(filepath.Join(outside, "keep.txt"), target))
			},
		},
		{
			Name:   "a symlink at the folder",
			Folder: true,
			Plant: func(t *testing.T, target string, outside string) {
				t.Helper()
				folder := filepath.Dir(target)
				link(t, os.RemoveAll(folder))
				plantIn(t, folder)
				link(t, os.Symlink(outside, folder))
			},
		},
		{
			Name: "a hard link at the file",
			Plant: func(t *testing.T, target string, outside string) {
				t.Helper()
				plantIn(t, target)
				link(t, os.Link(filepath.Join(outside, "keep.txt"), target))
			},
		},
	}
}

// Outside is a tree beside every root a test writes, holding one file a
// planted link aims at.
func Outside(t *testing.T) string {
	t.Helper()

	outside := filepath.Join(t.TempDir(), "outside")
	WriteFile(t, filepath.Join(outside, "keep.txt"), "untouched\n")

	return outside
}

// Untouched says the outside tree holds its one file as it was planted.
func Untouched(t *testing.T, outside string) bool {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(outside, "keep.txt"))
	listed, listErr := os.ReadDir(outside)

	return err == nil && listErr == nil && string(data) == "untouched\n" && len(listed) == 1
}

func plantIn(t *testing.T, target string) {
	t.Helper()

	_ = os.Remove(target)
	link(t, os.MkdirAll(filepath.Dir(target), 0o700))
}

func link(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("planting the link: %v", err)
	}
}
