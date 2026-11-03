package slots_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
)

// A migration taking in legacy files may be killed before it removes them,
// and the next run takes them in again.
func TestImportTakesInAFileItHoldsOnce(t *testing.T) {
	for _, tc := range []struct {
		name       string
		again      string
		value      string
		generation uint64
	}{
		{name: "the same file again", again: `{"rung":75}`, value: `{"rung":75}`, generation: 1},
		{name: "a file changed since", again: `{"rung":90}`, value: `{"rung":90}`, generation: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			legacy := filepath.Join(root, "ladder", "session.json")
			at := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)

			write := func(content string) {
				t.Helper()

				err := os.MkdirAll(filepath.Dir(legacy), 0o700)
				if err == nil {
					err = os.WriteFile(legacy, []byte(content), 0o600)
				}

				if err == nil {
					err = os.Chtimes(legacy, at, at)
				}

				if err != nil {
					t.Fatal(err)
				}
			}

			file := slots.Open(root, filepath.Join(root, "sessions.slots"))

			write(`{"rung":75}`)

			err := file.Import(ctx, []slots.Legacy{{Path: legacy, Key: "ladder/session"}})
			if err != nil {
				t.Fatal(err)
			}

			write(tc.again)

			err = file.Import(ctx, []slots.Legacy{{Path: legacy, Key: "ladder/session"}})
			if err != nil {
				t.Fatal(err)
			}

			held, err := file.All(ctx)
			if err != nil || len(held) != 1 {
				t.Fatalf("one slot should hold the file, got %v %v", held, err)
			}

			if string(held[0].Value) != tc.value || held[0].Generation != tc.generation {
				t.Errorf("a file already held should not be written twice, and a changed one should, got %s at generation %d",
					held[0].Value, held[0].Generation)
			}
		})
	}
}
