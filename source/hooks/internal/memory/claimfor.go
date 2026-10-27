package memory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
)

// A launcher seats a session it starts or keeps: `claim <seat> --session <id>`
// writes a reservation, the session's id and no process, which the session's
// first hook takes over; for a session already holding a seat it hands the
// session over, its process carried to the new seat at once.

// claimFor claims the seat for the session req.Session names rather than the
// caller's.
func (mem *memory) claimFor(ctx context.Context, req request) result {
	if !mem.storeExists() {
		err := mem.initStore()
		if err != nil {
			return refused("%v", err)
		}
	}

	if req.Name == "" {
		name, err := mem.mintShared(ctx, req.Shelf)
		if err != nil {
			return refused("%v", err)
		}

		req.Name = name
	}

	key := mem.seatKey(req.Name)
	if key != "" && req.Shelf != "" {
		on := mem.shelfName(mem.shelfOfKey(key))
		if on != req.Shelf {
			return refused("%s is on shelf %s", req.Name, on)
		}
	}

	key, err := mem.ensureSeat(ctx, req.Name, req.Shelf)
	if err != nil {
		return refused("%v", err)
	}

	reply, moving, refusal, err := mem.reserve(ctx, req, key)
	if err != nil {
		return refused("%v", err)
	}

	if refusal != "" {
		return refused("%s", refusal)
	}

	if reply == "" {
		return done(fmt.Sprintf("session %s already holds %s", short(req.Session), req.Name)).
			with(seatFields{Seat: req.Name, Shelf: mem.shelfName(mem.shelfOfKey(key))})
	}

	lines := []string{reply}

	lines = append(lines, mem.turnOffAutoMemory(ctx)...)

	described, err := mem.claimLifetime(ctx, req)
	if err != nil {
		return refused("%v", err)
	}

	if described != "" {
		lines = append(lines, described)
	}

	if moving {
		_ = mem.purgeExpired(ctx)
	}

	return done(strings.Join(lines, "\n")).with(seatFields{Seat: req.Name, Shelf: mem.shelfName(mem.shelfOfKey(key))})
}

// reserve writes the claim for req.Session on the seat filed under key, under
// the seats lock: a reservation, or the session's own claim moved from the seat
// it holds. It answers what it did and whether the session moved, nothing where
// the session holds the seat already, or why the seat cannot be taken.
func (mem *memory) reserve(ctx context.Context, req request, key string) (string, bool, string, error) {
	dir := mem.seatsDir()

	unlock, err := seat.Lock(ctx, dir)
	if err != nil {
		return "", false, "", err
	}
	defer unlock()

	existing, found := seat.Read(dir, key)
	if found && existing.SessionID != req.Session {
		refusal := mem.inUseRefusal(ctx, req.Name, existing)
		if refusal != "" {
			return "", false, refusal, nil
		}
	}

	fromKey, from := seat.OfSession(dir, req.Session)
	if fromKey != "" && fromKey == key && mem.alive(ctx, from) {
		return "", false, "", nil
	}

	moving := fromKey != "" && fromKey != key && (mem.alive(ctx, from) || mem.freshReservation(from))

	now := mem.now().Format(time.RFC3339Nano)
	held := seat.Claim{SessionID: req.Session, Claimed: now, Reserved: now}
	reply := fmt.Sprintf("reserved %s for session %s (shelf %s)", req.Name, short(req.Session),
		mem.shelfName(mem.shelfOfKey(key)))

	if moving {
		held = from
		held.Claimed = now

		if mem.alive(ctx, from) {
			held.MovedFrom = mem.seatName(fromKey)
			reply = fmt.Sprintf("moved session %s from %s to %s (shelf %s)", short(req.Session), held.MovedFrom,
				req.Name, mem.shelfName(mem.shelfOfKey(key)))
		}

		err = seat.Close(dir, fromKey, now)
		if err != nil {
			return "", false, "", err
		}
	}

	err = seat.Write(dir, key, held)
	if err != nil {
		return "", false, "", err
	}

	return reply, moving, "", nil
}

// inUseRefusal is why a claim may not take the seat from its claim, or nothing
// where the claim is free to take.
func (mem *memory) inUseRefusal(ctx context.Context, name string, existing seat.Claim) string {
	switch {
	case mem.alive(ctx, existing):
		return fmt.Sprintf("%s is held by session %s (pid %d), still running; its holder releases it with `%s release`, "+
			"or the seat frees when that session ends", name, short(existing.SessionID), existing.PID, self)
	case mem.freshReservation(existing):
		return fmt.Sprintf("%s is reserved for session %s until it starts, at most %s", name,
			short(existing.SessionID), reservationKeep)
	}

	return ""
}

// adopt makes the hook's own process the holder of a seat reserved for its
// session, the first hook the session runs taking the reservation over.
func (mem *memory) adopt(ctx context.Context, payload hookio.Payload) {
	if payload.SessionID == "" {
		return
	}

	own, _ := mem.ownSeat(ctx)
	if own != "" {
		return
	}

	key, held := seat.OfSession(mem.seatsDir(), payload.SessionID)
	if key == "" || held.PID != 0 || !mem.freshReservation(held) {
		return
	}

	owner := seat.Owner(ctx, mem.env.Processes, mem.env.OwnerName)
	started, running := mem.env.Processes.StartTime(ctx, owner)

	mem.rewriteClaim(ctx, key, func(current seat.Claim) bool { return current == held }, func(claim *seat.Claim) {
		if running {
			claim.Started = seat.FormatStarted(started)
		}

		claim.PID = owner
		claim.Reserved = ""
		claim.TranscriptPath = payload.TranscriptPath
		claim.Claimed = mem.now().Format(time.RFC3339Nano)
	})
}

// moved tells a session a handover moved it, once: the seat it left, the seat
// it holds now, and that seat's brief.
func (mem *memory) moved(ctx context.Context, seatName string, held seat.Claim) string {
	if held.MovedFrom == "" {
		return ""
	}

	from := held.MovedFrom
	mem.rewriteClaim(ctx, mem.seatKey(seatName), sameHolder(held), func(claim *seat.Claim) { claim.MovedFrom = "" })

	folded, err := mem.load(ctx)
	if err != nil {
		return ""
	}

	return fmt.Sprintf("%s: seat moved from %s to %s (shelf %s)\n%s", self, from, seatName,
		mem.shelfName(mem.shelfOf(seatName)), mem.brief(ctx, mem.checkout(ctx), folded, seatName))
}
