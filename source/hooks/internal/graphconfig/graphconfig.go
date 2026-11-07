// Package graphconfig finds the sdd graph governing a directory and where its
// project keeps its state.
package graphconfig

import (
	"os"
	"path/filepath"
)

const GraphDirName = ".sdd"

// GraphDir walks up from start to the graph governing it. It stops at the
// filesystem root rather than a repository boundary, so a graph one level above
// a nested checkout still answers for it.
func GraphDir(start string) string {
	walked := RealPath(start)
	for {
		held := filepath.Join(walked, GraphDirName)

		info, err := os.Stat(held)
		if err == nil && info.IsDir() {
			return held
		}

		parent := filepath.Dir(walked)
		if parent == walked {
			return ""
		}

		walked = parent
	}
}

// RealPath resolves what exists of a path, as far as it exists.
func RealPath(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return path
	}

	resolved, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		return resolved
	}

	parent := filepath.Dir(absolute)
	if parent == absolute {
		return absolute
	}

	return filepath.Join(RealPath(parent), filepath.Base(absolute))
}
