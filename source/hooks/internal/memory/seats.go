package memory

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
	"github.com/fza/agentic-helpers/source/hooks/internal/transcript"
)

// claimBudget bounds what a claim prints, brief and slice together.
const claimBudget = 20000

type holderView struct {
	Seat           string `json:"seat"`
	SessionID      string `json:"session_id"`
	PID            int    `json:"pid,omitempty"`
	Started        string `json:"started,omitempty"`
	Claimed        string `json:"claimed,omitempty"`
	TranscriptPath string `json:"transcript_path,omitempty"`
	Alive          bool   `json:"alive"`
	Reserved       bool   `json:"reserved,omitempty"`
}

func (mem *memory) viewOf(name string, held seat.Claim, alive bool) holderView {
	return holderView{
		Seat: name, SessionID: held.SessionID, PID: held.PID, Started: held.Started, Claimed: held.Claimed,
		TranscriptPath: held.TranscriptPath, Alive: alive, Reserved: !alive && mem.freshReservation(held),
	}
}

func (mem *memory) alive(ctx context.Context, held seat.Claim) bool {
	return seat.Alive(ctx, mem.env.Processes, mem.env.OwnerName, held)
}

func (mem *memory) holder(ctx context.Context, req request) result {
	if req.Session != "" {
		key, held := seat.OfSession(mem.seatsDir(), req.Session)
		name := ""
		if key != "" {
			name = mem.seatName(key)
		}

		view := holderView{Seat: name, SessionID: req.Session}

		if name != "" {
			view = mem.viewOf(name, held, mem.alive(ctx, held))
		}

		return mem.holderAnswer(view, req.JSON || true)
	}

	if req.Name == "" {
		return refused("holder names a seat, or takes --session <id> --json")
	}

	if !seat.ValidName(req.Name) {
		return refused("%v, %q is not one", seat.ErrBadName, req.Name)
	}

	held, found := seat.Read(mem.seatsDir(), mem.seatKey(req.Name))
	view := holderView{Seat: req.Name}

	if found {
		view = mem.viewOf(req.Name, held, mem.alive(ctx, held))
	}

	return mem.holderAnswer(view, req.JSON)
}

func (mem *memory) holderAnswer(view holderView, asJSON bool) result {
	code := 1
	if view.Seat != "" && view.Alive {
		code = 0
	}

	if asJSON {
		data, err := json.Marshal(view)
		if err != nil {
			return refused("encoding the holder: %v", err)
		}

		return result{text: string(data), code: code}
	}

	if code == 0 {
		return result{text: fmt.Sprintf("%s is held by session %s (pid %d)", view.Seat, short(view.SessionID), view.PID)}
	}

	if view.Reserved {
		return result{text: fmt.Sprintf("%s is reserved for session %s", view.Seat, short(view.SessionID)), code: 1}
	}

	return result{text: view.Seat + " is free", code: 1}
}

// newSeatName says why name cannot name a seat, or nothing where it can. An id's
// shape is taken: a name in it would read as another seat's id.
func newSeatName(name string) string {
	switch {
	case !seat.ValidName(name):
		return fmt.Sprintf("%v, %q is not one", seat.ErrBadName, name)
	case seat.IsID(name):
		return fmt.Sprintf("%q has the shape of a seat id, s- and 6 letters or digits; name the seat otherwise", name)
	case name == forAllSeats:
		return fmt.Sprintf("%q is the word --for takes for every seat; name the seat otherwise", name)
	}

	return ""
}

func (mem *memory) claim(ctx context.Context, req request) result {
	if req.Name == "" && req.Shelf == "" {
		return refused("claim names a seat, or a shelf with --shelf to claim a new seat on it")
	}

	if req.Shelf != "" {
		why := newShelfName(req.Shelf)
		if why != "" {
			return refused("%s", why)
		}
	}

	if req.Name != "" {
		why := newSeatName(req.Name)
		if why != "" {
			return refused("%s", why)
		}
	}

	if req.Session != "" {
		return mem.claimFor(ctx, req)
	}

	owner := seat.Owner(ctx, mem.env.Processes, mem.env.OwnerName)

	current, _ := mem.ownSeat(ctx)
	if current != "" && current != req.Name {
		return refused("this session already holds %s; release that seat before claiming %s", current,
			cmp.Or(req.Name, "another"))
	}

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

	took, refusal, err := mem.take(ctx, req.Name, key, current == req.Name, owner)
	if err != nil {
		return refused("%v", err)
	}

	if refusal != "" {
		return refused("%s", refusal)
	}

	if req.Private {
		err = mem.markPrivate(ctx, req.Name, true)
		if err != nil {
			return refused("%v", err)
		}
	}

	held, previous, hadPrevious := took.held, took.previous, took.hadPrevious

	folded, err := mem.load(ctx)
	if err != nil {
		return refused("%v", err)
	}

	here := mem.checkout(ctx)

	lines := []string{fmt.Sprintf("%s: %s (pid %d)", req.Name, short(held.SessionID), held.PID)}

	if req.Private {
		lines = append(lines, privateLine(req.Name))
	}

	lines = append(lines, mem.turnOffAutoMemory(ctx)...)

	described, err := mem.claimLifetime(ctx, req)
	if err != nil {
		return refused("%v", err)
	}

	if described != "" {
		lines = append(lines, described)
	}

	if owed := mem.doNow(folded, req.Name, onClaim, cmp.Or(mem.env.SessionID, held.SessionID)); owed != "" {
		lines = append(lines, owed)
	}

	lines = append(lines, mem.brief(ctx, here, folded, req.Name))

	hint := mem.carryForwardHint(ctx, req.Name)
	if hint != "" {
		lines = append(lines, hint)
	}

	used := len(strings.Join(lines, "\n"))

	if hadPrevious {
		slice := mem.previousTurns(ctx, folded, req.Name, previous)
		if slice != "" && used < claimBudget-200 {
			lines = append(lines, fmt.Sprintf("last turns of the previous session (%s):", short(previous.SessionID)),
				transcript.Tail(slice, claimBudget-used-100))
		}
	}

	return done(strings.Join(lines, "\n")).with(seatFields{Seat: req.Name, Shelf: mem.shelfName(mem.shelfOf(req.Name))})
}

