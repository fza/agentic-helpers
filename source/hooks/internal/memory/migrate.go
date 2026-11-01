package memory

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/formatsteps"
	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
)

// Seat files of a format-1 store, named for the seat; the longer suffix goes
// first, so `main.last.json` reads as `main`, not `main.last`.
var seatFileSuffixes = []string{".last.json", ".json", ".slice"}

// formatSteps is the store's step table (internal/formatsteps), one step per
// format, each doing one job. The steps to formats 5, 7 and 8 change nothing
// but the format: each fences off the binaries of the format before, which
// would misread the rows it lets the log hold.
func (mem *memory) formatSteps() []formatsteps.Step {
	return []formatsteps.Step{
		{From: 1, To: 2, Ops: mem.seatIDSteps},
		{From: 2, To: 3, Ops: mem.shelfSteps},
		{From: 3, To: 4, Ops: mem.kindSteps},
		{From: 4, To: itemsFormat, Ops: fenceOnly},
		{From: itemsFormat, To: slotsFormat, Ops: mem.slotSteps},
		{From: slotsFormat, To: dueFormat, Ops: fenceOnly},
		{From: dueFormat, To: storeFormat, Ops: fenceOnly},
	}
}

func fenceOnly(context.Context) ([]formatsteps.Operation, error) { return nil, nil }

func (mem *memory) storeComponent() formatsteps.Component {
	return formatsteps.Component{
		Name: "this store", Dir: mem.store(), Root: mem.env.Roots.Data, Lock: mem.lockStore, Steps: mem.formatSteps(),
	}
}

// prepare brings the store to the current format, its scratch paths
// rewritten, before anything reads it. A store already there costs two
// stats; a store of a newer format is refused.
func (mem *memory) prepare(ctx context.Context) error {
	if mem.outsideGit || !mem.storeExists() {
		return nil
	}

	err := mem.migrate(ctx)
	if err != nil {
		return err
	}

	return mem.rewriteScratchPaths(ctx)
}

// migrate runs the store's step table under the store lock, so no other call
// reads a half-migrated store. A run stopped anywhere, by a failure or a kill,
// leaves the store at the format of the step it stopped in, and the next call
// carries that step on.
func (mem *memory) migrate(ctx context.Context) error {
	component := mem.storeComponent()

	version, err := component.Check()
	if err != nil || version == storeFormat {
		return err
	}

	err = formatsteps.Run(ctx, component)
	if errors.Is(err, formatsteps.ErrNewer) {
		return err
	}

	mem.forgetRegistry()
	mem.forgetShelves()

	if err != nil {
		return fmt.Errorf("migrating the store failed: %w; the next call carries it on", err)
	}

	mem.recordStore(ctx)

	return nil
}

// backupStep copies each part of the store, a folder or a file, to
// backup/format-<from>-<UTC stamp>/ before the step from that format changes
// anything. The copy is built under a dotted name and renamed into place, so
// a stamped folder is a whole copy; a run carrying a killed step on finds it
// and copies nothing, since the store no longer holds what the step found.
func (mem *memory) backupStep(from int, parts ...string) formatsteps.Operation {
	return formatsteps.Operation{Name: "backing up the store", Apply: func(context.Context) error {
		backups := filepath.Join(mem.store(), "backup")
		prefix := fmt.Sprintf("format-%d-", from)

		made, err := filepath.Glob(filepath.Join(backups, prefix+"*"))
		if err != nil || len(made) > 0 {
			return err
		}

		building := filepath.Join(backups, "."+prefix+"copying")

		err = os.RemoveAll(building)
		if err != nil {
			return err
		}

		err = safefs.MkdirAll(mem.env.Roots.Data, building, 0o700)
		if err != nil {
			return err
		}

		for _, part := range parts {
			err = copyTree(mem.env.Roots.Data, filepath.Join(mem.store(), part), filepath.Join(building, part))
			if err != nil {
				return fmt.Errorf("copying %s: %w", part, err)
			}
		}

		return safefs.Rename(mem.env.Roots.Data, building, filepath.Join(backups, prefix+mem.now().UTC().Format(home.BackupStamp)))
	}}
}

// liveLog is the log a step reads: `log/`, or, where a run was killed between
// setting it aside and putting the migrated log in place, the set-aside one;
// empty where the store holds neither.
func (mem *memory) liveLog(setAside string) string {
	for _, dir := range []string{mem.logDir(), filepath.Join(mem.store(), setAside)} {
		_, err := os.Stat(dir)
		if err == nil {
			return dir
		}
	}

	return ""
}

