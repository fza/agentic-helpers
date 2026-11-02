package migration_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/migration"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestASettledHomeNeverOpensTheMigrationLock(t *testing.T) {
	m := newOldMachine(t)

	if stderr := m.prepare(); stderr != "" {
		t.Fatalf("the old home should migrate, got %q", stderr)
	}

	if !test.Exists(t, m.claude("agentic-memory")) {
		t.Fatal("the old memory tree should still stand for older releases until the sweep")
	}

	lock := filepath.Join(m.env.Roots.Data, ".migration.lock")

	err := os.Remove(lock)
	if err != nil {
		t.Fatalf("removing the lock: %v", err)
	}

	// A folder at the lock's path makes it impossible to open, as a sandbox
	// refusing writes to the data root does.
	err = os.Mkdir(lock, 0o700)
	if err != nil {
		t.Fatalf("placing a folder at the lock: %v", err)
	}

	m.env.SessionID = "another-session"

	if stderr := m.prepare(); stderr != "" {
		t.Errorf("a call on a settled home should never reach the lock, got %q", stderr)
	}

	if test.Exists(t, filepath.Join(m.env.Roots.Cache, "migration-failed")) {
		t.Error("a call on a settled home should leave no failure marker")
	}
}

func TestAFailureMarkerKeepsTheLineItSaid(t *testing.T) {
	m := newOldMachine(t)
	test.WriteFile(t, filepath.Join(m.env.Roots.Data, "backup"), "taken")

	said := m.prepare()
	if said == "" {
		t.Fatal("a migration that cannot write its backup should fail and say so")
	}

	marker := filepath.Join(m.env.Roots.Cache, "migration-failed", test.Session)
	if kept := test.ReadFile(t, marker); kept != said {
		t.Errorf("the marker should hold the line the failure printed, got %q", kept)
	}
}

func TestFailureMarkersExpireAfterAWeek(t *testing.T) {
	m := newOldMachine(t)

	if stderr := m.prepare(); stderr != "" {
		t.Fatalf("the old home should migrate, got %q", stderr)
	}

	markers := filepath.Join(m.env.Roots.Cache, "migration-failed")
	old := filepath.Join(markers, "old-session")
	young := filepath.Join(markers, "young-session")

	for path, age := range map[string]time.Duration{old: 7 * 24 * time.Hour, young: 6 * 24 * time.Hour} {
		test.WriteFile(t, path, "agentic-helpers: migrating to ... failed\n")

		err := os.Chtimes(path, m.now.Add(-age), m.now.Add(-age))
		if err != nil {
			t.Fatalf("aging %s: %v", filepath.Base(path), err)
		}
	}

	migration.ExpireMarkers(m.env)

	if test.Exists(t, old) || !test.Exists(t, young) {
		t.Error("a marker a week old should go and a younger one stay")
	}

	m.now = m.now.Add(24 * time.Hour)

	if stderr := m.prepare(); stderr != "" {
		t.Fatalf("a call on a migrated home should say nothing, got %q", stderr)
	}

	if test.Exists(t, markers) {
		t.Error("every call should expire the markers, and their folder once it is empty")
	}
}
