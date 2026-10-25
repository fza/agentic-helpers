package memory

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
)

// Every seat files onto one shelf, and the seats on a shelf share its entries:
// an entry belongs to its filing seat's shelf, which seats.json fixes, so no
// event names a shelf. A seat the registry gives no shelf, in a store of an
// earlier format, is a shelf of its own under its seat key.

func (mem *memory) shelves() (seat.Shelves, bool) {
	if mem.shelfRegistry == nil {
		shelves, found, err := seat.LoadShelves(mem.store())
		if err != nil {
			found = false
		}

		mem.shelfRegistry, mem.shelvesFound = &shelves, found
	}

	return *mem.shelfRegistry, mem.shelvesFound
}

func (mem *memory) forgetShelves() {
	mem.shelfRegistry = nil
}

// shelfOfKey is the shelf the seat filed under key files onto. A row naming
// its seat by name, as a store keyed by name wrote it, resolves through the
// name.
func (mem *memory) shelfOfKey(key string) string {
	registry, found := mem.registry()
	if !found {
		return key
	}

	held, known := registry.Seats[key]
	if !known {
		id, named := registry.IDOf(key)
		held, known = registry.Seats[id], named
	}

	if known && held.Shelf != "" {
		return held.Shelf
	}

	return key
}

// shelfOf is the shelf of the live seat named seatName, or empty where no seat
// has the name.
func (mem *memory) shelfOf(seatName string) string {
	key := mem.seatKey(seatName)
	if key == "" {
		return ""
	}

	return mem.shelfOfKey(key)
}

// shelfName is how a shelf reads: its name, or the seat's for a shelf of its own.
func (mem *memory) shelfName(shelf string) string {
	shelves, _ := mem.shelves()

	held, known := shelves.Shelves[shelf]
	if known {
		return held.Name
	}

	return mem.seatName(shelf)
}

func (mem *memory) entryShelf(held *entry) string {
	return mem.shelfOfKey(held.seatKey)
}

// onShelf is whether the entry belongs to the shelf seatName files onto.
func (mem *memory) onShelf(held *entry, seatName string) bool {
	mine := mem.shelfOf(seatName)

	return mine != "" && mem.entryShelf(held) == mine
}

// recipientShelf is the shelf a `--for` key names: a shelf id, or a seat key
// an earlier release stored, standing for that seat's shelf.
func (mem *memory) recipientShelf(key string) string {
	if seat.IsShelfID(key) {
		return key
	}

	return mem.shelfOfKey(key)
}

// newShelfName says why name cannot name a shelf, or nothing where it can.
func newShelfName(name string) string {
	switch {
	case !seat.ValidName(name):
		return fmt.Sprintf("%v, %q is not one", seat.ErrBadName, name)
	case seat.IsShelfID(name) || seat.IsID(name):
		return fmt.Sprintf("%q has the shape of an id; name the shelf otherwise", name)
	case name == forAllSeats:
		return fmt.Sprintf("%q is the word --for takes for every seat; name the shelf otherwise", name)
	}

	return ""
}

// ensureSeat is the key of the seat named name, minting it where the store has
// no seat by that name yet: onto the shelf named shelfName, made where no shelf
// carries the name, or onto a shelf of its own under its name where shelfName
// is empty.
func (mem *memory) ensureSeat(ctx context.Context, name string, shelfName string) (string, error) {
	key := mem.seatKey(name)
	if key != "" {
		return key, nil
	}

	unlock, err := mem.lockStore(ctx)
	if err != nil {
		return "", err
	}
	defer unlock()

	return mem.mintSeat(name, shelfName)
}

// mintSeat mints the seat and, where needed, its shelf; the caller holds the
// store lock.
func (mem *memory) mintSeat(name string, shelfName string) (string, error) {
	mem.forgetRegistry()
	mem.forgetShelves()

	registry, _ := mem.registry()

	id, found := registry.IDOf(name)
	if found {
		return id, nil
	}

	shelves, _ := mem.shelves()

	own := shelfName == ""
	if own {
		shelfName = name
	}

	shelf, exists := shelves.IDOf(shelfName)
	if own && exists {
		return "", fmt.Errorf("%s names a shelf; claim it with --shelf %s", name, name)
	}

	if !exists {
		shelf, err := shelves.Mint(shelfName, mem.env.Random)
		if err != nil {
			return "", fmt.Errorf("making the shelf %s: %w", shelfName, err)
		}

		err = shelves.Save()
		if err != nil {
			return "", err
		}

		return mem.mintOnto(registry, name, shelf)
	}

	return mem.mintOnto(registry, name, shelf)
}

func (mem *memory) mintOnto(registry seat.Registry, name string, shelf string) (string, error) {
	id, err := registry.Mint(name, mem.env.Random)
	if err != nil {
		return "", fmt.Errorf("minting the seat %s: %w", name, err)
	}

	held := registry.Seats[id]
	held.Shelf = shelf
	registry.Seats[id] = held

	err = registry.Save()
	if err != nil {
		return "", err
	}

	mem.forgetRegistry()
	mem.forgetShelves()

	return id, nil
}

var errNoFreeSeatName = errors.New("no free seat name on the shelf")

// mintShared mints the next free `<shelf>-<n>` seat onto the shelf, under the
// store lock, so two sessions joining at once never draw one name.
func (mem *memory) mintShared(ctx context.Context, shelfName string) (string, error) {
	unlock, err := mem.lockStore(ctx)
	if err != nil {
		return "", err
	}
	defer unlock()

	mem.forgetRegistry()
	mem.forgetShelves()

	registry, _ := mem.registry()
	shelves, _ := mem.shelves()

	for n := 2; n < 1000; n++ {
		name := shelfName + "-" + strconv.Itoa(n)
		if !seat.ValidName(name) {
			return "", fmt.Errorf("%w: %s-<n> breaks the name rule; name the seat", errNoFreeSeatName, shelfName)
		}

		_, taken := registry.IDOf(name)
		_, shelfTaken := shelves.IDOf(name)

		if !taken && !shelfTaken {
			_, err = mem.mintSeat(name, shelfName)
			if err != nil {
				return "", err
			}

			return name, nil
		}
	}

	return "", fmt.Errorf("%w %s", errNoFreeSeatName, shelfName)
}
