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

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
)

// Seat files of a format-1 store, named for the seat; the longer suffix goes
// first, so `main.last.json` reads as `main`, not `main.last`.
var seatFileSuffixes = []string{".last.json", ".json", ".slice"}

// prepare brings the store to the current format before anything reads it. A
// store already there costs one stat; a store of a newer format is refused.
func (mem *memory) prepare(ctx context.Context) error {
	if mem.outsideGit || !mem.storeExists() {
		return nil
	}

	version, err := readFormat(mem.store())
	if err != nil {
		return err
	}

	switch {
	case version == storeFormat:
		return nil
	case version > storeFormat:
		return fmt.Errorf("%w: this store has format %d; this release reads up to %d", ErrNewerStore, version, storeFormat)
	}

	return mem.migrate(ctx)
}

// migrate brings an older store to the current format one step at a time,
// holding the store lock throughout so no other call reads a half-migrated
// store.
func (mem *memory) migrate(ctx context.Context) error {
	unlock, err := mem.lockStore(ctx)
	if err != nil {
		return err
	}
	defer unlock()

	version, err := readFormat(mem.store())
	if err != nil || version == storeFormat {
		return err
	}

	if version == 1 {
		err = mem.migrateFormat1(ctx)
		if err != nil {
			return err
		}
	}

	if version <= 2 {
		err = mem.migrateFormat2()
		if err != nil {
			return err
		}
	}

	switch {
	case version <= 3:
		err = mem.migrateFormat3(ctx)
	case version == 4:
		err = mem.migrateFormat4()
	}

	if err != nil {
		return err
	}

	return mem.migrateFormat5(ctx)
}

// migrateFormat4 moves a format-4 store to format 5, whose log may hold
// `item` rows: nothing in the log changes, so the log is copied to
// backup/format-4-<UTC stamp>/ and format.json is written, which keeps a
// format-4 binary from reading a store it would misread.
func (mem *memory) migrateFormat4() error {
	backup := filepath.Join(mem.store(), "backup", "format-4-"+mem.now().Format("20060102T150405.000000000Z"))

	err := copyTree(mem.logDir(), filepath.Join(backup, "log"))
	if err != nil {
		return fmt.Errorf("backing up the store's log: %w", err)
	}

	return writeFormat(mem.store(), itemsFormat)
}

// migrateFormat1 turns a format-1 store, keyed by seat name, into format 2,
// keyed by seat id. format.json is written last: until it exists, the store
// reads as format 1, and every step before it is undone on failure.
func (mem *memory) migrateFormat1(ctx context.Context) error {
	err := mem.backUpFormat1()
	if err != nil {
		return err
	}

	events, err := mem.allEvents(ctx)
	if err != nil {
		return err
	}

	registry, _, err := seat.LoadRegistry(mem.store())
	if err != nil {
		return err
	}

	ids := map[string]string{}

	// A key already an id stays as it is: only a seat known by name gets one.
	for _, name := range mem.format1SeatNames(events) {
		if seat.IsID(name) {
			continue
		}

		id, err := registry.Mint(name, mem.env.Random)
		if err != nil {
			return fmt.Errorf("minting an id for the seat %s: %w", name, err)
		}

		ids[name] = id
	}

	for index := range events {
		if id, found := ids[events[index].Seat]; found {
			events[index].Seat = id
		}
	}

	undo := &rollback{}

	err = mem.rewrite(ctx, events, registry, ids, undo)
	if err != nil {
		undo.run()

		return fmt.Errorf("migrating the store failed: %w; the store is unchanged and the next call tries again", err)
	}

	mem.forgetRegistry()

	return nil
}

func (mem *memory) rewrite(ctx context.Context, events []memorylog.Event, registry seat.Registry, ids map[string]string,
	undo *rollback,
) error {
	building := filepath.Join(mem.store(), "log.migrating")

	err := os.RemoveAll(building)
	if err != nil {
		return fmt.Errorf("clearing an earlier migration's log: %w", err)
	}

	undo.add(func() { _ = os.RemoveAll(building) })

	err = memorylog.Build(ctx, building, events)
	if err != nil {
		return fmt.Errorf("writing the migrated log: %w", err)
	}

	err = mem.renameSeatFiles(ids, undo)
	if err != nil {
		return err
	}

	err = mem.swapLog(building, "log.format-1", undo)
	if err != nil {
		return err
	}

	err = registry.Save()
	if err != nil {
		return err
	}

	undo.add(func() { _ = os.Remove(seat.RegistryPath(mem.store())) })

	err = writeFormat(mem.store(), 2)
	if err != nil {
		return err
	}

	_ = os.RemoveAll(filepath.Join(mem.store(), "log.format-1"))

	return nil
}

