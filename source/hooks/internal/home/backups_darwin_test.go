package home_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestAWriterMakingTheCacheRootMarksItForBackupsToSkip(t *testing.T) {
	roots := test.Roots(t.TempDir())
	roots.ExcludeCacheOnCreate()

	err := slots.Open(roots.Cache, filepath.Join(roots.Cache, "prompt-vectors", "sessions.slots")).
		Put(context.Background(), "session", []byte("held"))
	if err != nil {
		t.Fatalf("writing below the cache root: %v", err)
	}

	err = safefs.MkdirAll(roots.Data, filepath.Join(roots.Data, "stores"), 0o700)
	if err != nil {
		t.Fatalf("writing below the data root: %v", err)
	}

	if !test.BackupExcluded(t, roots.Cache) {
		t.Error("the writer making the cache root should mark it for backups to skip")
	}

	if test.BackupExcluded(t, roots.Data) {
		t.Error("the data root should stay in backups")
	}
}

func TestACacheRootAlreadyThereIsLeftToSessionStart(t *testing.T) {
	roots := test.Roots(t.TempDir())

	err := safefs.MkdirAll(roots.Cache, roots.Cache, 0o700)
	if err != nil {
		t.Fatalf("making the cache root: %v", err)
	}

	roots.ExcludeCacheOnCreate()

	err = safefs.MkdirAll(roots.Cache, filepath.Join(roots.Cache, "stores"), 0o700)
	if err != nil {
		t.Fatalf("writing below the cache root: %v", err)
	}

	if test.BackupExcluded(t, roots.Cache) {
		t.Error("a writer should mark only a cache root it makes, leaving one already there to SessionStart")
	}

	roots.ExcludeCache()

	if !test.BackupExcluded(t, roots.Cache) {
		t.Error("SessionStart should mark a cache root that exists without the mark")
	}
}

func TestACacheRootMarkedAlreadyIsLeftUnwritten(t *testing.T) {
	roots := test.Roots(t.TempDir())

	err := os.MkdirAll(roots.Cache, 0o700)
	if err != nil {
		t.Fatalf("making the cache root: %v", err)
	}

	changed := func() unix.Timespec {
		var status unix.Stat_t

		err := unix.Stat(roots.Cache, &status)
		if err != nil {
			t.Fatalf("reading the cache root's status: %v", err)
		}

		return status.Ctim
	}

	roots.ExcludeCache()
	marked := changed()

	time.Sleep(10 * time.Millisecond)
	roots.ExcludeCache()

	if changed() != marked {
		t.Error("a SessionStart finding the mark should write nothing")
	}
}
