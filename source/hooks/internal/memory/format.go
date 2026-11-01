package memory

import (
	"fmt"

	"github.com/fza/agentic-helpers/source/hooks/internal/formatsteps"
	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
)

// storeFormat is the store's shape this release reads and writes: seats keyed
// by id through the registry, each filing onto a shelf of shelves.json, every
// entry filed with a kind, state rows in the log, todolist items and each
// seat's state ledger as `item` rows, the log's tail a journal of typed
// records, per-session state in the cache's slot file, and a size ledger. A store without format.json is format
// 1, keyed by seat name; format 2 has no shelves; format 3 files entries with
// a retention level and no kind; format 4 holds no `item` rows, which a
// format-4 binary cannot read; format 5 keeps its tail as `tail.jsonl` and a
// JSON file per session and purpose in the cache; format 6 holds no `due` rows,
// which a format-6 binary cannot read; format 7 holds no state ledger, `item`
// rows of the entry `state`, which a format-7 binary would drop unread.
const (
	storeFormat = 8
	dueFormat   = 7
	slotsFormat = 6
	itemsFormat = 5
)

var ErrNewerStore = formatsteps.ErrNewer

// initStore creates a fresh store at the current format, its scratch paths
// marked rewritten, so it never needs migrating.
func (mem *memory) initStore() error {
	err := safefs.MkdirAll(mem.env.Roots.Data, mem.store(), 0o700)
	if err != nil {
		return fmt.Errorf("creating the store: %w", err)
	}

	registry, found, err := seat.LoadRegistry(mem.store())
	if err != nil {
		return err
	}

	if !found {
		err = registry.Save(mem.env.Roots.Data)
		if err != nil {
			return err
		}
	}

	shelves, found, err := seat.LoadShelves(mem.store())
	if err != nil {
		return err
	}

	if !found {
		err = shelves.Save(mem.env.Roots.Data)
		if err != nil {
			return err
		}
	}

	mem.forgetRegistry()
	mem.forgetShelves()

	err = mem.markScratchPaths()
	if err != nil {
		return err
	}

	return formatsteps.Write(mem.env.Roots.Data, mem.store(), storeFormat)
}
