package migration_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/migration"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestEveryCallRemovesTheHomesExpiredLegacyBackups(t *testing.T) {
	m := newOldMachine(t)

	if stderr := m.prepare(); stderr != "" {
		t.Fatalf("the old home should migrate, got %q", stderr)
	}

	m.now = m.now.Add(10 * 24 * time.Hour)

	backups := filepath.Join(m.env.Roots.Data, "backup")
	expired := filepath.Join(backups, "legacy-"+m.now.Add(-home.BackupLifetime).Format(home.BackupStamp))
	young := filepath.Join(backups, "legacy-"+m.now.Add(-home.BackupLifetime+time.Hour).Format(home.BackupStamp))
	unstamped := filepath.Join(backups, "legacy-by-hand")
	loose := filepath.Join(backups, "legacy-"+m.now.Add(-60*24*time.Hour).Format(home.BackupStamp)+".txt")

	for _, folder := range []string{expired, young, unstamped} {
		test.WriteFile(t, filepath.Join(folder, "agentic-memory", "seats.json"), "{}\n")
	}

	test.WriteFile(t, loose, "a note\n")

	if stderr := m.prepare(); stderr != "" {
		t.Fatalf("a call on a migrated home should say nothing, got %q", stderr)
	}

	if test.Exists(t, expired) {
		t.Error("a legacy backup the lifetime old should be removed")
	}

	for _, kept := range []string{young, unstamped, loose} {
		if !test.Exists(t, kept) {
			t.Errorf("a younger backup, an unstamped folder and a loose file should stay, %s went", filepath.Base(kept))
		}
	}

	migrated, _ := filepath.Glob(filepath.Join(backups, "legacy-2026*Z"))
	if len(migrated) != 2 {
		t.Errorf("the migration's own backup, ten days old, should stay beside the younger one, got %v", migrated)
	}
}

func TestAHomeWithoutAbsoluteRootsExpiresNothing(t *testing.T) {
	working := t.TempDir()
	t.Chdir(working)

	old := filepath.Join(working, "data", "backup", "legacy-"+time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).Format(home.BackupStamp))
	test.WriteFile(t, filepath.Join(old, "seats.json"), "{}\n")

	migration.ExpireBackups(context.Background(), migration.Env{Roots: home.Roots{Data: "data", Cache: "cache"}, Now: time.Now})

	if !test.Exists(t, old) {
		t.Error("a home whose roots are not absolute should expire nothing below the working directory")
	}
}