// closeSeat closes the claim on the seat filed under key, under the seats
// lock: a seat another process holds closes only once its holder is gone. It
// answers whether a claim was closed, or why none may be.
func (mem *memory) closeSeat(ctx context.Context, name string, key string, owner int) (bool, string, error) {
	dir := mem.seatsDir()

	unlock, err := seat.Lock(ctx, dir)
	if err != nil {
		return false, "", err
	}
	defer unlock()

	held, found := seat.Read(dir, key)
	if !found {
		return false, "", nil
	}

	if held.PID != owner && mem.alive(ctx, held) {
		return false, fmt.Sprintf("%s is held by session %s (pid %d); only its holder releases it", name,
			short(held.SessionID), held.PID), nil
	}

	err = seat.Close(dir, key, mem.now().Format(time.RFC3339Nano))
	if err != nil {
		return false, "", err
	}

	mem.recordStore(ctx)

	return true, "", nil
}

type taken struct {
	held        seat.Claim
	previous    seat.Claim
	hadPrevious bool
}

// take writes this session's claim on the seat filed under key, reading and
// judging the claim it replaces under the seats lock, and answers the claim
// written with the previous holder's, or why the seat cannot be taken.
func (mem *memory) take(ctx context.Context, name string, key string, own bool, owner int) (taken, string, error) {
	dir := mem.seatsDir()

	unlock, err := seat.Lock(ctx, dir)
	if err != nil {
		return taken{}, "", err
	}
	defer unlock()

	existing, found := seat.Read(dir, key)
	took := taken{previous: existing, hadPrevious: found}

	if !found {
		took.previous, took.hadPrevious = seat.ReadLast(dir, key)
	}

	// A running holder may be mid-work, and nothing tells a stuck one from a busy
	// one, so no claim ever takes its seat; a fresh reservation waits for its
	// session the same way.
	if found && !own {
		refusal := mem.inUseRefusal(ctx, name, existing)
		if refusal != "" {
			return taken{}, refusal, nil
		}
	}

	took.held = seat.Claim{
		SessionID: mem.env.SessionID, PID: owner, Claimed: mem.now().Format(time.RFC3339Nano), Commit: mem.checkout(ctx).commit,
	}

	if own {
		took.held = existing
		took.previous, took.hadPrevious = seat.ReadLast(dir, key)
	} else if found {
		err = seat.Remove(dir, key)
		if err != nil {
			return taken{}, "", err
		}
	}

	// Rewritten on every claim, a session's own again included, so a claim
	// holding `started` in an earlier release's locale text gains an instant.
	started, running := mem.env.Processes.StartTime(ctx, owner)
	if running {
		took.held.Started = seat.FormatStarted(started)
	}

	err = seat.Write(dir, key, took.held)
	if err != nil {
		return taken{}, "", err
	}

	mem.recordStore(ctx)

	return took, "", nil
}

// claimLifetime sets the lifetime --lifetime names on the claimed seat's
// shelf, and says how a temporary shelf ends.
func (mem *memory) claimLifetime(ctx context.Context, req request) (string, error) {
	shelf := mem.shelfOf(req.Name)

	if req.Lifetime != "" {
		held, err := mem.setLifetime(ctx, shelf, req.Lifetime)
		if err != nil {
			return "", err
		}

		return mem.describeLifetime(held), nil
	}

	shelves, _ := mem.shelves()

	held := shelves.Shelves[shelf]
	if held.Lifetime == "" {
		return "", nil
	}

	return mem.describeLifetime(held), nil
}

