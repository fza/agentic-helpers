package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/formatsteps"
	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const cutSession = "c3fb65eb-dbdf-47a8-9e04-e0477053b2b6"

// cutLibrary lays a format-1 library in a fresh root, its session file at a
// fixed time, since an import stamps a slot with the file's own.
func cutLibrary(t *testing.T) (home.Roots, string, string, *safefs.Dir) {
	t.Helper()

	root := t.TempDir()
	roots := home.Roots{Data: filepath.Join(root, "data"), Cache: filepath.Join(root, "cache")}
	at, cache := roots.Store("s1", home.Tools), roots.StoreCache("s1", home.Tools)

	test.FixtureTree(t, "tools/fdbox-library", at)
	test.WriteFile(t, filepath.Join(at, "sessions", cutSession+".json"), test.Fixture(t, "tools/legacy-sessions/session.json"))

	stamp := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)

	err := filepath.WalkDir(at, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		return os.Chtimes(path, stamp, stamp)
	})
	if err != nil {
		t.Fatal(err)
	}

	store, err := safefs.OpenDir(at)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(store.Close)

	return roots, at, cache, store
}

func cutTree(t *testing.T, roots home.Roots) string {
	t.Helper()

	return test.CanonicalTree(t, filepath.Dir(roots.Data), strings.NewReplacer(), func(string) bool { return false })
}

// Every cut of the library's step, followed by a new run, leaves what an
// uninterrupted run does.
func TestEveryLibraryOperationCarriesOn(t *testing.T) {
	ctx := context.Background()

	roots, at, cache, store := cutLibrary(t)

	err := prepareStore(ctx, store, roots, at, cache)
	if err != nil {
		t.Fatalf("the uninterrupted run should succeed: %v", err)
	}

	want := cutTree(t, roots)
	listed := journalSteps(store, roots, at, cache)

	for cut := range len(listed) + 1 {
		t.Run(fmt.Sprintf("cut after %d of %d operations", cut, len(listed)), func(t *testing.T) {
			roots, at, cache, store := cutLibrary(t)

			for _, operation := range journalSteps(store, roots, at, cache)[:cut] {
				err := operation.Apply(ctx)
				if err != nil {
					t.Fatalf("%s: %v", operation.Name, err)
				}
			}

			err := prepareStore(ctx, store, roots, at, cache)
			if err != nil {
				t.Fatalf("the run after the cut should carry the step on: %v", err)
			}

			if got := cutTree(t, roots); got != want {
				t.Errorf("a run cut and carried on should end as an uninterrupted run does, got:\n%s\nwant:\n%s", got, want)
			}

			version, err := formatsteps.Read(at)
			if err != nil || version != libraryFormat {
				t.Errorf("the library should stand at its format, got %d %v", version, err)
			}
		})
	}
}

// layoutLibrary lays a format-2 library whose sessions an earlier release
// kept in one fixed-width slot file, one of them past the session ceiling.
func layoutLibrary(t *testing.T) (home.Roots, string, string, *safefs.Dir, []slots.Slot) {
	t.Helper()

	ctx := context.Background()
	roots, at, cache, store := cutLibrary(t)

	component := libraryComponent(store, roots, at, cache)
	component.Steps = component.Steps[:1]

	err := formatsteps.Run(ctx, component)
	if err != nil {
		t.Fatalf("bringing the library to format 2: %v", err)
	}

	path := filepath.Join(cache, "sessions.slots")

	held, err := slots.Open(roots.Cache, path).All(ctx)
	if err != nil || len(held) == 0 {
		t.Fatalf("the library should hold a session, got %d %v", len(held), err)
	}

	err = os.Remove(path)
	if err != nil {
		t.Fatal(err)
	}

	fixed := slots.OpenFixed(roots.Cache, path, 128<<10)
	stamp := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	fixed.Now = func() time.Time { return stamp }

	oversized, err := json.Marshal(sessionState{Nudged: worstList(100, 200)})
	if err != nil || len(oversized) <= sessionCeiling {
		t.Fatalf("the session should pass its ceiling, %d bytes %v", len(oversized), err)
	}

	for _, slot := range append(held, slots.Slot{Key: "session/oversized", Value: oversized}) {
		err = fixed.Put(ctx, slot.Key, slot.Value)
		if err != nil {
			t.Fatal(err)
		}
	}

	kept, err := fixed.All(ctx)
	if err != nil {
		t.Fatal(err)
	}

	return roots, at, cache, store, kept
}

// Every cut of the library's layout step, followed by a new run, leaves what
// an uninterrupted run does: a slot directory keeping every session within
// its ceiling as it was and fitting the one past it.
func TestEveryLayoutOperationCarriesOn(t *testing.T) {
	ctx := context.Background()

	roots, at, cache, store, kept := layoutLibrary(t)

	err := prepareStore(ctx, store, roots, at, cache)
	if err != nil {
		t.Fatalf("the uninterrupted run should succeed: %v", err)
	}

	want := cutTree(t, roots)

	held, err := slots.Open(roots.Cache, filepath.Join(cache, "sessions.slots")).All(ctx)
	if err != nil || len(held) != len(kept) {
		t.Fatalf("the layout should keep every session, got %d %v", len(held), err)
	}

	byKey := map[string][]byte{}
	for _, slot := range held {
		byKey[slot.Key] = slot.Value
	}

	for _, slot := range kept {
		value, found := byKey[slot.Key]
		within := len(slot.Value) <= sessionCeiling

		if !found || within != bytes.Equal(value, slot.Value) || len(value) > sessionCeiling {
			t.Errorf("%s should stay as it was within the ceiling and be fitted past it", slot.Key)
		}
	}

	for cut := range 2 {
		t.Run(fmt.Sprintf("layout cut after %d operations", cut), func(t *testing.T) {
			roots, at, cache, store, _ := layoutLibrary(t)

			listed, err := libraryComponent(store, roots, at, cache).Steps[1].Ops(ctx)
			if err != nil {
				t.Fatal(err)
			}

			for _, operation := range listed[:cut] {
				err = operation.Apply(ctx)
				if err != nil {
					t.Fatalf("%s: %v", operation.Name, err)
				}
			}

			err = prepareStore(ctx, store, roots, at, cache)
			if err != nil {
				t.Fatalf("the run after the cut should carry the step on: %v", err)
			}

			if got := cutTree(t, roots); got != want {
				t.Errorf("a run cut and carried on should end as an uninterrupted run does, got:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}
