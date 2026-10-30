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

	if err := registry.Save(store); err != nil {
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

func TestShelvesMintRenameAndSave(t *testing.T) {
	store := t.TempDir()

	shelves, found, err := seat.LoadShelves(store)
	if err != nil || found {
		t.Fatalf("a store without shelves should load none, not found, got %v %v", found, err)
	}

	id, err := shelves.Mint("row-7ah", bytes.NewReader([]byte{0, 1, 2, 3, 4, 35}))
	if err != nil || id != "h-abcde9" || !seat.IsShelfID(id) || seat.IsID(id) {
		t.Fatalf("a mint should draw h- and 6 letters or digits, got %q %v", id, err)
	}

	if _, err := shelves.Mint("row-7ah", test.Random()); !errors.Is(err, seat.ErrShelfNameTaken) {
		t.Errorf("a second shelf should not take a name in use, got %v", err)
	}

	other, _ := shelves.Mint("row-8bq", test.Random())
	if err := shelves.Rename(other, "row-7ah"); !errors.Is(err, seat.ErrShelfNameTaken) {
		t.Errorf("a rename should not take another shelf's name, got %v", err)
	}

	if err := shelves.Save(store); err != nil {
		t.Fatal(err)
	}

	loaded, found, err := seat.LoadShelves(store)
	if err != nil || !found {
		t.Fatalf("saved shelves should load: %v", err)
	}

	if got, _ := loaded.IDOf("row-7ah"); got != id || loaded.NameOf(id) != "row-7ah" || loaded.NameOf("h-zzzzzz") != "h-zzzzzz" {
		t.Error("loaded shelves should resolve names to ids and back, an unknown id to itself")
	}
}

func TestAnExpiredSeatFreesItsName(t *testing.T) {
	registry := seat.Registry{Seats: map[string]seat.Entry{
		"s-aaaaaa": {Name: "impl", Shelf: "h-aaaaaa", Expired: true},
		"s-bbbbbb": {Name: "rev", Shelf: "h-aaaaaa"},
	}}

	if _, found := registry.IDOf("impl"); found {
		t.Error("an expired seat's name should resolve to no seat")
	}

	if registry.NameOf("s-aaaaaa") != "impl (expired)" {
		t.Errorf("an expired seat should read as expired, got %q", registry.NameOf("s-aaaaaa"))
	}

	if _, err := registry.Mint("impl", test.Random()); err != nil {
		t.Errorf("a new seat should take an expired seat's name, got %v", err)
	}

	if got := registry.OnShelf("h-aaaaaa"); len(got) != 1 || got[0] != "s-bbbbbb" {
		t.Errorf("a shelf should hold only its live seats, got %v", got)
	}
}