// carryForwardHint points a seat at the carry-forward file it still has. The
// file is the agent's to fold and delete; no other file under `.memory/` is
// ever touched.
func (mem *memory) carryForwardHint(ctx context.Context, seatName string) string {
	root := mem.mainRoot(ctx)
	relative := filepath.Join(".memory", "carryforward", seatName+"-memory.md")

	_, err := os.Stat(filepath.Join(root, relative))
	if err != nil {
		return ""
	}

	return fmt.Sprintf("%s still holds this seat's carry-forward: read it once, file each finding with %s new, "+
		"then delete that one file; no other file under .memory/ is touched (agentic-helpers:agent-memory)",
		relative, self)
}

// newestOwn is when the seat last filed an entry or wrote its state slot: the
// point its replay, its nudges and its previous session's turns count from. A
// hook-written draft is no write of the seat's.
func (mem *memory) newestOwn(folded *state, seatName string) time.Time {
	var newest time.Time
	if slot := folded.states[seatName]; !slot.Draft {
		newest = slot.at
	}

	for _, held := range folded.entryList() {
		if held.seat == seatName && held.filed.After(newest) {
			newest = held.filed
		}
	}

	return newest
}

func (mem *memory) slicePath(seatName string) string {
	return filepath.Join(mem.seatsDir(), mem.seatKey(seatName)+".slice")
}

// previousTurns is what the seat's previous session did after its newest
// entry: its slice where that session ended through SessionEnd, else its
// transcript read directly, for a session killed before any hook ran.
func (mem *memory) previousTurns(ctx context.Context, folded *state, seatName string, previous seat.Claim) string {
	raw, err := os.ReadFile(mem.slicePath(seatName))
	if err == nil {
		header, text, _ := strings.Cut(string(raw), "\n")
		if strings.TrimPrefix(header, "session ") == previous.SessionID && previous.SessionID != "" {
			return text
		}
	}

	if previous.TranscriptPath == "" {
		return ""
	}

	turns, err := transcript.Since(ctx, previous.TranscriptPath, mem.newestOwn(folded, seatName))
	if err != nil {
		return ""
	}

	return transcript.Condense(turns)
}

func (mem *memory) release(ctx context.Context, req request) result {
	own, _ := mem.ownSeat(ctx)

	name := req.Name
	if name == "" {
		name = own
	}

	if name == "" {
		return refused("this session holds no seat; name the one to release")
	}

	if !seat.ValidName(name) {
		return refused("%v, %q is not one", seat.ErrBadName, name)
	}

	key := mem.seatKey(name)

	released, refusal, err := mem.closeSeat(ctx, name, key, seat.Owner(ctx, mem.env.Processes, mem.env.OwnerName))
	if err != nil {
		return refused("%v", err)
	}

	if refusal != "" {
		return refused("%s", refusal)
	}

	if !released {
		return done(name + " is free").with(releasedFields{Seat: name})
	}

	mem.endSession(ctx, name)
	_ = mem.purgeExpired(ctx)

	return done("released " + name).with(releasedFields{Seat: name, Released: true})
}

// rename gives the seat the calling session holds a new name. Entries, claims
// and slices name the seat's id, so the registry is the only thing it changes.
func (mem *memory) rename(ctx context.Context, req request) result {
	own, _ := mem.ownSeat(ctx)
	if own == "" {
		return refused("this session holds no seat; a rename renames the seat it holds")
	}

	why := newSeatName(req.Name)
	if why != "" {
		return refused("%s", why)
	}

	if req.Name == own {
		return done(own + " keeps its name").with(renamedFields{Seat: own, Was: own})
	}

	unlock, err := mem.lockStore(ctx)
	if err != nil {
		return refused("%v", err)
	}
	defer unlock()

	registry, found, err := seat.LoadRegistry(mem.store())
	if err != nil {
		return refused("%v", err)
	}

	id, known := registry.IDOf(own)
	if !found || !known {
		return refused("this store keeps no seat ids yet, so %s cannot be renamed", own)
	}

	err = registry.Rename(id, req.Name)
	if errors.Is(err, seat.ErrNameTaken) {
		return refused("%s already names another seat", req.Name)
	}

	if err != nil {
		return refused("%v", err)
	}

	shelves, _, err := seat.LoadShelves(mem.store())
	if err != nil {
		return refused("%v", err)
	}

	other, carried := shelves.IDOf(req.Name)
	shelf := registry.Seats[id].Shelf

	if carried && other != shelf {
		return refused("%s already names a shelf", req.Name)
	}

	// A seat's own shelf carries its name and goes with it; a shared shelf keeps
	// its own.
	if shelves.NameOf(shelf) == own {
		err = shelves.Rename(shelf, req.Name)
		if err != nil {
			return refused("%v", err)
		}

		err = shelves.Save()
		if err != nil {
			return refused("%v", err)
		}
	}

	err = registry.Save()
	if err != nil {
		return refused("%v", err)
	}

	mem.forgetRegistry()
	mem.forgetShelves()

	return done(fmt.Sprintf("%s is now %s", own, req.Name)).with(renamedFields{Seat: req.Name, Was: own})
}
