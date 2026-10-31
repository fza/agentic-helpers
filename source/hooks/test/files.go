package test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// TreeFiles opens files under one of Roots only, through an os.Root, so a
// test never reads outside its own trees: any other path is refused as not
// permitted.
type TreeFiles struct {
	Roots []string
}

func (files TreeFiles) Open(name string) (fs.File, error) {
	for _, root := range files.Roots {
		relative, err := filepath.Rel(root, name)
		if err != nil || relative == ".." || strings.HasPrefix(relative, "../") {
			continue
		}

		opened, err := os.OpenInRoot(root, relative)
		if err != nil {
			return nil, fmt.Errorf("opening %s in the test tree: %w", name, err)
		}

		return opened, nil
	}

	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
}
