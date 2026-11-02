package home

import (
	"os"
	"path/filepath"
	"testing"
)

func TestACheckoutMovedWithoutAVolumeIdentityReplacesThePathsItLeft(t *testing.T) {
	root := t.TempDir()
	kept := filepath.Join(root, "kept")

	err := os.MkdirAll(filepath.Join(kept, ".git"), 0o755)
	if err != nil {
		t.Fatalf("creating a checkout still on disk: %v", err)
	}

	gone := Admitted{Path: filepath.Join(root, "gone"), CommonDir: filepath.Join(root, "gone", ".git")}
	still := Admitted{Path: kept, CommonDir: filepath.Join(kept, ".git")}
	moved := Checkout{Root: filepath.Join(root, "moved"), CommonDir: filepath.Join(root, "moved", ".git"), Pointer: "store"}

	cases := []struct {
		name     string
		held     []Admitted
		pointer  string
		replaced bool
	}{
		{"every recorded path gone, the pointer naming the store", []Admitted{gone}, "store", true},
		{"a recorded path still on disk", []Admitted{gone, still}, "store", false},
		{"the pointer naming another store", []Admitted{gone}, "other", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			identity := &Identity{ID: "store", Checkouts: append([]Admitted(nil), tc.held...)}
			checkout := moved
			checkout.Pointer = tc.pointer

			held := &mapping{stores: map[string]*Identity{"store": identity}}
			if !held.admit(identity, checkout, false) {
				t.Fatal("admitting a checkout the store did not list should change the store")
			}

			last := identity.Checkouts[len(identity.Checkouts)-1]
			if last.Path != moved.Root {
				t.Errorf("the moved checkout should be admitted, got %+v", identity.Checkouts)
			}

			if replaced := len(identity.Checkouts) == 1; replaced != tc.replaced {
				t.Errorf("the paths left behind should be replaced only when all are gone and the pointer names the store, got %+v", identity.Checkouts)
			}
		})
	}
}
