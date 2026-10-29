package memory

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
)

// who lists the seats of the current project, held, reserved or free, with
// its shelf, and frees the project's claims whose holder is gone and its
// reservations nobody took over. With --all it adds every other project's
// seats on the machine under a heading naming the project, and never writes
// to another project. A free seat nothing was filed by or used from in
// usedWindow is counted rather than listed, unless --unused. It runs from
// anywhere and migrates no store.
func (mem *memory) who(ctx context.Context, req request) result {
	if req.JSON {
		return mem.whoJSON(ctx, req)
	}

	var blocks []string

	here := ""

	if mem.storeExists() {
		here = mem.key

		rows := seatLines(mem.seatRecords(ctx, mem.mayFree(ctx)), req.Unused, self+" who --unused")
		if len(rows) == 0 {
			rows = []string{"no seat in this project"}
		}

		blocks = append(blocks, strings.Join(rows, "\n"))
	} else {
		blocks = append(blocks, "no seat in this project")
	}

	if !req.All {
		return done(blocks[0])
	}

	for _, held := range mem.otherStores(here) {
		other := mem.sibling(held.ID)

		rows := seatLines(public(other.seatRecords(ctx, false)), req.Unused, self+" who --all --unused")
		if len(rows) > 0 {
			blocks = append(blocks, held.Name()+" ("+held.ID+"):\n"+indent(rows))
		}
	}

	return done(strings.Join(blocks, "\n\n"))
}

// whoJSON is who's answer for a program: every seat of this project, and
// with --all every other project's, each naming its project.
func (mem *memory) whoJSON(ctx context.Context, req request) result {
	var seats []jsonSeat

	here := ""

	if mem.storeExists() {
		here = mem.key

		project := ""
		if req.All {
			project = mem.storeName()
		}

		for _, row := range mem.seatRecords(ctx, mem.mayFree(ctx)) {
			seats = append(seats, seatJSON(row, project))
		}
	}

	if req.All {
		for _, held := range mem.otherStores(here) {
			other := mem.sibling(held.ID)
			for _, row := range public(other.seatRecords(ctx, false)) {
				seats = append(seats, seatJSON(row, held.Name()))
			}
		}
	}

	return jsonAnswer(jsonWho{Seats: seats})
}

func indent(rows []string) string {
	indented := make([]string, 0, len(rows))
	for _, row := range rows {
		indented = append(indented, "  "+row)
	}

	return strings.Join(indented, "\n")
}

// mayFree is whether this call may free the project's dead claims: a write,
// so never on a store of a format this release cannot write.
func (mem *memory) mayFree(_ context.Context) bool {
	return !mem.outsideGit && mem.unreadable == ""
}

// otherStores is every store on the machine holding memory seats, but the
// one named here, by id.
func (mem *memory) otherStores(here string) []home.Identity {
	var others []home.Identity

	for _, identity := range mem.env.Roots.Identities() {
		if identity.ID == here || !mem.sibling(identity.ID).holdsSeats() {
			continue
		}

		others = append(others, identity)
	}

	slices.SortFunc(others, func(left, right home.Identity) int { return strings.Compare(left.ID, right.ID) })

	return others
}

func (mem *memory) holdsSeats() bool {
	info, err := os.Stat(mem.seatsDir())

	return err == nil && info.IsDir()
}

// storeIDs is the id of every store on the machine keeping memory.
func (mem *memory) storeIDs() []string {
	var ids []string

	for id := range mem.env.Roots.Identities() {
		info, err := os.Stat(mem.sibling(id).store())
		if err == nil && info.IsDir() {
			ids = append(ids, id)
		}
	}

	slices.Sort(ids)

	return ids
}

// storeName is how the own store reads to a person.
func (mem *memory) storeName() string {
	identity, err := mem.env.Roots.Identity(mem.key)
	if err != nil {
		return mem.key
	}

	return identity.Name()
}

type seatRow struct {
	shelf, seat, state string
	usage              seatUse
	// status is held, reserved or free, and holder the claim of a held or
	// reserved seat; freed is a free seat whose gone holder this call freed.
	status  string
	holder  *seat.Claim
	freed   bool
	private bool
}

// unused is a free seat nothing was filed by or used from within usedWindow.
// A seat this call freed is listed once, so the freeing is seen.
func (row seatRow) unused() bool {
	return row.status == seatFree && !row.freed && row.usage.filed == 0 && row.usage.used == 0
}

const (
	seatHeld     = "held"
	seatReserved = "reserved"
	seatFree     = "free"
)

// seatUse is how many entries a seat filed within usedWindow, and how many of
// all its entries a use warmed within it.
type seatUse struct {
	filed, used int
}

