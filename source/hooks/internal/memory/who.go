package memory

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
)

// who lists every live seated session on the machine, in every project's
// store. It only reads: it frees no seat, warms nothing and migrates no store,
// and it runs from anywhere, since it reads every project rather than the
// caller's.
func (mem *memory) who(ctx context.Context, _ request) result {
	root := mem.root()

	type row struct {
		project, seat, session string
		pid                    int
		since                  string
	}

	var rows []row

	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		// A migration's backup copies a store's seats; it is no store of its own.
		if err == nil && entry.IsDir() && entry.Name() == "backup" {
			return filepath.SkipDir
		}

		if err != nil || !entry.IsDir() || entry.Name() != "seats" {
			return nil
		}

		store := filepath.Dir(path)

		project, err := filepath.Rel(root, store)
		if err != nil || project == "." {
			return filepath.SkipDir
		}

		registry, found, _ := seat.LoadRegistry(store)

		for key, claim := range seat.Held(path) {
			if !seat.Alive(ctx, mem.env.Processes, mem.env.OwnerName, claim) {
				continue
			}

			name := key
			if found {
				name = registry.NameOf(key)
			}

			since := claim.Claimed
			if at, parsed := time.Parse(time.RFC3339Nano, claim.Claimed); parsed == nil {
				since = stamp(at)
			}

			rows = append(rows, row{
				project: filepath.ToSlash(project), seat: name, session: short(claim.SessionID), pid: claim.PID, since: since,
			})
		}

		return filepath.SkipDir
	})

	if len(rows) == 0 {
		return done("no live seated session on this machine")
	}

	slices.SortFunc(rows, func(a, b row) int {
		return strings.Compare(a.project+"\x00"+a.seat, b.project+"\x00"+b.seat)
	})

	projects, seats := 0, 0
	for _, held := range rows {
		projects, seats = max(projects, len(held.project)), max(seats, len([]rune(held.seat)))
	}

	lines := make([]string, 0, len(rows))
	for _, held := range rows {
		text := fmt.Sprintf("%-*s  %s%s  %s  pid %d", projects, held.project, held.seat,
			strings.Repeat(" ", seats-len([]rune(held.seat))), held.session, held.pid)
		if held.since != "" {
			text += "  since " + held.since
		}

		lines = append(lines, text)
	}

	return done(strings.Join(lines, "\n"))
}
