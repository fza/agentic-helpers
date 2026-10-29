package test

import (
	"bytes"
	"os"
	"testing"
	"unsafe"

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

	err = unix.IoctlSetInt(descriptor, unix.TIOCPTYGRANT, 0)
	if err != nil {
		t.Fatalf("granting the pseudo-terminal: %v", err)
	}

	err = unix.IoctlSetInt(descriptor, unix.TIOCPTYUNLK, 0)
	if err != nil {
		t.Fatalf("unlocking the pseudo-terminal: %v", err)
	}

	var name [128]byte

	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(descriptor), unix.TIOCPTYGNAME, uintptr(unsafe.Pointer(&name[0])))
	if errno != 0 {
		t.Fatalf("naming the pseudo-terminal: %v", errno)
	}

	return openUserSide(t, string(name[:bytes.IndexByte(name[:], 0)]))
}
