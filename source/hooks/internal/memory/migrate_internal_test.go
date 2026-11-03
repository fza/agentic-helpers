package memory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func migrateMemory(t *testing.T) *memory {
	t.Helper()

	root := t.TempDir()

	return &memory{
		env: Env{
			Roots: home.Roots{Data: filepath.Join(root, "data"), Cache: filepath.Join(root, "cache")},
			Now:   func() time.Time { return time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC) },
		},
		key: "s1",
	}
}

// A newer release can move the store between prepare's read and the store
// lock: migrate reads the format again under the lock and refuses it.
func TestAStoreMovedPastTheNewestFormatIsNeverWrittenBack(t *testing.T) {
	mem := migrateMemory(t)

	formatPath := filepath.Join(mem.store(), "format.json")
	test.WriteFile(t, formatPath, `{"format":11}`+"\n")

	err := mem.migrate(context.Background())
	if !errors.Is(err, ErrNewerStore) {
		t.Errorf("a store of a newer format met under the lock should be refused, got %v", err)
	}

	written, _ := os.ReadFile(formatPath)
	if string(written) != `{"format":11}`+"\n" {
		t.Errorf("a store of a newer format should never be written back, got %s", written)
	}
}

// Every call prepares the store: one already at the current format is read
// and left as it is, its size ledger and clock mark untouched.
func TestACurrentStoreIsLeftAsItIs(t *testing.T) {
	mem := migrateMemory(t)
	test.WriteFile(t, filepath.Join(mem.store(), "format.json"), `{"format":10}`+"\n")
	mem.clock.latest = mem.now().Add(time.Hour)

	before := test.Tree(t, mem.store())

	err := mem.migrate(context.Background())
	if err != nil {
		t.Fatalf("a current store should need no migration: %v", err)
	}

	if after := test.Tree(t, mem.store()); after != before {
		t.Errorf("a store at the current format should be left as it is, got:\n%s\nbefore:\n%s", after, before)
	}
}
