package memory

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
)

// reservationKeep is how long a claim made for another session holds its seat
// before that session takes it over; past it the seat is free.
const reservationKeep = 10 * time.Minute

// shelfUse is whether any seat on a shelf is in use, held by a running session
// or reserved, and when the last of them closed.
type shelfUse struct {
	inUse  bool
	closed time.Time
	holder string
}

func parseStamp(text string) time.Time {
	at, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return time.Time{}
	}

	return at
}

func (mem *memory) freshReservation(held seat.Claim) bool {
	if held.Reserved == "" {
		return false
	}

	return mem.now().Sub(parseStamp(held.Reserved)) < reservationKeep
}

// closedAt is when the session behind a claim no longer in use ended: the
// close it recorded, else its transcript's last write, else the claim's time.
// A holder gone without SessionEnd closed at its last sign of life.
func closedAt(held seat.Claim) time.Time {
	at := parseStamp(held.Closed)
	if !at.IsZero() {
		return at
	}

	if held.TranscriptPath != "" {
		info, err := os.Stat(held.TranscriptPath)
		if err == nil {
			return info.ModTime().UTC()
		}
	}

	return parseStamp(held.Claimed)
}

func (mem *memory) useOf(ctx context.Context, shelf string) shelfUse {
	registry, _ := mem.registry()

	var use shelfUse

	for _, key := range registry.OnShelf(shelf) {
		held, found := seat.Read(mem.seatsDir(), key)
		if found && (mem.alive(ctx, held) || mem.freshReservation(held)) {
			use.inUse = true
			use.holder = mem.seatName(key)

			continue
		}

		if !found {
			held, found = seat.ReadLast(mem.seatsDir(), key)
		}

		at := closedAt(held)
		if found && at.After(use.closed) {
			use.closed = at
		}
	}

	return use
}

// expired is every temporary shelf no seat uses whose retention has run out
// since its last seat closed.
func (mem *memory) expired(ctx context.Context) []string {
	shelves, found := mem.shelves()
	if !found {
		return nil
	}

	var due []string

	for _, id := range shelves.IDs() {
		temporary, _, retention, _ := mem.retention(shelves.Shelves[id])
		if !temporary {
			continue
		}

		use := mem.useOf(ctx, id)
		if !use.inUse && mem.now().Sub(use.closed) >= retention {
			due = append(due, id)
		}
	}

	return due
}

// purgeExpired purges every expired shelf of the store.
func (mem *memory) purgeExpired(ctx context.Context) error {
	due := mem.expired(ctx)
	if len(due) == 0 {
		return nil
	}

	_, _, err := mem.purge(ctx, due)

	return err
}