// rebuildSteps swap a log built from events in for the store's own. A stale
// set-aside log, left by a run killed before it was removed, is cleared before
// the live log takes its name: the step's backup holds the log it found.
func (mem *memory) rebuildSteps(events []memorylog.Event, setAside string) []formatsteps.Operation {
	building := filepath.Join(mem.store(), "log.migrating")
	retired := filepath.Join(mem.store(), setAside)

	return []formatsteps.Operation{
		{Name: "writing the migrated log", Apply: func(ctx context.Context) error {
			err := os.RemoveAll(building)
			if err != nil {
				return err
			}

			return memorylog.Build(ctx, mem.env.Roots.Data, building, events)
		}},
		{Name: "setting the old log aside", Apply: func(context.Context) error {
			_, err := os.Stat(mem.logDir())
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}

			err = os.RemoveAll(retired)
			if err != nil {
				return err
			}

			return safefs.Rename(mem.env.Roots.Data, mem.logDir(), retired)
		}},
		{Name: "putting the migrated log in place", Apply: func(context.Context) error {
			return safefs.Rename(mem.env.Roots.Data, building, mem.logDir())
		}},
		mem.removeSetAside(setAside),
	}
}

func (mem *memory) removeSetAside(setAside string) formatsteps.Operation {
	return formatsteps.Operation{Name: "removing the old log", Apply: func(context.Context) error {
		return os.RemoveAll(filepath.Join(mem.store(), setAside))
	}}
}

// seatIDSteps move a format-1 store, keyed by seat name, to format 2, keyed
// by seat id: every seat name an event or a seat file mentions gets an id,
// recorded in seats.json before anything names it, so a run carrying the step
// on finds the same ids; then the log is rebuilt with every event's seat
// replaced by its id, and every seat file renamed to its id.
func (mem *memory) seatIDSteps(ctx context.Context) ([]formatsteps.Operation, error) {
	events, err := mem.eventsIn(ctx, mem.liveLog("log.format-1"))
	if err != nil {
		return nil, err
	}

	registry, _, err := seat.LoadRegistry(mem.store())
	if err != nil {
		return nil, err
	}

	ids := map[string]string{}

	// A key already an id stays as it is: only a seat known by name gets one.
	for _, name := range mem.format1SeatNames(events) {
		if seat.IsID(name) {
			continue
		}

		id, known := registry.IDOf(name)
		if !known {
			id, err = registry.Mint(name, mem.env.Random)
			if err != nil {
				return nil, fmt.Errorf("minting an id for the seat %s: %w", name, err)
			}
		}

		ids[name] = id
	}

	for index := range events {
		if id, found := ids[events[index].Seat]; found {
			events[index].Seat = id
		}
	}

	operations := []formatsteps.Operation{
		mem.backupStep(1, "log", "seats"),
		{Name: "recording the seat ids", Apply: func(context.Context) error {
			mem.forgetRegistry()

			return registry.Save(mem.env.Roots.Data)
		}},
		{Name: "renaming the seat files", Apply: func(context.Context) error { return mem.renameSeatFiles(ids) }},
	}

	return append(operations, mem.rebuildSteps(events, "log.format-1")...), nil
}

func (mem *memory) eventsIn(ctx context.Context, dir string) ([]memorylog.Event, error) {
	if dir == "" {
		return nil, nil
	}

	log, err := memorylog.Open(mem.env.Roots.Data, dir)
	if err != nil {
		return nil, fmt.Errorf("opening the log: %w", err)
	}

	return eventsOf(ctx, log)
}

func (mem *memory) allEvents(ctx context.Context) ([]memorylog.Event, error) {
	_, err := os.Stat(mem.logDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	log, err := mem.openLog()
	if err != nil {
		return nil, err
	}

	return eventsOf(ctx, log)
}

func eventsOf(ctx context.Context, log *memorylog.Log) ([]memorylog.Event, error) {
	snapshot, err := log.Snapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading the log to migrate: %w", err)
	}

	defer func() { _ = snapshot.Close() }()

	rows := snapshot.Rows()
	events := make([]memorylog.Event, 0, len(rows))

	for index, row := range rows {
		payload, err := snapshot.Payload(index)
		if err != nil {
			return nil, fmt.Errorf("reading the payload of %s %s: %w", row.Kind, row.Entry, err)
		}

		events = append(events, memorylog.Event{
			Kind: row.Kind, Entry: row.Entry, Time: row.Time, Seat: row.Seat, Target: row.Target, Payload: payload,
		})
	}

	return events, nil
}

