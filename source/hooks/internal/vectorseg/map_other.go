//go:build !unix

package vectorseg

import (
	"fmt"
	"os"
)

func mapFile(path string) ([]byte, func() error, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", path, err)
	}

	return data, func() error { return nil }, nil
}
