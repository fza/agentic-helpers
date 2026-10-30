package safefs_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

// writer is one way safefs writes target below root.
type writer struct {
	name  string
	write func(root string, target string) error
	// replaces says a link at the target's own name is replaced rather than
	// refused: the write renames over it.
	replaces bool
	// hardLinks says a target with a second name is written past, never
	// into: a rename moves a name, it opens no file.
	hardLinks bool
}

func closing(file *os.File, err error) error {
	if err == nil {
		_, err = file.WriteString("written")
		err = errors.Join(err, file.Close())
	}

	return err
}

func writers() []writer {
	return []writer{
		{name: "WriteFile", replaces: true, write: func(root, target string) error {
			return safefs.WriteFile(root, target, []byte("written"), 0o600)
		}},
		{name: "Create", write: func(root, target string) error {
			return safefs.Create(root, target, []byte("written"), 0o600)
		}},
		{name: "CreateFile", write: func(root, target string) error {
			return closing(safefs.CreateFile(root, target, 0o600))
		}},
		{name: "Append", write: func(root, target string) error {
			return closing(safefs.Append(root, target, 0o600))
		}},
		{name: "Lock", write: func(root, target string) error {
			return closing(safefs.Lock(root, target, 0o600))
		}},
		{name: "OpenChecked for writing", write: func(root, target string) error {
			return closing(safefs.OpenChecked(root, target, os.O_RDWR, 0o600))
		}},
		{name: "MkdirAll", write: func(root, target string) error {
			return safefs.MkdirAll(root, target, 0o700)
		}},
		{name: "Mkdir", write: func(root, target string) error {
			return safefs.Mkdir(root, target, 0o700)
		}},
		{name: "Symlink", write: func(root, target string) error {
			return safefs.Symlink(root, "elsewhere", target)
		}},
		{name: "Rename", replaces: true, hardLinks: true, write: func(root, target string) error {
			source := filepath.Join(root, "source")

			err := os.WriteFile(source, []byte("written"), 0o600)
			if err != nil {
				return err
			}

			return safefs.Rename(root, source, target)
		}},
	}
}

// decoy is a tree inside the root a planted link aims at: os.Root alone
// would follow a link to it, so only safefs's own checks keep it untouched.
func decoy(t *testing.T, root string) func() bool {
	t.Helper()

	dir := filepath.Join(root, "decoy")
	test.WriteFile(t, filepath.Join(dir, "keep.txt"), "untouched\n")

	before := test.Tree(t, dir)

	return func() bool { return test.Tree(t, dir) == before }
}

func TestAWriteNeverFollowsALinkPlantedInsideTheRoot(t *testing.T) {
	plantings := []struct {
		name   string
		folder bool
		hard   bool
		plant  func(t *testing.T, root string, target string)
	}{
		{name: "a link at the file", plant: func(t *testing.T, root string, target string) {
			t.Helper()

			// A sibling in the same folder: the folder's os.Root follows a
			// link that stays inside it, and refuses one leaving it.
			test.WriteFile(t, filepath.Join(filepath.Dir(target), "spare.txt"), "untouched\n")

			if os.Symlink("spare.txt", target) != nil {
				t.Fatal("the link should be planted")
			}
		}},
		{name: "a link at the folder", folder: true, plant: func(t *testing.T, root string, target string) {
			t.Helper()

			if os.Remove(filepath.Dir(target)) != nil || os.Symlink("decoy", filepath.Dir(target)) != nil {
				t.Fatal("the link should be planted")
			}
		}},
		{name: "a hard link at the file", hard: true, plant: func(t *testing.T, root string, target string) {
			t.Helper()

			if os.Link(filepath.Join(root, "decoy", "keep.txt"), target) != nil {
				t.Fatal("the link should be planted")
			}
		}},
	}

	for _, each := range writers() {
		for _, planting := range plantings {
			t.Run(each.name+", "+planting.name, func(t *testing.T) {
				root := t.TempDir()
				untouched := decoy(t, root)
				target := filepath.Join(root, "state", "target")

				err := os.Mkdir(filepath.Join(root, "state"), 0o700)
				if err != nil {
					t.Fatalf("making the folder: %v", err)
				}

				planting.plant(t, root, target)

				err = each.write(root, target)

				spare, _ := os.ReadFile(filepath.Join(root, "state", "spare.txt"))
				if !untouched() || len(spare) > 0 && string(spare) != "untouched\n" {
					t.Error("a write should never land where a planted link points")
				}

				replaced := !planting.folder && (each.replaces && !planting.hard || each.hardLinks && planting.hard)
				if err == nil && !replaced {
					t.Error("a write onto a planted link should be refused")
				}

				if replaced && err != nil {
					t.Errorf("a write that renames over a planted name should replace it, got %v", err)
				}
			})
		}
	}
}

func TestAWriteNeverLandsOutsideItsRoot(t *testing.T) {
	for _, each := range writers() {
		for _, planting := range test.Plantings() {
			t.Run(each.name+", "+planting.Name, func(t *testing.T) {
				root := t.TempDir()
				outside := test.Outside(t)
				target := filepath.Join(root, "state", "target")
				planting.Plant(t, target, outside)

				_ = each.write(root, target)

				if !test.Untouched(t, outside) {
					t.Error("a write should never land outside its root")
				}
			})
		}
	}
}

func TestAPathOutsideTheRootIsRefused(t *testing.T) {
	root := t.TempDir()
	outside := test.Outside(t)

	err := safefs.WriteFile(root, filepath.Join(root, "..", filepath.Base(outside), "keep.txt"), []byte("x"), 0o600)
	if !errors.Is(err, safefs.ErrOutside) || !test.Untouched(t, outside) {
		t.Errorf("a path outside the root should be refused, got %v", err)
	}
}

func TestARootThatIsALinkKeepsWorking(t *testing.T) {
	real := t.TempDir()
	share := filepath.Join(t.TempDir(), "share")

	err := os.Symlink(real, share)
	if err != nil {
		t.Fatalf("linking the root: %v", err)
	}

	root := filepath.Join(share, "agentic-helpers")

	err = safefs.MkdirAll(root, filepath.Join(root, "stores", "s1"), 0o700)
	if err == nil {
		err = safefs.WriteFile(root, filepath.Join(root, "stores", "s1", "identity.json"), []byte("{}"), 0o600)
	}

	if err != nil || test.ReadFile(t, filepath.Join(real, "agentic-helpers", "stores", "s1", "identity.json")) != "{}" {
		t.Errorf("a root below a link should be written through the link, got %v", err)
	}
}
