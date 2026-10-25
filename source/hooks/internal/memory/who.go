package memory

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
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
}

// seatRows is one row per seat of the store, grouped by shelf, freeing the
// claims nobody uses any more where free is set.
func (mem *memory) seatRows(ctx context.Context, free bool) []string {
	keys := seat.Known(mem.seatsDir())
	held := seat.Held(mem.seatsDir())

	var rows []seatRow

	for _, key := range keys {
		claim, found := held[key]
		row := seatRow{shelf: mem.shelfName(mem.shelfOfKey(key)), seat: mem.seatName(key), state: "free"}

		switch {
		case !found:
		case mem.alive(ctx, claim):
			row.state = fmt.Sprintf("%s pid %d", short(claim.SessionID), claim.PID)

			at, err := time.Parse(time.RFC3339Nano, claim.Claimed)
			if err == nil {
				row.state += "  since " + stamp(at)
			}
		case mem.freshReservation(claim):
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
		lines = append(lines, fmt.Sprintf("%s%s  shelf %s%s  %s", row.seat, strings.Repeat(" ", seats-len([]rune(row.seat))),
			row.shelf, strings.Repeat(" ", shelves-len([]rune(row.shelf))), row.state))
	}

	return lines
}
