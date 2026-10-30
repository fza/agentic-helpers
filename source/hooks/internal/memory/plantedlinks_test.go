package memory_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestAClaimNeverWritesAtAPlantedLink(t *testing.T) {
	targets := []struct {
		name string
		path func(store string) string
	}{
		{name: "the store's lock", path: func(store string) string { return filepath.Join(store, ".lock") }},
		{name: "the seat registry", path: func(store string) string { return filepath.Join(store, "seats.json") }},
		{name: "the seats lock", path: func(store string) string { return filepath.Join(store, "seats", ".lock") }},
		{name: "the log's tail", path: func(store string) string { return filepath.Join(store, "log", "tail.jnl") }},
	}

	for _, each := range targets {
		for _, planting := range test.Plantings() {
			t.Run(each.name+", "+planting.Name, func(t *testing.T) {
				h := newHarness(t)
				store := h.store()
				outside := test.Outside(t)

				if planting.Folder && filepath.Dir(each.path(store)) == store {
					t.Skip("a link at the store itself is the folder-one-up case of the seats and log rows")
				}

				planting.Plant(t, each.path(store), outside)

				h.run("", "claim", "main")
				h.run("", "new", "--kind", "finding", "--title", "Gate ignores MCP reads", "--body", "only Bash")

				if !test.Untouched(t, outside) {
					t.Error("memory should never write outside the home")
				}
			})
		}
	}
}

// A folder carrying a link, renamed into the store under a name the store
// has not used yet, is walked like any other on the owner's next write.
func TestAFolderRenamedIntoTheStoreIsRefusedOnTheNextClaim(t *testing.T) {
	h := newHarness(t)
	store := h.store()
	outside := test.Outside(t)

	planted := filepath.Join(t.TempDir(), "seats")

	err := os.MkdirAll(planted, 0o700)
	if err == nil {
		err = os.MkdirAll(store, 0o700)
	}

	if err == nil {
		err = os.Link(filepath.Join(outside, "keep.txt"), filepath.Join(planted, ".lock"))
	}

	if err == nil {
		err = os.Rename(planted, filepath.Join(store, "seats"))
	}

	if err != nil {
		t.Fatalf("planting the folder: %v", err)
	}

	got := h.run("", "claim", "main")
	if got.code == 0 || !test.Untouched(t, outside) {
		t.Errorf("a claim through a renamed-in folder holding a link should be refused, got %d %q", got.code, got.out)
	}
}
