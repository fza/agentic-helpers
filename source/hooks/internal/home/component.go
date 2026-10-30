package home

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/fza/agentic-helpers/source/hooks/internal/atomicfile"
	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
)

// ErrNewerFormat is a component's directory written by a newer release.
var ErrNewerFormat = errors.New("a newer format")

// ComponentFormat is the format the component at dir records in its own
// `format.json`; absent, a directory no release recorded one for, which
// reads as format 1.
func ComponentFormat(dir string) (int, error) {
	data, err := os.ReadFile(filepath.Join(dir, formatName))
	if errors.Is(err, fs.ErrNotExist) {
		return 1, nil
	}

	if err != nil {
		return 0, fmt.Errorf("reading the format of %s: %w", dir, err)
	}

	var held formatFile

	err = json.Unmarshal(data, &held)
	if err != nil || held.Format < 1 {
		return 0, fmt.Errorf("parsing the format of %s: %s", dir, data)
	}

	return held.Format, nil
}

// CheckComponent is the component's format, refusing one newer than current.
func CheckComponent(dir string, current int) (int, error) {
	version, err := ComponentFormat(dir)
	if err != nil {
		return 0, err
	}

	if version > current {
		return version, fmt.Errorf("%w: %s has format %d; this release reads up to %d", ErrNewerFormat, dir, version,
			current)
	}

	return version, nil
}

// WriteComponentFormat records version as the component's format: a
// migration's last step, so a run that failed before it is run again.
func WriteComponentFormat(root string, dir string, version int) error {
	data, err := json.Marshal(formatFile{Format: version})
	if err != nil {
		return fmt.Errorf("encoding the format of %s: %w", dir, err)
	}

	err = safefs.MkdirAll(root, dir, 0o700)
	if err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	err = atomicfile.Write(root, filepath.Join(dir, formatName), append(data, '\n'), 0o600)
	if err != nil {
		return fmt.Errorf("writing the format of %s: %w", dir, err)
	}

	return nil
}
