package memory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
)

// Seats are names everywhere a person or an agent meets them, and ids in the
// store: events, claim files, slices and brief marks carry the id, and the
// registry is the one place a name resolves through. A store of the earlier
// format has no registry, and there a seat's key is its name.

func (mem *memory) registry() (seat.Registry, bool) {
	if mem.seatRegistry == nil {
		registry, found, err := seat.LoadRegistry(mem.store())
		if err != nil {
			found = false
		}

		mem.seatRegistry, mem.registryFound = &registry, found
	}

	return *mem.seatRegistry, mem.registryFound
}

// forgetRegistry makes the next lookup read the registry again, after a call
// changed it.
func (mem *memory) forgetRegistry() {
	mem.seatRegistry = nil
}

// seatKey is the key the store files the seat named name under: its id, or
// empty where no seat has the name, and the name itself in a store without a
// registry.
func (mem *memory) seatKey(name string) string {
	registry, found := mem.registry()
	if !found {
		return name
	}

	id, _ := registry.IDOf(name)

	return id
}

// seatName is the current name of the seat the store files under key.
func (mem *memory) seatName(key string) string {
	registry, found := mem.registry()
	if !found {
		return key
	}

	return registry.NameOf(key)
}

// ensureSeat is the key of the seat named name, minting it where the store
// has no seat by that name yet.
func (mem *memory) ensureSeat(ctx context.Context, name string) (string, error) {
	key := mem.seatKey(name)
	if key != "" {
		return key, nil
	}

	unlock, err := mem.lockStore(ctx)
	if err != nil {
		return "", err
	}
	defer unlock()

	mem.forgetRegistry()

	registry, _ := mem.registry()

	id, found := registry.IDOf(name)
	if found {
		return id, nil
	}

	id, err = registry.Mint(name, mem.env.Random)
	if err != nil {
		return "", fmt.Errorf("minting the seat %s: %w", name, err)
	}

	err = registry.Save()
	if err != nil {
		return "", err
	}

	mem.forgetRegistry()

	return id, nil
}

// lockStore holds the store's lock until the returned function runs: the
// registry and the format migration are written under it.
func (mem *memory) lockStore(ctx context.Context) (func(), error) {
	err := os.MkdirAll(mem.store(), 0o700)
	if err != nil {
		return nil, fmt.Errorf("creating the store: %w", err)
	}

	file, err := os.OpenFile(filepath.Join(mem.store(), ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening the store lock: %w", err)
	}

	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
	if err != nil {
		_ = file.Close()

		return nil, fmt.Errorf("taking the store lock: %w", err)
	}

	err = ctx.Err()
	if err != nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()

		return nil, fmt.Errorf("holding the store lock: %w", err)
	}

	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}
