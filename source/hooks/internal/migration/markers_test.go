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

// placeMarkers writes a failure marker per session, each aged as given.
func placeMarkers(t *testing.T, m *oldMachine, ages map[string]time.Duration) string {
	t.Helper()

	markers := filepath.Join(m.env.Roots.Cache, "migration-failed")

	for session, age := range ages {
		path := filepath.Join(markers, session)
		test.WriteFile(t, path, "agentic-helpers: migrating to ... failed\n")

		err := os.Chtimes(path, m.now.Add(-age), m.now.Add(-age))
		if err != nil {
			t.Fatalf("aging %s: %v", session, err)
		}
	}

	return markers
}

func TestFailureMarkersOfAnUnsettledHomeExpireAfterAWeek(t *testing.T) {
	m := newOldMachine(t)
	test.WriteFile(t, filepath.Join(m.env.Roots.Data, "backup"), "taken")

	markers := placeMarkers(t, m, map[string]time.Duration{"old-session": 7 * 24 * time.Hour, "young-session": 6 * 24 * time.Hour})

	if m.prepare() == "" {
		t.Fatal("a migration that cannot write its backup should fail and say so")
	}

	// The marker the failure wrote carries the wall clock; the test runs on
	// its own.
	placeMarkers(t, m, map[string]time.Duration{test.Session: 0})

	for session, kept := range map[string]bool{"old-session": false, "young-session": true, test.Session: true} {
		if test.Exists(t, filepath.Join(markers, session)) != kept {
			t.Errorf("a marker a week old should go and a younger one stay, %s kept: %v", session, !kept)
		}
	}

	m.now = m.now.Add(8 * 24 * time.Hour)
	m.env.SessionID = "later-session"
	m.prepare()

	if names, _ := os.ReadDir(markers); len(names) != 1 || names[0].Name() != "later-session" {
		t.Errorf("every call should expire the markers a week old, got %v", names)
	}
}

func TestASettledHomeDropsEveryFailureMarker(t *testing.T) {
	m := newOldMachine(t)

	if stderr := m.prepare(); stderr != "" {
		t.Fatalf("the old home should migrate, got %q", stderr)
	}

	markers := placeMarkers(t, m, map[string]time.Duration{"young-session": time.Hour})

	migration.ExpireMarkers(m.env)

	if test.Exists(t, markers) {
		t.Error("a settled home has no failure left to say, so every marker and their folder should go")
	}
}
