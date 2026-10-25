//go:build unix

package memorylog

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func lockFile(file *os.File) error {
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

func unlockFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}

func syncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("opening the log directory: %w", err)
	}
	defer handle.Close()

	err = handle.Sync()
	if err != nil {
		return fmt.Errorf("syncing the log directory: %w", err)
	}

	return nil
}
