package memory

import (
	"context"
	"fmt"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
	"github.com/fza/agentic-helpers/source/hooks/internal/span"
)

// A shelf is durable or temporary. A temporary shelf's retention runs once the
// last seat on it closes, and when it runs out the shelf, its seats and its
// entries are purged. A shelf keeps `temporary` for the default retention, the
// config.yaml option temporary_seat_lifetime, or the span itself; durable keeps
// nothing.
const (
	lifetimeDurable   = "durable"
	lifetimeTemporary = "temporary"
	defaultLifetime   = pluginconfig.DefaultTemporarySeatLifetime
)

// retention reads a span as every time option reads one.
func retention(text string) (time.Duration, bool) {
	held, err := span.Parse(text)

	return held, err == nil
}

// lifetimeValue is what a shelf stores for a value `lifetime` takes, or why
// the value is none.
func lifetimeValue(value string) (string, error) {
	switch value {
	case lifetimeDurable:
		return "", nil
	case lifetimeTemporary:
		return lifetimeTemporary, nil
	}

	_, valid := retention(value)
	if !valid {
		return "", fmt.Errorf("lifetime takes durable, temporary or a span such as 90m, 12h, 2d, 1w or 0, not %q", value)
	}

	return value, nil
}

// defaultRetention is the retention a temporary shelf without a span of its
// own keeps, and a note where the option holds no span.
func (mem *memory) defaultRetention() (string, time.Duration, string) {
	text := mem.options().TemporarySeatLifetime
	if text == "" {
		text = defaultLifetime
	}

	held, valid := retention(text)
	if !valid {
		fallback, _ := retention(defaultLifetime)

		return defaultLifetime, fallback, fmt.Sprintf("temporary_seat_lifetime %q is no span; %s holds", text, defaultLifetime)
	}

	return text, held, ""
}

// retention is whether the shelf is temporary, and the span its retention
// runs, with its spelling and any note on the default.
func (mem *memory) retention(shelf seat.Shelf) (bool, string, time.Duration, string) {
	switch shelf.Lifetime {
	case "":
		return false, "", 0, ""
	case lifetimeTemporary:
		text, held, note := mem.defaultRetention()

		return true, text, held, note
	}

	held, valid := retention(shelf.Lifetime)
	if !valid {
		text, fallback, note := mem.defaultRetention()

		return true, text, fallback, note
	}

	return true, shelf.Lifetime, held, ""
}

// describeLifetime is how a shelf's lifetime reads in a reply.
func (mem *memory) describeLifetime(shelf seat.Shelf) string {
	temporary, text, _, note := mem.retention(shelf)
	if !temporary {
		return fmt.Sprintf("shelf %s is durable", shelf.Name)
	}

	reply := fmt.Sprintf("shelf %s is temporary: purged %s after its last seat closes", shelf.Name, text)
	if note != "" {
		reply += " (" + note + ")"
	}

	return reply
}

// setLifetime gives the shelf the lifetime value names.
func (mem *memory) setLifetime(ctx context.Context, shelf string, value string) (seat.Shelf, error) {
	stored, err := lifetimeValue(value)
	if err != nil {
		return seat.Shelf{}, err
	}

	unlock, err := mem.lockStore(ctx)
	if err != nil {
		return seat.Shelf{}, err
	}
	defer unlock()

	mem.forgetShelves()

	shelves, found := mem.shelves()

	held, known := shelves.Shelves[shelf]
	if !found || !known {
		return seat.Shelf{}, fmt.Errorf("this store keeps no shelf %s", shelf)
	}

	held.Lifetime = stored
	shelves.Shelves[shelf] = held

	err = shelves.Save(mem.env.Roots.Data)
	if err != nil {
		return seat.Shelf{}, err
	}

	mem.forgetShelves()

	return held, nil
}

// lifetime sets the lifetime of the shelf of the seat the calling session
// holds: `durable`, `temporary` for the default retention, or a span.
func (mem *memory) lifetime(ctx context.Context, req request) result {
	own, _ := mem.ownSeat(ctx)
	if own == "" {
		return refused("%s", ErrNoSeat)
	}

	held, err := mem.setLifetime(ctx, mem.shelfOf(own), req.Value)
	if err != nil {
		return refused("%v", err)
	}

	temporary, _, _, _ := mem.retention(held)

	return done(mem.describeLifetime(held)).with(lifetimeFields{Shelf: held.Name, Temporary: temporary})
}