// purge removes the shelves, every seat on them and every entry they filed,
// bodies and events alike, except the entries filed for a shelf that stays:
// those survive, their filer kept as an expired seat while any event names it.
func (mem *memory) purge(ctx context.Context, shelfIDs []string) (int, int, error) {
	unlock, err := mem.lockStore(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer unlock()

	mem.forgetRegistry()
	mem.forgetShelves()

	registry, _ := mem.registry()
	shelves, _ := mem.shelves()

	var seats []string
	for _, shelf := range shelfIDs {
		seats = append(seats, registry.OnShelf(shelf)...)
	}

	folded, err := mem.load(ctx)
	if err != nil {
		return 0, 0, err
	}

	dropped := map[string]bool{}

	for _, held := range folded.entryList() {
		if !slices.Contains(shelfIDs, mem.entryShelf(held)) {
			continue
		}

		reachesAnother := held.forAll || slices.ContainsFunc(held.forKeys, func(key string) bool {
			return !slices.Contains(shelfIDs, mem.recipientShelf(key))
		})
		if !reachesAnother {
			dropped[held.id] = true
		}
	}

	named, err := mem.rewriteWithout(ctx, dropped)
	if err != nil {
		return 0, 0, err
	}

	for _, key := range seats {
		held := registry.Seats[key]
		held.Expired = true
		registry.Seats[key] = held

		err = seat.Forget(mem.seatsDir(), key)
		if err != nil {
			return 0, 0, err
		}
	}

	for _, key := range registry.IDs() {
		if registry.Seats[key].Expired && !named[key] {
			delete(registry.Seats, key)
		}
	}

	for _, shelf := range shelfIDs {
		delete(shelves.Shelves, shelf)
	}

	err = shelves.Save()
	if err != nil {
		return 0, 0, err
	}

	err = registry.Save()
	if err != nil {
		return 0, 0, err
	}

	mem.forgetRegistry()
	mem.forgetShelves()

	return len(seats), len(dropped), nil
}

// rewriteWithout rewrites the log without every row of the dropped entries,
// through the merge policy, and says which seat keys the rows left name.
func (mem *memory) rewriteWithout(ctx context.Context, dropped map[string]bool) (map[string]bool, error) {
	named := map[string]bool{}

	_, err := os.Stat(mem.logDir())
	if errors.Is(err, fs.ErrNotExist) {
		return named, nil
	}

	log, err := mem.openLog()
	if err != nil {
		return nil, err
	}

	if len(dropped) > 0 {
		base := mergePolicy(mem.now(), mem.retiredKeep())

		err = log.Rewrite(ctx, func(rows []memorylog.Row, payload func(int) ([]byte, error)) ([]memorylog.Event, error) {
			var kept []memorylog.Row

			var origin []int

			for index, row := range rows {
				if row.Kind != memorylog.KindMerge && dropped[row.Entry] {
					continue
				}

				kept = append(kept, row)
				origin = append(origin, index)
			}

			return base(kept, func(index int) ([]byte, error) { return payload(origin[index]) })
		})
		if err != nil {
			return nil, fmt.Errorf("purging the log: %w", err)
		}
	}

	snapshot, err := log.Snapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading the purged log: %w", err)
	}

	defer func() { _ = snapshot.Close() }()

	for _, row := range snapshot.Rows() {
		named[row.Seat] = true
	}

	return named, nil
}

// purgeEverywhere purges the expired shelves of every store on the machine at
// the current format: a temporary shelf goes once its retention runs out,
// whichever project's session starts next. A store holding no temporary shelf
// is only read; one this release cannot read is left as it is.
func (mem *memory) purgeEverywhere(ctx context.Context) {
	root := mem.root()

	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.IsDir() {
			return nil
		}

		if entry.Name() == "backup" || entry.Name() == "log" || entry.Name() == "seats" || path == mem.sensorDir() {
			return filepath.SkipDir
		}

		version, err := readFormat(path)
		_, statErr := os.Stat(filepath.Join(path, formatName))

		if statErr != nil || err != nil || version != storeFormat {
			return nil
		}

		key, err := filepath.Rel(root, path)
		if err != nil {
			return filepath.SkipDir
		}

		other := &memory{env: mem.env, streams: mem.streams, key: filepath.ToSlash(key)}
		_ = other.purgeExpired(ctx)

		return filepath.SkipDir
	})
}

// drop purges a temporary shelf no seat uses at once.
func (mem *memory) drop(ctx context.Context, req request) result {
	shelves, _ := mem.shelves()

	id, found := shelves.IDOf(req.Shelf)
	if !found {
		return refused("no shelf called %s; %s who lists them", req.Shelf, self)
	}

	temporary, _, _, _ := mem.retention(shelves.Shelves[id])
	if !temporary {
		return refused("%s is durable; make it temporary first", req.Shelf)
	}

	use := mem.useOf(ctx, id)
	if use.inUse {
		return refused("%s holds a seat on %s", use.holder, req.Shelf)
	}

	seats, entries, err := mem.purge(ctx, []string{id})
	if err != nil {
		return refused("%v", err)
	}

	return done(fmt.Sprintf("dropped %s (%s, %s)", req.Shelf, plural(seats, "seat"), plural(entries, "entry")))
}
