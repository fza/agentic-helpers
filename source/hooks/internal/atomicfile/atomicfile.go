// Package atomicfile replaces a file whole, so a reader sees the old content or
// the new, never a mix: the hooks of one event run in parallel and write the
// same state files at once.
package atomicfile

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Write puts data at path through a temporary file of its own in the same
// directory, renamed over path. A temporary name shared by every writer would
// let two writers interleave in it, and the second rename publish the mix.
//
// The file is not synced: a sync on darwin is a full flush of the drive, paid
// inside hook calls.
func Write(path string, data []byte, perm fs.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("creating a temporary file for %s: %w", path, err)
	}

	_, err = temporary.Write(data)
	if err == nil {
		err = temporary.Chmod(perm)
	}

	closed := temporary.Close()
	if err == nil {
		err = closed
	}

	if err == nil {
		err = os.Rename(temporary.Name(), path)
	}

	if err != nil {
		_ = os.Remove(temporary.Name())

		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}
