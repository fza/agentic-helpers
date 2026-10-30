// Package atomicfile replaces a file whole, so a reader sees the old content or
// the new, never a mix: the hooks of one event run in parallel and write the
// same state files at once.
package atomicfile

import (
	"fmt"
	"io/fs"
	"os"

	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
)

// Write puts data at path below root through a temporary file of its own in
// the same directory, renamed over path. A temporary name shared by every
// writer would let two writers interleave in it, and the second rename publish
// the mix. The folders from root down are walked through safefs, so a planted
// link is refused, and a link at path is replaced, never written through.
//
// The file is not synced: a sync on darwin is a full flush of the drive, paid
// inside hook calls.
func Write(root string, path string, data []byte, perm fs.FileMode) error {
	folder, name, err := safefs.Parent(root, path)
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	defer folder.Close()

	err = folder.ReplaceWith(name, "."+name+".*.tmp", perm, func(file *os.File) error {
		_, err := file.Write(data)

		return err
	})
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}
