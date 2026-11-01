package slots

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/formatsteps"
)

// adoptionRoot lays a component at format 1 holding two session files, and
// lists them.
func adoptionRoot(t *testing.T) (string, *File, func() []Legacy) {
	t.Helper()

	root := t.TempDir()
	at := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)

	var legacy []Legacy

	for _, name := range []string{"one", "two"} {
		path := filepath.Join(root, "component", name+".json")
		err := os.MkdirAll(filepath.Dir(path), 0o700)
		if err == nil {
			err = os.WriteFile(path, fmt.Appendf(nil, `{"name":%q}`, name), 0o600)
		}

		if err == nil {
			err = os.Chtimes(path, at, at)
		}

		if err != nil {
			t.Fatal(err)
		}

		legacy = append(legacy, Legacy{Path: path, Key: "session/" + name})
	}

	return root, Open(root, filepath.Join(root, "sessions.slots"), 4096), func() []Legacy {
		var left []Legacy

		for _, held := range legacy {
			_, err := os.Stat(held.Path)
			if err == nil {
				left = append(left, held)
			}
		}

		return left
	}
}

// Every cut of the adoption's step, followed by a new run, leaves what an
// uninterrupted run does: every file held once, none left, format.json written.
func TestAdoptionCarriesOnFromAnyOperation(t *testing.T) {
	ctx := context.Background()

	for cut := range 3 {
		t.Run(fmt.Sprintf("cut after %d operations", cut), func(t *testing.T) {
			root, file, legacy := adoptionRoot(t)
			dir := filepath.Join(root, "component")
			component := file.adoption(dir, 2, legacy)

			unlock, err := component.Lock(ctx)
			if err != nil {
				t.Fatal(err)
			}

			listed, err := component.Steps[0].Ops(ctx)
			if err != nil {
				t.Fatal(err)
			}

			for _, operation := range listed[:cut] {
				err = operation.Apply(ctx)
				if err != nil {
					t.Fatalf("%s: %v", operation.Name, err)
				}
			}

			unlock()

			err = file.Adopt(ctx, dir, 2, legacy)
			if err != nil {
				t.Fatalf("the run after the cut should carry the step on: %v", err)
			}

			held, err := file.All(ctx)
			if err != nil || len(held) != 2 {
				t.Fatalf("each file should be held in one slot, got %v %v", held, err)
			}

			for _, slot := range held {
				if slot.Generation != 1 {
					t.Errorf("%s should be written once, generation %d", slot.Key, slot.Generation)
				}
			}

			if left := legacy(); len(left) != 0 {
				t.Errorf("no session file should stay, got %v", left)
			}

			version, err := formatsteps.Read(dir)
			if err != nil || version != 2 {
				t.Errorf("the component should stand at the new format, got %d %v", version, err)
			}
		})
	}
}

func TestAdoptionRefusesANewerFormat(t *testing.T) {
	root, file, legacy := adoptionRoot(t)
	dir := filepath.Join(root, "component")
	format := filepath.Join(dir, formatsteps.FileName)

	err := os.WriteFile(format, []byte(`{"format":3}`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = file.Adopt(context.Background(), dir, 2, legacy)
	if !errors.Is(err, formatsteps.ErrNewer) {
		t.Errorf("a newer component should be refused, got %v", err)
	}

	if written, _ := os.ReadFile(format); string(written) != `{"format":3}` || len(legacy()) != 2 {
		t.Errorf("a refused adoption should change nothing, got %s", written)
	}
}