// seatUsage is every seat's use, by seat key, read from the log and the heat
// cache without a lock; a log that cannot be read counts nothing.
func (mem *memory) seatUsage(ctx context.Context) map[string]seatUse {
	usage := map[string]seatUse{}

	_, err := os.Stat(mem.logDir())
	if err != nil {
		return usage
	}

	log, err := mem.openLog()
	if err != nil {
		return usage
	}

	snapshot, err := log.Snapshot(ctx)
	if err != nil {
		return usage
	}

	defer func() { _ = snapshot.Close() }()

	folded, err := fold(snapshot, func(key string) string { return key })
	if err != nil {
		return usage
	}

	heats := mem.heats()
	since := mem.now().Add(-usedWindow)

	for _, held := range folded.entryList() {
		counted := usage[held.seatKey]

		if held.filed.After(since) {
			counted.filed++
		}

		if heats[held.id].Used.After(since) {
			counted.used++
		}

		usage[held.seatKey] = counted
	}

	return usage
}

// seatRecords is every seat of the store, sorted by shelf and seat, freeing
// the claims nobody uses any more where free is set.
func (mem *memory) seatRecords(ctx context.Context, free bool) []seatRow {
	keys := seat.Known(mem.seatsDir())
	held := seat.Held(mem.seatsDir())

	var rows []seatRow

	usage := mem.seatUsage(ctx)
	registry, _ := mem.registry()

	for _, key := range keys {
		claim, found := held[key]
		row := seatRow{shelf: mem.shelfName(mem.shelfOfKey(key)), seat: mem.seatName(key), state: "free", usage: usage[key],
			status: seatFree, private: registry.Seats[key].Private}

		switch {
		case !found:
		case mem.alive(ctx, claim):
			row.status, row.holder = seatHeld, &claim
			row.state = fmt.Sprintf("%s pid %d", short(claim.SessionID), claim.PID)

			at, err := time.Parse(time.RFC3339Nano, claim.Claimed)
			if err == nil {
				row.state += "  since " + stamp(at)
			}
		case mem.freshReservation(claim):
			row.status, row.holder = seatReserved, &claim
			row.state = "reserved " + short(claim.SessionID)
		case free:
			mem.sweep(ctx, key, claim)
			row.state, row.freed = fmt.Sprintf("free (holder %s gone, freed)", short(claim.SessionID)), true
		}

		rows = append(rows, row)
	}

	slices.SortFunc(rows, func(a, b seatRow) int {
		return cmp.Or(strings.Compare(a.shelf, b.shelf), strings.Compare(a.seat, b.seat))
	})

	return rows
}

// sweep frees the seat filed under key from the claim a gone holder left,
// under the seats lock, so a claim written since the holder was judged gone
// stays.
func (mem *memory) sweep(ctx context.Context, key string, gone seat.Claim) {
	dir := mem.seatsDir()

	unlock, err := seat.Lock(ctx, dir)
	if err != nil {
		return
	}
	defer unlock()

	current, found := seat.Read(dir, key)
	if found && current == gone {
		_ = seat.Remove(dir, key)
	}
}

// public is rows without the private seats, which only their own project
// lists.
func public(rows []seatRow) []seatRow {
	return slices.DeleteFunc(slices.Clone(rows), func(row seatRow) bool { return row.private })
}

// seatLines is one line per seat of rows, grouped by shelf as rows come, the
// unused ones left out and counted in a closing line naming the command that
// lists them, unless every seat is asked for.
func seatLines(rows []seatRow, every bool, listsUnused string) []string {
	unused := 0

	if !every {
		rows = slices.DeleteFunc(slices.Clone(rows), func(row seatRow) bool {
			if row.unused() {
				unused++

				return true
			}

			return false
		})
	}

	seats := 0
	for _, row := range rows {
		seats = max(seats, len([]rune(row.seat)))
	}

	shelves := 0
	for _, row := range rows {
		shelves = max(shelves, len([]rune(row.shelf)))
	}

	lines := make([]string, 0, len(rows)+1)
	for _, row := range rows {
		line := fmt.Sprintf("%s%s  shelf %s%s  %s  filed %d / used %d (14d)", row.seat,
			strings.Repeat(" ", seats-len([]rune(row.seat))), row.shelf, strings.Repeat(" ", shelves-len([]rune(row.shelf))),
			row.state, row.usage.filed, row.usage.used)
		if row.private {
			line += "  private"
		}

		lines = append(lines, line)
	}

	if unused > 0 {
		lines = append(lines, "+"+plural(unused, "unused seat")+": "+listsUnused)
	}

	return lines
}
