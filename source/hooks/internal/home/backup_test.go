package home_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestExpireBackupsGoesByTheStampInTheName(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	stamp := func(age time.Duration) string { return now.Add(-age).Format(home.BackupStamp) }

	for _, tc := range []struct {
		name  string
		entry string
		file  bool
		link  bool
		kept  bool
	}{
		{name: "a format backup past the lifetime", entry: "format-6-" + stamp(31*24*time.Hour)},
		{name: "a backup exactly the lifetime old", entry: "scratch-paths-" + stamp(home.BackupLifetime)},
		{name: "a legacy backup past the lifetime", entry: "legacy-" + stamp(90*24*time.Hour)},
		{name: "a backup younger than the lifetime", entry: "format-6-" + stamp(29*24*time.Hour), kept: true},
		{name: "a backup a second short of the lifetime", entry: "scratch-paths-" + stamp(home.BackupLifetime-time.Second),
			kept: true},
		{name: "a folder without a stamp", entry: "keep-me", kept: true},
		{name: "a folder with a stamp of another shape", entry: "format-6-20260901T000000Z", kept: true},
		{name: "a folder named only a stamp", entry: stamp(60 * 24 * time.Hour), kept: true},
		{name: "a loose file named like an old backup", entry: "format-6-" + stamp(60*24*time.Hour), file: true, kept: true},
		{name: "a link named like an old backup", entry: "legacy-" + stamp(60*24*time.Hour), link: true, kept: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := t.TempDir()
			backups := filepath.Join(data, "backup")
			target := filepath.Join(data, "elsewhere")
			test.WriteFile(t, filepath.Join(target, "precious"), "kept\n")

			at := filepath.Join(backups, tc.entry)

			switch {
			case tc.link:
				test.WriteFile(t, filepath.Join(backups, ".placeholder"), "")

				err := os.Symlink(target, at)
				if err != nil {
					t.Fatal(err)
				}
			case tc.file:
				test.WriteFile(t, at, "loose\n")
			default:
				test.WriteFile(t, filepath.Join(at, "log", "manifest.json"), "{}\n")
			}

			err := home.ExpireBackups(context.Background(), data, backups, now)
			if err != nil {
				t.Fatalf("expiring the backups should succeed: %v", err)
			}

			if test.Exists(t, at) != tc.kept {
				t.Errorf("the entry should be kept only where it is no stamped folder past the lifetime, kept: %v",
					test.Exists(t, at))
			}

			if !test.Exists(t, filepath.Join(target, "precious")) {
				t.Error("nothing a link points at should be removed")
			}
		})
	}
}

func TestExpireBackupsWithoutABackupFolderDoesNothing(t *testing.T) {
	data := t.TempDir()

	err := home.ExpireBackups(context.Background(), data, filepath.Join(data, "backup"), time.Now())
	if err != nil {
		t.Errorf("a home without backups should be nothing to do: %v", err)
	}

	if test.Exists(t, filepath.Join(data, "backup")) {
		t.Error("expiring should create nothing")
	}
}

func TestExpireBackupsRefusesABackupFolderThatIsALink(t *testing.T) {
	data := t.TempDir()
	target := filepath.Join(data, "elsewhere")
	old := "legacy-" + time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).Format(home.BackupStamp)
	test.WriteFile(t, filepath.Join(target, old, "precious"), "kept\n")

	err := os.Symlink(target, filepath.Join(data, "backup"))
	if err != nil {
		t.Fatal(err)
	}

	err = home.ExpireBackups(context.Background(), data, filepath.Join(data, "backup"), time.Now())
	if err == nil {
		t.Error("a backup folder that is a link should be refused")
	}

	if !test.Exists(t, filepath.Join(target, old, "precious")) {
		t.Error("nothing behind a linked backup folder should be removed")
	}
}

func TestExpireBackupsStopsOnACancelledContext(t *testing.T) {
	data := t.TempDir()
	old := filepath.Join(data, "backup", "legacy-"+time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).Format(home.BackupStamp))
	test.WriteFile(t, filepath.Join(old, "seats.json"), "{}\n")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := home.ExpireBackups(ctx, data, filepath.Join(data, "backup"), time.Now())
	if err == nil {
		t.Error("a cancelled call should say it stopped")
	}

	if !test.Exists(t, old) {
		t.Error("a cancelled call should remove nothing")
	}
}
