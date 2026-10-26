package memory

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
	"github.com/fza/agentic-helpers/source/hooks/internal/writers"
)

// who lists every seat of the current project, held, reserved or free, with
// its shelf, and frees the project's claims whose holder is gone and its
// reservations nobody took over. With --all it adds every other project's
// seats on the machine under a heading naming the project, and never writes
// to another project. It runs from anywhere and migrates no store.
func (mem *memory) who(ctx context.Context, req request) result {
	if req.JSON {
		return mem.whoJSON(ctx, req)
	}

	var blocks []string

	here := ""

	if mem.key != "" && mem.storeExists() {
		here = mem.key

		rows := mem.seatRows(ctx, mem.mayFree(ctx))
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

	for _, key := range mem.storeKeys() {
		if key == here {
			continue
		}

		other := &memory{env: mem.env, streams: mem.streams, key: key}

		rows := other.seatRows(ctx, false)
		if len(rows) > 0 {
			blocks = append(blocks, key+":\n"+indent(rows))
		}
	}

	return done(strings.Join(blocks, "\n\n"))
}

// whoJSON is who's answer for a program: every seat of this project, and
// with --all every other project's, each naming its project.
func (mem *memory) whoJSON(ctx context.Context, req request) result {
	seats := []jsonSeat{}
	here := ""

	if mem.key != "" && mem.storeExists() {
		here = mem.key

		project := ""
		if req.All {
			project = mem.key
		}

		for _, row := range mem.seatRecords(ctx, mem.mayFree(ctx)) {
			seats = append(seats, seatJSON(row, project))
		}
	}

	if req.All {
		for _, key := range mem.storeKeys() {
			if key == here {
				continue
			}

			other := &memory{env: mem.env, streams: mem.streams, key: key}
			for _, row := range other.seatRecords(ctx, false) {
				seats = append(seats, seatJSON(row, key))
			}
		}
	}

	return jsonAnswer(map[string]any{"seats": seats})
}

func indent(rows []string) string {
	indented := make([]string, 0, len(rows))
	for _, row := range rows {
		indented = append(indented, "  "+row)
	}

	return strings.Join(indented, "\n")
}

// mayFree is whether this call may free the project's dead claims: a write,
// so only a checkout listed for the store, or one of a project nobody claimed
// yet, frees any. Nothing claims a project for who.
func (mem *memory) mayFree(ctx context.Context) bool {
	if mem.outsideGit || mem.unreadable != "" {
		return false
	}

	if !mem.gitKeyed {
		return true
	}

	err := mem.writersList().Check(ctx, mem.env.Base)

	return err == nil || errors.Is(err, writers.ErrUnclaimed)
}

// storeKeys is the key of every store on the machine, a migration's backups
// aside, sorted.
func (mem *memory) storeKeys() []string {
	root := mem.root()

	var keys []string

	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && entry.IsDir() && entry.Name() == "backup" {
			return filepath.SkipDir
		}

		if err != nil || !entry.IsDir() || entry.Name() != "seats" {
			return nil
		}

		key, err := filepath.Rel(root, filepath.Dir(path))
		if err == nil && key != "." {
			keys = append(keys, filepath.ToSlash(key))
		}

		return filepath.SkipDir
	})

	slices.Sort(keys)

	return keys
}

type seatRow struct {
	shelf, seat, state string
	usage              seatUse
	// status is held, reserved or free, and holder the claim of a held or
	// reserved seat.
	status string
	holder *seat.Claim
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

	for _, key := range keys {
		claim, found := held[key]
		row := seatRow{shelf: mem.shelfName(mem.shelfOfKey(key)), seat: mem.seatName(key), state: "free", usage: usage[key],
			status: seatFree}

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
			_ = seat.Remove(mem.seatsDir(), key)
			row.state = fmt.Sprintf("free (holder %s gone, freed)", short(claim.SessionID))
		}

		rows = append(rows, row)
	}

	slices.SortFunc(rows, func(a, b seatRow) int {
		return cmp.Or(strings.Compare(a.shelf, b.shelf), strings.Compare(a.seat, b.seat))
	})

	return rows
}

// seatRows is one line per seat of the store, grouped by shelf, freeing the
// claims nobody uses any more where free is set.
func (mem *memory) seatRows(ctx context.Context, free bool) []string {
	rows := mem.seatRecords(ctx, free)

	seats := 0
	for _, row := range rows {
		seats = max(seats, len([]rune(row.seat)))
	}

	shelves := 0
	for _, row := range rows {
		shelves = max(shelves, len([]rune(row.shelf)))
	}

	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, fmt.Sprintf("%s%s  shelf %s%s  %s  filed %d / used %d (14d)", row.seat,
			strings.Repeat(" ", seats-len([]rune(row.seat))), row.shelf, strings.Repeat(" ", shelves-len([]rune(row.shelf))),
			row.state, row.usage.filed, row.usage.used))
	}

	return lines
}
