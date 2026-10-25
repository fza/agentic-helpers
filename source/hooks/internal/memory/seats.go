package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/project"
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
			view = holderView{
				Seat: name, SessionID: held.SessionID, PID: held.PID, Started: held.Started, Claimed: held.Claimed,
				TranscriptPath: held.TranscriptPath, Alive: mem.alive(ctx, held),
			}
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
		view = holderView{
			Seat: req.Name, SessionID: held.SessionID, PID: held.PID, Started: held.Started, Claimed: held.Claimed,
			TranscriptPath: held.TranscriptPath, Alive: mem.alive(ctx, held),
		}
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
	why := newSeatName(req.Name)
	if why != "" {
		return refused("%s", why)
	}

	owner := seat.Owner(ctx, mem.env.Processes, mem.env.OwnerName)

	current, _ := mem.ownSeat(ctx)
	if current != "" && current != req.Name {
		return refused("this session already holds %s; release that seat before claiming %s", current, req.Name)
	}

	if !mem.storeExists() {
		err := mem.initStore()
		if err != nil {
			return refused("%v", err)
		}
	}

	dir := mem.seatsDir()
	key := mem.seatKey(req.Name)
	existing, found := seat.Read(dir, key)
	previous, hadPrevious := existing, found

	if !hadPrevious {
		previous, hadPrevious = seat.ReadLast(dir, key)
	}

	// A running holder may be mid-work, and nothing tells a stuck one from a busy
	// one, so no claim ever takes its seat.
	if found && current != req.Name && mem.alive(ctx, existing) {
		return refused("%s is held by session %s (pid %d), still running; its holder releases it with `%s release`, or the seat frees when that session ends",
			req.Name, short(existing.SessionID), existing.PID, self)
	}

	if current == req.Name {
		previous, hadPrevious = seat.ReadLast(dir, key)
	} else if found {
		err := seat.Remove(dir, key)
		if err != nil {
			return refused("%v", err)
		}
	}

	held := seat.Claim{
		SessionID: mem.env.SessionID, PID: owner, Claimed: mem.now().Format(time.RFC3339Nano),
	}

	if current == req.Name {
		held = existing
	}

	// Rewritten on every claim, a session's own again included, so a claim
	// holding `started` in an earlier release's locale text gains an instant.
	started, running := mem.env.Processes.StartTime(ctx, owner)
	if running {
		held.Started = seat.FormatStarted(started)
	}

	key, err := mem.ensureSeat(ctx, req.Name)
	if err != nil {
		return refused("%v", err)
	}

	err = seat.Write(dir, key, held)
	if err != nil {
		return refused("%v", err)
	}

	folded, err := mem.load(ctx)
	if err != nil {
		return refused("%v", err)
	}

	here := mem.checkout(ctx)

	lines := []string{fmt.Sprintf("%s: %s (pid %d)", req.Name, short(held.SessionID), held.PID)}

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

	return done(strings.Join(lines, "\n"))
}

// carryForwardHint points a seat at the carry-forward file it still has. The
// file is the agent's to fold and delete; no other file under `.memory/` is
// ever touched.
func (mem *memory) carryForwardHint(ctx context.Context, seatName string) string {
	root := project.Root(ctx, mem.env.Base, mem.env.Checkout)
	relative := filepath.Join(".memory", "carryforward", seatName+"-memory.md")

	_, err := os.Stat(filepath.Join(root, relative))
	if err != nil {
		return ""
	}

	return fmt.Sprintf("%s still holds this seat's carry-forward: read it once, file each finding with %s new, "+
		"then delete that one file; no other file under .memory/ is touched (agentic-helpers:agent-memory)",
		relative, self)
}

func (mem *memory) newestOwn(folded *state, seatName string) time.Time {
	var newest time.Time

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

	held, found := seat.Read(mem.seatsDir(), key)
	if !found {
		return done(name + " is free")
	}

	if name != own && mem.alive(ctx, held) {
		return refused("%s is held by session %s (pid %d); only its holder releases it", name, short(held.SessionID),
			held.PID)
	}

	err := seat.Remove(mem.seatsDir(), key)
	if err != nil {
		return refused("%v", err)
	}

	return done("released " + name)
}

func (mem *memory) sweep(ctx context.Context) []string {
	var rows []string

	held := seat.Held(mem.seatsDir())

	for _, key := range seat.Known(mem.seatsDir()) {
		claim, found := held[key]
		name := mem.seatName(key)

		switch {
		case !found:
			rows = append(rows, name+"  free")
		case mem.alive(ctx, claim):
			rows = append(rows, fmt.Sprintf("%s  %s  pid %d", name, short(claim.SessionID), claim.PID))
		default:
			_ = seat.Remove(mem.seatsDir(), key)
			rows = append(rows, fmt.Sprintf("%s  free (holder %s gone, freed)", name, short(claim.SessionID)))
		}
	}

	return rows
}

func (mem *memory) sweepCommand(ctx context.Context, _ request) result {
	rows := mem.sweep(ctx)
	if len(rows) == 0 {
		return done("no seat in this project")
	}

	return done(strings.Join(rows, "\n"))
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
		return done(own + " keeps its name")
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

	err = registry.Save()
	if err != nil {
		return refused("%v", err)
	}

	mem.forgetRegistry()

	return done(fmt.Sprintf("%s is now %s", own, req.Name))
}
