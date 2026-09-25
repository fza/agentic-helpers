// Package graphconfig finds the sdd graph governing a directory and reads what
// the project says about it in `.sdd/grounding.yaml`.
package graphconfig

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/goccy/go-yaml"
)

const (
	GraphDirName = ".sdd"
	fileName     = "grounding.yaml"
)

// Config is the project's own shape for its graph. ListingPrefix names the
// topic prefix every entry carries and every grounding listing reads;
// DraftLint is the command a draft body is linted with. Either may be empty.
type Config struct {
	ListingPrefix string `yaml:"listing_prefix"`
	DraftLint     string `yaml:"draft_lint"`
}

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

// Load reads the project's config. A project carrying no file has the zero
// config; a file that does not parse is an error, because a prefix the project
// meant to set would otherwise go unenforced.
func Load(graphDir string) (Config, error) {
	var config Config

	data, err := os.ReadFile(filepath.Join(graphDir, fileName))
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, nil
	}

	if err != nil {
		return Config{}, fmt.Errorf("reading %s: %w", fileName, err)
	}

	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return Config{}, fmt.Errorf("parsing %s: %w", fileName, err)
	}

	return config, nil
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
