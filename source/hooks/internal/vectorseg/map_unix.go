//go:build unix

package vectorseg

import (
	"fmt"
	"math"
	"os"

	"golang.org/x/sys/unix"
)

// mapFile maps path read-only and shared, so every process searching one store
// reads the same page-cache pages. A mapping outlives the unlink of its file.
func mapFile(path string) ([]byte, func() error, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, nil, fmt.Errorf("measuring %s: %w", path, err)
	}

	if info.Size() == 0 {
		return nil, func() error { return nil }, nil
	}

	if info.Size() > math.MaxInt {
		return nil, nil, fmt.Errorf("mapping %s: too large", path)
	}

	data, err := unix.Mmap(int(file.Fd()), 0, int(info.Size()), unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		return nil, nil, fmt.Errorf("mapping %s: %w", path, err)
	}

	// A refused hint changes how fast pages arrive, never what they hold.
	_ = unix.Madvise(data, unix.MADV_WILLNEED)

	return data, func() error { return unix.Munmap(data) }, nil
}