// backUpFormat1 copies the log and the seat files, byte for byte, to
// backup/format-1-<UTC stamp>/ before anything changes.
func (mem *memory) backUpFormat1() error {
	backup := filepath.Join(mem.store(), "backup", "format-1-"+mem.now().Format("20060102T150405.000000000Z"))

	for _, part := range []string{"log", "seats"} {
		err := copyTree(filepath.Join(mem.store(), part), filepath.Join(backup, part))
		if err != nil {
			return fmt.Errorf("backing up the store's %s: %w", part, err)
		}
	}

	return nil
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

func (mem *memory) renameSeatFiles(ids map[string]string, undo *rollback) error {
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

			from := filepath.Join(mem.seatsDir(), file.Name())
			to := filepath.Join(mem.seatsDir(), ids[name]+suffix)

			err = os.Rename(from, to)
			if err != nil {
				return fmt.Errorf("renaming the seat file %s: %w", file.Name(), err)
			}

			undo.add(func() { _ = os.Rename(to, from) })

			break
		}
	}

	return nil
}

func (mem *memory) swapLog(building string, setAside string, undo *rollback) error {
	retired := filepath.Join(mem.store(), setAside)

	_, err := os.Stat(mem.logDir())
	hadLog := err == nil

	if hadLog {
		err = os.Rename(mem.logDir(), retired)
		if err != nil {
			return fmt.Errorf("setting the old log aside: %w", err)
		}

		undo.add(func() { _ = os.Rename(retired, mem.logDir()) })
	}

	err = os.Rename(building, mem.logDir())
	if err != nil {
		return fmt.Errorf("putting the migrated log in place: %w", err)
	}

	undo.add(func() { _ = os.Rename(mem.logDir(), building) })

	return nil
}

// rollback undoes a migration's steps, the last first.
type rollback struct {
	steps []func()
}

func (undo *rollback) add(step func()) {
	undo.steps = append(undo.steps, step)
}

func (undo *rollback) run() {
	for _, step := range slices.Backward(undo.steps) {
		step()
	}
}

func copyTree(from string, to string) error {
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
			return os.MkdirAll(target, 0o700)
		}

		source, err := os.Open(path)
		if err != nil {
			return err
		}
		defer source.Close()

		copied, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
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

// migrateFormat2 gives every seat of a format-2 store a shelf of its own under
// its name and moves the topics it follows there. The log is untouched: an
// entry's shelf is its filing seat's. seats.json is backed up first, and
// format.json written last, every step before it undone on failure.
func (mem *memory) migrateFormat2() error {
	registryPath := seat.RegistryPath(mem.store())

	backup := filepath.Join(mem.store(), "backup", "format-2-"+mem.now().Format("20060102T150405.000000000Z"))

	err := os.MkdirAll(backup, 0o700)
	if err != nil {
		return fmt.Errorf("backing up the seat registry: %w", err)
	}

	original, err := os.ReadFile(registryPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("backing up the seat registry: %w", err)
	}

	err = os.WriteFile(filepath.Join(backup, "seats.json"), original, 0o600)
	if err != nil {
		return fmt.Errorf("backing up the seat registry: %w", err)
	}

	registry, _, err := seat.LoadRegistry(mem.store())
	if err != nil {
		return err
	}

	shelves, _, err := seat.LoadShelves(mem.store())
	if err != nil {
		return err
	}

	for _, id := range registry.IDs() {
		held := registry.Seats[id]
		if held.Shelf != "" {
			continue
		}

		shelf, err := shelves.Mint(held.Name, mem.env.Random)
		if err != nil {
			return fmt.Errorf("making the shelf of %s: %w", held.Name, err)
		}

		onShelf := shelves.Shelves[shelf]
		onShelf.Follows = held.Follows
		shelves.Shelves[shelf] = onShelf

		held.Shelf, held.Follows = shelf, nil
		registry.Seats[id] = held
	}

	undo := &rollback{}

	err = mem.placeShelves(registry, shelves, original, undo)
	if err != nil {
		undo.run()

		return fmt.Errorf("migrating the store failed: %w; the store is unchanged and the next call tries again", err)
	}

	mem.forgetRegistry()
	mem.forgetShelves()

	return nil
}

func (mem *memory) placeShelves(registry seat.Registry, shelves seat.Shelves, original []byte, undo *rollback) error {
	err := shelves.Save()
	if err != nil {
		return err
	}

	undo.add(func() { _ = os.Remove(seat.ShelvesPath(mem.store())) })

	err = registry.Save()
	if err != nil {
		return err
	}

	undo.add(func() { _ = os.WriteFile(seat.RegistryPath(mem.store()), original, 0o600) })

	return writeFormat(mem.store(), 3)
}
