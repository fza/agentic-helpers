package test

import (
	"os"
	"strconv"
	"testing"

	"golang.org/x/sys/unix"
)

// PseudoTerminal opens the user side of a fresh pseudo-terminal, closed when
// the test ends: a standard input a person types into, without one.
func PseudoTerminal(t *testing.T) *os.File {
	t.Helper()

	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no pseudo-terminal to open: %v", err)
	}

	t.Cleanup(func() { _ = master.Close() })

	descriptor := int(master.Fd())

	err = unix.IoctlSetPointerInt(descriptor, unix.TIOCSPTLCK, 0)
	if err != nil {
		t.Fatalf("unlocking the pseudo-terminal: %v", err)
	}

	number, err := unix.IoctlGetInt(descriptor, unix.TIOCGPTN)
	if err != nil {
		t.Fatalf("numbering the pseudo-terminal: %v", err)
	}

	return openUserSide(t, "/dev/pts/"+strconv.Itoa(number))
}
