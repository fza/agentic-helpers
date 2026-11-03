package migration_test

import (
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/migration"
	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestTheMachinesFirstCacheWriterMarksTheCacheRoot(t *testing.T) {
	userHome := t.TempDir()

	machine := migration.Machine(userHome, func(string) string { return "" })

	for _, root := range []string{machine.Roots.Cache, machine.Roots.Data} {
		err := safefs.MkdirAll(root, filepath.Join(root, "stores"), 0o700)
		if err != nil {
			t.Fatalf("writing below %s: %v", root, err)
		}
	}

	if !test.BackupExcluded(t, machine.Roots.Cache) {
		t.Error("every binary's writer making the cache root should mark it for backups to skip")
	}

	if test.BackupExcluded(t, machine.Roots.Data) {
		t.Error("the data root should stay in backups")
	}
}
