package seat_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestASeatWriteNeverLandsAtAPlantedLink(t *testing.T) {
	writes := []struct {
		name   string
		target func(store string) string
		write  func(root string, store string) error
		// replaces says a link at the file's own name is renamed over.
		replaces bool
	}{
		{
			name:     "a claim",
			replaces: true,
			target:   func(store string) string { return seat.Path(seat.Dir(store), "main") },
			write: func(root string, store string) error {
				return seat.Write(root, seat.Dir(store), "main", seat.Claim{SessionID: test.Session})
			},
		},
		{
			name:   "the seats lock",
			target: func(store string) string { return filepath.Join(seat.Dir(store), ".lock") },
			write: func(root string, store string) error {
				unlock, err := seat.Lock(context.Background(), root, seat.Dir(store))
				if err == nil {
					unlock()
				}

				return err
			},
		},
		{
			name:     "the seat registry",
			replaces: true,
			target:   seat.RegistryPath,
			write: func(root string, store string) error {
				registry, _, err := seat.LoadRegistry(store)
				if err != nil {
					return err
				}

				return registry.Save(root)
			},
		},
		{
			name:     "the shelves",
			replaces: true,
			target:   seat.ShelvesPath,
			write: func(root string, store string) error {
				shelves, _, err := seat.LoadShelves(store)
				if err != nil {
					return err
				}

				return shelves.Save(root)
			},
		},
	}

	for _, each := range writes {
		for _, planting := range test.Plantings() {
			t.Run(each.name+", "+planting.Name, func(t *testing.T) {
				root := t.TempDir()
				store := filepath.Join(root, "stores", "s1", "memory")
				outside := test.Outside(t)
				planting.Plant(t, each.target(store), outside)

				err := each.write(root, store)

				if !test.Untouched(t, outside) {
					t.Error("a seat write should never land outside the home")
				}

				if err == nil && (planting.Folder || !each.replaces) {
					t.Error("a seat write through a planted link should be refused")
				}
			})
		}
	}
}

// A folder carrying a link, renamed into the store under a name the store
// has not used yet, is walked like any other on the owner's next write.
func TestAFolderRenamedIntoTheStoreIsRefusedOnTheNextWrite(t *testing.T) {
	root := t.TempDir()
	store := filepath.Join(root, "stores", "s1", "memory")
	outside := test.Outside(t)

	planted := filepath.Join(t.TempDir(), "seats")
	test.WriteFile(t, filepath.Join(store, "format.json"), `{"format": 3}`)

	err := os.MkdirAll(planted, 0o700)
	if err == nil {
		err = os.Link(filepath.Join(outside, "keep.txt"), filepath.Join(planted, ".lock"))
	}

	if err == nil {
		err = os.Rename(planted, seat.Dir(store))
	}

	if err != nil {
		t.Fatalf("planting the folder: %v", err)
	}

	unlock, err := seat.Lock(context.Background(), root, seat.Dir(store))
	if err == nil {
		unlock()
		t.Error("a lock file with a second name should be refused")
	}

	if !test.Untouched(t, outside) {
		t.Error("no write should reach the file the planted folder links to")
	}
}
