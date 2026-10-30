package memorylog_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestAnAppendNeverWritesAtAPlantedLink(t *testing.T) {
	for _, name := range []string{memorylog.TailName, ".lock"} {
		for _, planting := range test.Plantings() {
			t.Run(name+", "+planting.Name, func(t *testing.T) {
				root := t.TempDir()
				dir := filepath.Join(root, "stores", "s1", "memory", "log")
				outside := test.Outside(t)
				planting.Plant(t, filepath.Join(dir, name), outside)

				// A tail the log can read, so the repair that runs first
				// passes it on to the append.
				target := filepath.Join(outside, "keep.txt")

				err := os.WriteFile(target, memorylog.JournalHeader(), 0o600)
				if err != nil {
					t.Fatalf("shaping the planted file: %v", err)
				}

				log, err := memorylog.Open(root, dir)
				if err == nil {
					err = log.Append(context.Background(), event(1, 0))
				}

				held, _ := os.ReadFile(target)
				listed, _ := os.ReadDir(outside)

				if !bytes.Equal(held, memorylog.JournalHeader()) || len(listed) != 1 {
					t.Error("an append should never write outside the home")
				}

				if err == nil {
					t.Error("an append through a planted link should be refused")
				}
			})
		}
	}
}
