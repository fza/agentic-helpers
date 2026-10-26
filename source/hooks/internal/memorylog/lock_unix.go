//go:build unix

package memorylog

import (
	"fmt"
	"os"

	"github.com/fza/agentic-helpers/source/hooks/internal/filelock"
)

func lockFile(file *os.File) error {
	return filelock.Exclusive(file)
}

func unlockFile(file *os.File) error {
	return filelock.Unlock(file)
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
