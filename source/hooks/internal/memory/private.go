package memory

import (
	"context"
	"fmt"

	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
)

// A private seat leaves no trace in any other project's `who --all`: the
// listing reads every store on the machine and prints each seat with its
// holder session, process and claim time. The mark lives on the seat's
// registry entry, so it outlasts a release and every later claim.

func (mem *memory) private(ctx context.Context, req request) result {
	if req.Value != "on" && req.Value != "off" {
		return refused("private takes on or off, not %q", req.Value)
	}

	own, _ := mem.ownSeat(ctx)
	if own == "" {
		return refused("this session holds no seat; private marks the seat it holds")
	}

	on := req.Value == "on"

	err := mem.markPrivate(ctx, own, on)
	if err != nil {
		return refused("%v", err)
	}

	if on {
		return done(privateLine(own))
	}

	return done(own + " is no longer private: other projects' who --all lists it")
}

// markPrivate sets or clears the private mark on the seat named name, under
// the store's lock.
func (mem *memory) markPrivate(ctx context.Context, name string, on bool) error {
	unlock, err := mem.lockStore(ctx)
	if err != nil {
		return err
	}
	defer unlock()

	registry, found, err := seat.LoadRegistry(mem.store())
	if err != nil {
		return err
	}

	id, known := registry.IDOf(name)
	if !found || !known {
		return fmt.Errorf("this store keeps no seat ids yet, so %s cannot be marked private", name)
	}

	held := registry.Seats[id]
	if held.Private == on {
		return nil
	}

	held.Private = on
	registry.Seats[id] = held

	err = registry.Save(mem.env.Roots.Data)
	if err != nil {
		return err
	}

	mem.forgetRegistry()

	return nil
}

func privateLine(name string) string {
	return name + " is private: no other project's who lists it"
}
