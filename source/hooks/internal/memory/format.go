package memory

import (
	"errors"
	"fmt"

	"github.com/fza/agentic-helpers/source/hooks/internal/formatsteps"
	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
)

// storeFormat is the store's shape this release reads and writes: seats keyed
// by id through the registry, each filing onto a shelf of shelves.json, every
// entry filed with a kind, state rows in the log, todolist items and each
// seat's state ledger as `item` rows, the log's tail a journal of typed
// records, per-session state in the cache's slot file, a size ledger, and
// every entry's name in names.json. A store without format.json is format
// 1, keyed by seat name; format 2 has no shelves; format 3 files entries with
// a retention level and no kind; format 4 holds no `item` rows, which a
// format-4 binary cannot read; format 5 keeps its tail as `tail.jsonl` and a
// JSON file per session and purpose in the cache; format 6 holds no `due` rows,
// which a format-6 binary cannot read; format 7 holds no state ledger, `item`
// rows of the entry `state`, which a format-7 binary would drop unread;
// format 8 names no entry, and a format-8 binary would file entries the index
// never names; format 9 keeps the cache's session state in one fixed-width
// slot file, where format 10 keeps a slot directory with its classes, which a
// format-9 binary cannot read.
const (
	storeFormat  = 10
	namesFormat  = 9
	ledgerFormat = 8
	dueFormat    = 7
	slotsFormat  = 6
	itemsFormat  = 5
)

var (
	ErrNewerStore       = formatsteps.ErrNewer
	ErrDevelopmentBuild = errors.New("a development build")
)

// developmentBuild is the version of a binary no release stamped, and
// binDirVariable the setting a checkout working on these binaries opts in
// through. A development build writes a format no release may read yet, so it
// writes one only there: reached from another project's PATH, it would leave
// that project's store unreadable to the release its sessions run.
const (
	developmentBuild = "dev"
	binDirVariable   = "AGENTIC_HELPERS_BIN_DIR"
)

func (mem *memory) developmentHold() error {
	if mem.env.Version != developmentBuild || mem.getenv(binDirVariable) != "" {
		return nil
	}

	return fmt.Errorf("%w: this build writes store format %d, which a release may not read; it writes it only where %s is set",
		ErrDevelopmentBuild, storeFormat, binDirVariable)
}

// initStore creates a fresh store at the current format, its scratch paths
// marked rewritten, so it never needs migrating.
func (mem *memory) initStore() error {
	err := mem.developmentHold()
	if err != nil {
		return err
	}

	err = safefs.MkdirAll(mem.env.Roots.Data, mem.store(), 0o700)
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
