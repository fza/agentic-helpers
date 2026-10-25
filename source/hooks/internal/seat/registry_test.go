package seat_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestARegistryMintsRenamesAndSaves(t *testing.T) {
	store := t.TempDir()

	registry, found, err := seat.LoadRegistry(store)
	if err != nil || found {
		t.Fatalf("a store without a registry should load an empty one, not found, got %v %v", found, err)
	}

	// 252 and above are drawn again; the rest are taken modulo the alphabet.
	id, err := registry.Mint("main", bytes.NewReader([]byte{252, 0, 1, 2, 3, 4, 35}))
	if err != nil || id != "s-abcde9" || !seat.IsID(id) {
		t.Fatalf("a mint should draw s- and 6 unbiased letters or digits, got %q %v", id, err)
	}

	if _, err := registry.Mint("main", test.Random()); !errors.Is(err, seat.ErrNameTaken) {
		t.Errorf("a second seat should not take a name in use, got %v", err)
	}

	other, err := registry.Mint("drive", test.Random())
	if err != nil {
		t.Fatal(err)
	}

	if err := registry.Rename(other, "main"); !errors.Is(err, seat.ErrNameTaken) {
		t.Errorf("a rename should not take another seat's name, got %v", err)
	}

	if err := registry.Rename(id, "haupt"); err != nil {
		t.Fatalf("a rename to a free name should succeed: %v", err)
	}

	if err := registry.Save(); err != nil {
		t.Fatal(err)
	}

	loaded, found, err := seat.LoadRegistry(store)
	if err != nil || !found {
		t.Fatalf("a saved registry should load: %v", err)
	}

	if got, _ := loaded.IDOf("haupt"); got != id || loaded.NameOf(id) != "haupt" || loaded.NameOf("s-zzzzzz") != "s-zzzzzz" {
		t.Error("a loaded registry should resolve names to ids and back, an unknown id to itself")
	}

	if test.Exists(t, seat.RegistryPath(store)+".tmp") {
		t.Error("a save should leave no temporary file")
	}
}
