package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/fza/agentic-helpers/source/hooks/internal/atomicfile"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
)

// storeFormat is the store's shape this release reads and writes: seats keyed
// by id through the registry, each filing onto a shelf of shelves.json, every
// entry filed with a kind, state rows in the log, todolist items as `item`
// rows, the log's tail a journal of typed records, per-session state in the
// cache's slot file, and a size ledger. A store without format.json is format
// 1, keyed by seat name; format 2 has no shelves; format 3 files entries with
// a retention level and no kind; format 4 holds no `item` rows, which a
// format-4 binary cannot read; format 5 keeps its tail as `tail.jsonl` and a
// JSON file per session and purpose in the cache; format 6 holds no `due` rows,
// which a format-6 binary cannot read.
const (
	storeFormat = 7
	slotsFormat = 6
	itemsFormat = 5
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

func writeFormat(store string, version int) error {
	data, err := json.Marshal(formatFile{Format: version})
	if err != nil {
		return fmt.Errorf("encoding the store's format: %w", err)
	}

	err = atomicfile.Write(filepath.Join(store, formatName), append(data, '\n'), 0o600)
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

	shelves, found, err := seat.LoadShelves(mem.store())
	if err != nil {
		return err
	}

	if !found {
		err = shelves.Save()
		if err != nil {
			return err
		}
	}

	mem.forgetRegistry()
	mem.forgetShelves()

	return writeFormat(mem.store(), storeFormat)
}
