package home_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestStoreDirsListsClaimedAndUnclaimedStoresByID(t *testing.T) {
	roots := test.Roots(t.TempDir())
	claimed := "01a17249-ce50-7449-89c0-3bb864c20079-b"
	unclaimed := "01a17249-ce50-7449-89c0-3bb864c20079-a"
	test.WriteFile(t, filepath.Join(roots.Stores(), claimed, "identity.json"), "{}")
	test.WriteFile(t, filepath.Join(roots.Data, "unclaimed", unclaimed, "identity.json"), "{}")
	test.WriteFile(t, filepath.Join(roots.Stores(), "not-a-store", "identity.json"), "{}")

	dirs, err := roots.StoreDirs()
	if err != nil || len(dirs) != 2 {
		t.Fatalf("every folder named as a store id should be listed, and nothing else: %+v %v", dirs, err)
	}

	if dirs[0].ID != unclaimed || !dirs[0].Unclaimed || dirs[1].ID != claimed || dirs[1].Unclaimed {
		t.Errorf("stores should come in ascending id order, each marked where it waits: %+v", dirs)
	}
}

func TestStoreDirsRefusesAStoreKeptInBothPlaces(t *testing.T) {
	roots := test.Roots(t.TempDir())
	id := "01a17249-ce50-7449-89c0-3bb864c20079-b"
	test.WriteFile(t, filepath.Join(roots.Stores(), id, "identity.json"), "{}")
	test.WriteFile(t, filepath.Join(roots.Data, "unclaimed", id, "identity.json"), "{}")

	_, err := roots.StoreDirs()
	if err == nil || !strings.Contains(err.Error(), "both claimed and unclaimed") {
		t.Errorf("a store in both places should be refused: %v", err)
	}
}

func TestReadIdentityFileKeepsTheBytesAsStored(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "01a17249-ce50-7449-89c0-3bb864c20079-b")
	stored := `{"id": "01a17249-ce50-7449-89c0-3bb864c20079-b", "created": "2026-03-02T09:14:55Z", "checkouts": [], "later": 1}` + "\n"
	test.WriteFile(t, filepath.Join(dir, "identity.json"), stored)

	identity, raw, err := home.ReadIdentityFile(dir)
	if err != nil || identity.ID != filepath.Base(dir) || string(raw) != stored {
		t.Errorf("the identity should be read, its bytes kept as stored: %+v %q %v", identity, raw, err)
	}

	test.WriteFile(t, filepath.Join(dir, "identity.json"), `{"id": "01a17249-ce50-7449-89c0-3bb864c20079-c"}`)

	_, _, err = home.ReadIdentityFile(dir)
	if err == nil {
		t.Errorf("an identity naming another store should be refused")
	}
}
