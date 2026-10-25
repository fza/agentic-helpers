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

	dir := mem.seatsDir()

	key := mem.seatKey(req.Name)
	if key != "" {
		if req.Shelf != "" {
			on := mem.shelfName(mem.shelfOfKey(key))
			if on != req.Shelf {
				return refused("%s is on shelf %s", req.Name, on)
			}
		}

		existing, found := seat.Read(dir, key)
		if found && existing.SessionID != req.Session {
			refusal := mem.inUseRefusal(ctx, req.Name, existing)
			if refusal != "" {
				return refused("%s", refusal)
			}
		}
	}

	fromKey, from := seat.OfSession(dir, req.Session)
	if fromKey != "" && fromKey == key && mem.alive(ctx, from) {
		return done(fmt.Sprintf("session %s already holds %s", short(req.Session), req.Name))
	}

	moving := fromKey != "" && fromKey != key && (mem.alive(ctx, from) || mem.freshReservation(from))

	key, err := mem.ensureSeat(ctx, req.Name, req.Shelf)
	if err != nil {
		return refused("%v", err)
	}

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
			return refused("%v", err)
		}
	}

	err = seat.Write(dir, key, held)
	if err != nil {
		return refused("%v", err)
	}

	lines := []string{reply}

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

	return done(strings.Join(lines, "\n"))
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
	if running {
		held.Started = seat.FormatStarted(started)
	}

	held.PID = owner
	held.Reserved = ""
	held.TranscriptPath = payload.TranscriptPath
	held.Claimed = mem.now().Format(time.RFC3339Nano)

	_ = seat.Write(mem.seatsDir(), key, held)
}

// moved tells a session a handover moved it, once: the seat it left, the seat
// it holds now, and that seat's brief.
func (mem *memory) moved(ctx context.Context, seatName string, held seat.Claim) string {
	if held.MovedFrom == "" {
		return ""
	}

	from := held.MovedFrom
	held.MovedFrom = ""
	_ = seat.Write(mem.seatsDir(), mem.seatKey(seatName), held)

	folded, err := mem.load(ctx)
	if err != nil {
		return ""
	}

	return fmt.Sprintf("%s: seat moved from %s to %s (shelf %s)\n%s", self, from, seatName,
		mem.shelfName(mem.shelfOf(seatName)), mem.brief(ctx, mem.checkout(ctx), folded, seatName))
}
