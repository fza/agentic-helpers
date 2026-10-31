package memory_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestASessionStartRemovesTheStoresExpiredBackups(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	now := h.clock.Now()
	backups := filepath.Join(h.store(), "backup")
	stamp := func(age time.Duration) string { return now.Add(-age).UTC().Format(home.BackupStamp) }

	expiredFormat := filepath.Join(backups, "format-6-"+stamp(home.BackupLifetime))
	expiredScratch := filepath.Join(backups, "scratch-paths-"+stamp(45*24*time.Hour))
	young := filepath.Join(backups, "format-6-"+stamp(home.BackupLifetime-time.Minute))
	unstamped := filepath.Join(backups, "format-6-by-hand")
	loose := filepath.Join(backups, "format-6-"+stamp(60*24*time.Hour)+".tar")
	link := filepath.Join(backups, "scratch-paths-"+stamp(60*24*time.Hour))
	elsewhere := filepath.Join(filepath.Dir(h.repo), "elsewhere")

	for _, folder := range []string{expiredFormat, expiredScratch, young, unstamped} {
		test.WriteFile(t, filepath.Join(folder, "log", "manifest.json"), "{}\n")
	}

	test.WriteFile(t, loose, "an archive\n")
	test.WriteFile(t, filepath.Join(elsewhere, "precious"), "kept\n")

	err := os.Symlink(elsewhere, link)
	if err != nil {
		t.Fatal(err)
	}

	h.must(unrelatedStart(), "session-start")

	for _, gone := range []string{expiredFormat, expiredScratch} {
		if test.Exists(t, gone) {
			t.Errorf("a backup the lifetime old or older should be removed at the session's start, %s stayed", filepath.Base(gone))
		}
	}

	for _, kept := range []string{young, unstamped, loose, link, filepath.Join(elsewhere, "precious")} {
		if !test.Exists(t, kept) {
			t.Errorf("a younger backup, an unstamped folder, a loose file and a link should stay, %s went", filepath.Base(kept))
		}
	}

	if mismatches := test.LedgerMismatches(t, h.store()); len(mismatches) > 0 {
		t.Errorf("the size ledger should match the store's files after the removal: %v", mismatches)
	}
}

func TestACallOtherThanTheSessionStartLeavesBackups(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	expired := filepath.Join(h.store(), "backup",
		"format-6-"+h.clock.Now().Add(-60*24*time.Hour).UTC().Format(home.BackupStamp))
	test.WriteFile(t, filepath.Join(expired, "log", "manifest.json"), "{}\n")

	h.must("", "topics")

	if !test.Exists(t, expired) {
		t.Error("only a session's start should expire the store's backups")
	}
}