// format1SeatNames is every seat name a format-1 store mentions, in an event
// or a seat file, sorted.
func (mem *memory) format1SeatNames(events []memorylog.Event) []string {
	seen := map[string]bool{}

	for _, event := range events {
		if event.Seat != "" {
			seen[event.Seat] = true
		}
	}

	files, _ := os.ReadDir(mem.seatsDir())
	for _, file := range files {
		name, found := seatFileName(file.Name())
		if found {
			seen[name] = true
		}
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}

	slices.Sort(names)

	return names
}

func seatFileName(file string) (string, bool) {
	for _, suffix := range seatFileSuffixes {
		name, found := strings.CutSuffix(file, suffix)
		if found && seat.ValidName(name) {
			return name, true
		}
	}

	return "", false
}

// renameSeatFiles renames every seat file named for a seat to its id; a file
// already renamed no longer carries the name.
func (mem *memory) renameSeatFiles(ids map[string]string) error {
	files, err := os.ReadDir(mem.seatsDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("listing the seat files: %w", err)
	}

	for _, file := range files {
		for _, suffix := range seatFileSuffixes {
			name, found := strings.CutSuffix(file.Name(), suffix)
			if !found || ids[name] == "" {
				continue
			}

			err = safefs.Rename(mem.env.Roots.Data, filepath.Join(mem.seatsDir(), file.Name()),
				filepath.Join(mem.seatsDir(), ids[name]+suffix))
			if err != nil {
				return fmt.Errorf("renaming the seat file %s: %w", file.Name(), err)
			}

			break
		}
	}

	return nil
}

// copyTree copies the folder or file from to to; a missing one copies
// nothing.
func copyTree(root string, from string, to string) error {
	return filepath.WalkDir(from, func(path string, entry fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && path == from {
			return nil
		}

		if err != nil {
			return err
		}

		relative, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}

		target := filepath.Join(to, relative)

		if entry.IsDir() {
			return safefs.MkdirAll(root, target, 0o700)
		}

		source, err := os.Open(path)
		if err != nil {
			return err
		}
		defer source.Close()

		err = safefs.MkdirAll(root, filepath.Dir(target), 0o700)
		if err != nil {
			return err
		}

		copied, err := safefs.CreateFile(root, target, 0o600)
		if err != nil {
			return err
		}

		_, err = io.Copy(copied, source)
		if err != nil {
			_ = copied.Close()

			return err
		}

		return copied.Close()
	})
}

// shelfSteps move a format-2 store to format 3: every seat gets a shelf of
// its own under its name, the topics it follows moved there. The log is
// untouched: an entry's shelf is its filing seat's. shelves.json is written
// before seats.json names a shelf, and a shelf already minted under the seat's
// name is the one a killed run made, so a run carrying the step on mints none
// twice.
func (mem *memory) shelfSteps(context.Context) ([]formatsteps.Operation, error) {
	registry, _, err := seat.LoadRegistry(mem.store())
	if err != nil {
		return nil, err
	}

	shelves, _, err := seat.LoadShelves(mem.store())
	if err != nil {
		return nil, err
	}

	for _, id := range registry.IDs() {
		held := registry.Seats[id]
		if held.Shelf != "" {
			continue
		}

		shelf, minted := shelves.IDOf(held.Name)
		if !minted {
			shelf, err = shelves.Mint(held.Name, mem.env.Random)
			if err != nil {
				return nil, fmt.Errorf("making the shelf of %s: %w", held.Name, err)
			}
		}

		onShelf := shelves.Shelves[shelf]
		onShelf.Follows = held.Follows
		shelves.Shelves[shelf] = onShelf

		held.Shelf, held.Follows = shelf, nil
		registry.Seats[id] = held
	}

	return []formatsteps.Operation{
		mem.backupStep(2, "seats.json"),
		{Name: "writing the shelves", Apply: func(context.Context) error {
			mem.forgetShelves()

			return shelves.Save(mem.env.Roots.Data)
		}},
		{Name: "moving every seat onto its shelf", Apply: func(context.Context) error {
			mem.forgetRegistry()

			return registry.Save(mem.env.Roots.Data)
		}},
	}, nil
}
