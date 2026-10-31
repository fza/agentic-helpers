package scratch

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
)

// quietAge is how long nothing in a stray may change before it moves to
// `.tmp/keep/`: a session still writing there under the older layout keeps its
// paths through the day.
const quietAge = 24 * time.Hour

// keepSuffixLimit bounds the suffixes tried for a name `.tmp/keep/` holds.
const keepSuffixLimit = 1000

func legacyDir(root string) string {
	return filepath.Join(root, ".tmp", "claude")
}

func keepDir(root string) string {
	return filepath.Join(root, ".tmp", "keep")
}

// RelocateSeats moves each marked `.tmp/claude/<seat>/` whose marker names a
// seat owned accepts to `.tmp/seats/<seat>/`, then removes `.tmp/claude/`
// where that left it empty, and answers a line per move.
func RelocateSeats(root string, owned func(id string) bool) []string {
	entries, err := os.ReadDir(legacyDir(root))
	if err != nil {
		return nil
	}

	var said []string

	for _, entry := range entries {
		source := filepath.Join(legacyDir(root), entry.Name())

		id := markOf(source)
		if id == "" || !owned(id) {
			continue
		}

		said = appendLine(said, relocate(root, source, SeatDir(root, entry.Name())))
	}

	return appendLine(said, removeLegacy(root))
}

// Relocate moves each `.tmp/claude/<session-id>/` to `.tmp/seats/`, and every
// unmarked entry of `.tmp/claude/`, and every unmarked entry of `.tmp/seats/`
// not named for a session, to `.tmp/keep/` once nothing in it changed for a
// day. It then removes `.tmp/claude/` where that left it empty, and answers a
// line per move. A marked folder in `.tmp/claude/` is RelocateSeats' to move.
func Relocate(ctx context.Context, root string, now time.Time) []string {
	var said []string

	quiet := now.Add(-quietAge)

	legacy, _ := os.ReadDir(legacyDir(root))
	for _, entry := range legacy {
		if ctx.Err() != nil {
			return said
		}

		source := filepath.Join(legacyDir(root), entry.Name())

		switch {
		case entry.IsDir() && isSession(entry.Name()):
			said = appendLine(said, relocate(root, source, filepath.Join(seatsDir(root), entry.Name())))
		case markOf(source) == "":
			said = appendLine(said, keepStray(root, source, quiet))
		}
	}

	seats, _ := os.ReadDir(seatsDir(root))
	for _, entry := range seats {
		if ctx.Err() != nil {
			return said
		}

		source := filepath.Join(seatsDir(root), entry.Name())
		if entry.IsDir() && isSession(entry.Name()) || markOf(source) != "" {
			continue
		}

		said = appendLine(said, keepStray(root, source, quiet))
	}

	return appendLine(said, removeLegacy(root))
}

// relocate moves source to target where nothing holds target yet. A target
// already there is never merged into: what stays is said, and moves at a later
// session start once the target's own sweep took it.
func relocate(root string, source string, target string) string {
	_, err := os.Lstat(target)
	if err == nil {
		return fmt.Sprintf("Scratch: left %s where it is, %s is taken.", shown(root, source, true), shown(root, target, true))
	}

	if !errors.Is(err, fs.ErrNotExist) {
		return ""
	}

	err = safefs.MkdirAll(root, seatsDir(root), 0o700)
	if err != nil {
		return ""
	}

	err = safefs.Rename(root, source, target)
	if err != nil {
		return ""
	}

	return movedLine(root, source, target)
}

// A stray holding more than the walk reads stays where it is.
func keepStray(root string, source string, quiet time.Time) string {
	newest, dated := newestIn(source)
	if !dated || !newest.Before(quiet) {
		return ""
	}

	return toKeep(root, source)
}

func toKeep(root string, source string) string {
	err := safefs.MkdirAll(root, keepDir(root), 0o700)
	if err != nil {
		return ""
	}

	name := filepath.Base(source)

	for suffix := 1; suffix <= keepSuffixLimit; suffix++ {
		candidate := name
		if suffix > 1 {
			candidate = fmt.Sprintf("%s-%d", name, suffix)
		}

		target := filepath.Join(keepDir(root), candidate)

		_, err = os.Lstat(target)
		if !errors.Is(err, fs.ErrNotExist) {
			continue
		}

		// The check above only skips the names known taken; the exclusive move
		// is what keeps a name filled since from being replaced.
		err = safefs.RenameExclusive(root, source, target)
		if errors.Is(err, fs.ErrExist) {
			continue
		}

		if err != nil {
			return ""
		}

		return movedLine(root, source, target)
	}

	return ""
}

// The removal itself refuses a folder holding anything; a link at the name is
// never removed.
func removeLegacy(root string) string {
	dir := legacyDir(root)

	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return ""
	}

	held, name, err := safefs.Parent(root, dir)
	if err != nil {
		return ""
	}

	defer held.Close()

	err = held.Remove(name)
	if err != nil {
		return ""
	}

	return "Scratch: removed the empty .tmp/claude/"
}

func movedLine(root string, source string, target string) string {
	info, err := os.Lstat(target)
	folder := err == nil && info.IsDir()

	return fmt.Sprintf("Scratch: moved %s to %s", shown(root, source, folder), shown(root, target, folder))
}

func shown(root string, path string, folder bool) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		relative = path
	}

	if folder {
		return filepath.ToSlash(relative) + "/"
	}

	return filepath.ToSlash(relative)
}

func appendLine(said []string, line string) []string {
	if line == "" {
		return said
	}

	return append(said, line)
}
