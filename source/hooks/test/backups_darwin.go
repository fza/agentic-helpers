package test

import (
	"bytes"
	"os/exec"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
)

// BackupExcluded is whether path carries Time Machine's exclusion mark, read
// back as the attribute itself.
func BackupExcluded(t *testing.T, path string) bool {
	t.Helper()

	size, err := unix.Getxattr(path, home.BackupExclusion, nil)

	return err == nil && size > 0
}

// TimeMachineAnswer is what `tmutil isexcluded` prints for path, the word in
// brackets alone, or nothing on a machine without tmutil.
func TimeMachineAnswer(t *testing.T, path string) string {
	t.Helper()

	tmutil, err := exec.LookPath("tmutil")
	if err != nil {
		return ""
	}

	printed, err := exec.CommandContext(t.Context(), tmutil, "isexcluded", path).Output()
	if err != nil {
		t.Fatalf("asking tmutil about %s: %v", path, err)
	}

	answer, _, _ := bytes.Cut(bytes.TrimSpace(printed), []byte(" "))

	return string(answer)
}
