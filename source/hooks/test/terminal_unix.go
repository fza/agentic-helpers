//go:build darwin || linux

package test

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// openUserSide opens a pseudo-terminal's user side without making it the
// test process's controlling terminal.
func openUserSide(t *testing.T, name string) *os.File {
	t.Helper()

	file, err := os.OpenFile(name, os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("opening %s: %v", name, err)
	}

	t.Cleanup(func() { _ = file.Close() })

	return file
}
