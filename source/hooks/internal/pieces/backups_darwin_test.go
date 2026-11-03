package pieces_test

import (
	"context"
	"os"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/migration"
	"github.com/fza/agentic-helpers/source/hooks/internal/pieces"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func runCheck(t *testing.T, machine migration.Env, hook bool) (int, string, string) {
	t.Helper()

	var captured test.Captured

	env := pieces.Env{
		Settings: settings(t, ""), Hook: hook, Migration: machine,
		Getenv: test.Getenv(test.FlavouredPath(t, []string{"timeout", "sed", "date"}, []string{"sed", "date"})),
	}

	code := pieces.Main(context.Background(), []string{"check"}, env, captured.Streams(""))

	return code, captured.Out.String(), captured.Err.String()
}

func TestSessionStartKeepsTheCacheRootOutOfBackups(t *testing.T) {
	roots := test.Roots(t.TempDir())

	for _, root := range []string{roots.Cache, roots.Data} {
		err := os.MkdirAll(root, 0o700)
		if err != nil {
			t.Fatalf("making %s: %v", root, err)
		}
	}

	code, _, stderr := runCheck(t, migration.Env{Roots: roots}, true)
	if code != 0 {
		t.Fatalf("check should exit 0: %s", stderr)
	}

	if !test.BackupExcluded(t, roots.Cache) {
		t.Error("SessionStart should mark the cache root for backups to skip")
	}

	answer := test.TimeMachineAnswer(t, roots.Cache)
	if answer != "" && answer != "[Excluded]" {
		t.Errorf("Time Machine should count the marked cache root as excluded, answered %s", answer)
	}

	if test.BackupExcluded(t, roots.Data) {
		t.Error("the data root should stay in backups")
	}
}

func TestCheckRunByHandLeavesTheCacheRootAlone(t *testing.T) {
	roots := test.Roots(t.TempDir())

	err := os.MkdirAll(roots.Cache, 0o700)
	if err != nil {
		t.Fatalf("making the cache root: %v", err)
	}

	code, _, stderr := runCheck(t, migration.Env{Roots: roots}, false)
	if code != 0 {
		t.Fatalf("check should exit 0: %s", stderr)
	}

	if test.BackupExcluded(t, roots.Cache) {
		t.Error("only the SessionStart hook should mark the cache root, never a listing run by hand")
	}
}

func TestAFailingBackupMarkChangesNothingSessionStartSays(t *testing.T) {
	marked := test.Roots(t.TempDir())
	refused := test.Roots(t.TempDir())

	for _, root := range []string{marked.Cache, refused.Cache} {
		err := os.MkdirAll(root, 0o700)
		if err != nil {
			t.Fatalf("making %s: %v", root, err)
		}
	}

	// A folder its owner may not write refuses a new extended attribute, as a
	// file system without them does.
	err := os.Chmod(refused.Cache, 0o500)
	if err != nil {
		t.Fatalf("making the cache root read-only: %v", err)
	}

	t.Cleanup(func() { _ = os.Chmod(refused.Cache, 0o700) })

	wantCode, wantOut, wantErr := runCheck(t, migration.Env{Roots: marked}, true)
	code, out, stderr := runCheck(t, migration.Env{Roots: refused}, true)

	if test.BackupExcluded(t, refused.Cache) {
		t.Fatal("a read-only cache root should refuse the mark")
	}

	if code != wantCode || out != wantOut || stderr != wantErr {
		t.Errorf("a failing mark should leave the hook's exit and output as a working one leaves them, got %d %q %q",
			code, out, stderr)
	}
}
