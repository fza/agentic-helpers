//go:build unix

package memorylog

import (
	"fmt"
	"math"
	"os"
	"syscall"
)

// mapFile maps path read-only. A zero-length file is never mapped: mmap
// refuses a zero length.
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

	data, err := syscall.Mmap(int(file.Fd()), 0, int(info.Size()), syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return nil, nil, fmt.Errorf("mapping %s: %w", path, err)
	}

	return data, func() error { return syscall.Munmap(data) }, nil
}
