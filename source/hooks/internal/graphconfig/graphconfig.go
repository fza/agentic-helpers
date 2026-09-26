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

var ErrNegativeDepth = errors.New("a `show_depth` depth below 0")

// Config is the project's own shape for its graph. ListingPrefix names the
// topic prefix every entry carries and every grounding listing reads;
// DraftLint is the command a draft body is linted with. Either may be empty.
type Config struct {
	ListingPrefix string
	DraftLint     string
	ShowDepth     ShowDepth
}

// ShowDepth is the least depth an `sdd show` owes in each direction, and the
// seats owing none.
type ShowDepth struct {
	Down        int
	Up          int
	ExemptSeats []string
}

// written is the file as the project wrote it. A depth is a pointer so a key
// left out tells apart from a key set to 0.
type written struct {
	ListingPrefix string `yaml:"listing_prefix"`
	DraftLint     string `yaml:"draft_lint"`
	ShowDepth     struct {
		Down        *int     `yaml:"down"`
		Up          *int     `yaml:"up"`
		ExemptSeats []string `yaml:"exempt_seats"`
	} `yaml:"show_depth"`
}

// Default is the config of a project setting nothing.
func Default() Config {
	return Config{ShowDepth: ShowDepth{Down: 2, Up: 1}}
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

// Load reads the project's config. A key the file leaves out, or a project
// carrying no file, keeps its default; a file that does not parse, or sets a
// negative depth, is an error, because a setting the project meant would
// otherwise go unenforced.
func Load(graphDir string) (Config, error) {
	config := Default()

	data, err := os.ReadFile(filepath.Join(graphDir, fileName))
	if errors.Is(err, fs.ErrNotExist) {
		return config, nil
	}

	if err != nil {
		return Config{}, fmt.Errorf("reading %s: %w", fileName, err)
	}

	var file written

	err = yaml.Unmarshal(data, &file)
	if err != nil {
		return Config{}, fmt.Errorf("parsing %s: %w", fileName, err)
	}

	config.ListingPrefix = file.ListingPrefix
	config.DraftLint = file.DraftLint
	config.ShowDepth.ExemptSeats = file.ShowDepth.ExemptSeats

	if file.ShowDepth.Down != nil {
		config.ShowDepth.Down = *file.ShowDepth.Down
	}

	if file.ShowDepth.Up != nil {
		config.ShowDepth.Up = *file.ShowDepth.Up
	}

	if config.ShowDepth.Down < 0 || config.ShowDepth.Up < 0 {
		return Config{}, fmt.Errorf("parsing %s: %w", fileName, ErrNegativeDepth)
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
