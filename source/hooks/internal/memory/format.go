package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
)

// storeFormat is the store's shape this release reads and writes: seats keyed
// by id through the registry. A store without format.json is format 1, keyed
// by seat name.
const (
	storeFormat = 2
	formatName  = "format.json"
)

var ErrNewerStore = errors.New("a newer store")

type formatFile struct {
	Format int `json:"format"`
}

func readFormat(store string) (int, error) {
	raw, err := os.ReadFile(filepath.Join(store, formatName))
	if errors.Is(err, fs.ErrNotExist) {
		return 1, nil
	}

	if err != nil {
		return 0, fmt.Errorf("reading the store's format: %w", err)
	}

	var held formatFile

	err = json.Unmarshal(raw, &held)
	if err != nil || held.Format < 1 {
		return 0, fmt.Errorf("parsing the store's format: %s", raw)
	}

	return held.Format, nil
}

func writeFormat(store string) error {
	data, err := json.Marshal(formatFile{Format: storeFormat})
	if err != nil {
		return fmt.Errorf("encoding the store's format: %w", err)
	}

	temporary := filepath.Join(store, formatName+".tmp")

	err = os.WriteFile(temporary, append(data, '\n'), 0o600)
	if err != nil {
		return fmt.Errorf("writing the store's format: %w", err)
	}

	err = os.Rename(temporary, filepath.Join(store, formatName))
	if err != nil {
		return fmt.Errorf("placing the store's format: %w", err)
	}

	return nil
}

// initStore creates a fresh store at the current format, so it never needs
// migrating.
func (mem *memory) initStore() error {
	err := os.MkdirAll(mem.store(), 0o700)
	if err != nil {
		return fmt.Errorf("creating the store: %w", err)
	}

	registry, found, err := seat.LoadRegistry(mem.store())
	if err != nil {
		return err
	}

	if !found {
		err = registry.Save()
		if err != nil {
			return err
		}
	}

	mem.forgetRegistry()

	return writeFormat(mem.store())
}
