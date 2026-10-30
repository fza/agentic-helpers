package scratch

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Marker is the file inside a seat's folder naming the id of the seat it
// belongs to. The marker, never the name, is what lets a folder be swept: a
// folder named like a seat but carrying no marker is somebody's keep.
const Marker = ".agentic-scratch"

// walkLimit bounds what the reap reads to date one folder; a folder holding
// more is left for the agent to clear.
const walkLimit = 10000

// SeatDir is a seat's scratch folder under root.
func SeatDir(root string, name string) string {
	return filepath.Join(sessionsDir(root), name)
}

// Seat makes the seat's folder under a root carrying `.tmp/`, marked for the
// seat id, and answers the line a claim prints, or nothing where the root has
// no `.tmp/`. A folder already there without the seat's marker is left as it
// is, and the line says it is a keep.
func Seat(root string, name string, id string) string {
	if !isDir(filepath.Join(root, ".tmp")) {
		return ""
	}

	line := "scratch: " + filepath.Join(".tmp", "claude", name) + "/"
	dir := SeatDir(root, name)

	_, err := os.Lstat(dir)
	if err == nil {
		if markOf(dir) == id {
			return line
		}

		return line + " (a keep, never swept)"
	}

	if !errors.Is(err, fs.ErrNotExist) {
		return ""
	}

	err = os.MkdirAll(dir, 0o700)
	if err != nil {
		return ""
	}

	err = os.WriteFile(filepath.Join(dir, Marker), []byte(id+"\n"), 0o600)
	if err != nil {
		return ""
	}

	return line
}

// RemoveSeat removes the seat's folder where its marker names the seat, and
// answers whether it went.
func RemoveSeat(ctx context.Context, root string, name string, id string) bool {
	dir := SeatDir(root, name)
	if id == "" || markOf(dir) != id {
		return false
	}

	return remove(ctx, dir)
}

// MoveSeat carries a renamed or moved seat's folder to its new name, marked
// for the seat it now belongs to, where the folder is the old seat's own and
// nothing holds the new name yet: os.Rename refuses a directory there.
func MoveSeat(root string, from string, fromID string, to string, toID string) {
	source, target := SeatDir(root, from), SeatDir(root, to)
	if from == to || fromID == "" || toID == "" || markOf(source) != fromID {
		return
	}

	err := os.Rename(source, target)
	if err != nil || fromID == toID {
		return
	}

	_ = os.WriteFile(filepath.Join(target, Marker), []byte(toID+"\n"), 0o600)
}

// ReapSeats removes every marked folder whose marker names a seat owned
// accepts and whose newest file is older than a week, the backstop for a seat
// nobody releases, and answers how many went.
func ReapSeats(ctx context.Context, root string, owned func(id string) bool, now time.Time) int {
	entries, err := os.ReadDir(sessionsDir(root))
	if err != nil {
		return 0
	}

	gone := 0
	stale := now.Add(-orphanAge)

	for _, entry := range entries {
		if ctx.Err() != nil {
			break
		}

		dir := filepath.Join(sessionsDir(root), entry.Name())
		if !entry.IsDir() {
			continue
		}

		id := markOf(dir)
		if id == "" || !owned(id) {
			continue
		}

		newest, dated := newestIn(dir)
		if !dated || !newest.Before(stale) {
			continue
		}

		if remove(ctx, dir) {
			gone++
		}
	}

	return gone
}

// Migrate moves each `.tmp/claude/<session-id>/` folder whose session seatOf
// maps to a seat into that seat's folder, a name already there going into a
// subfolder named by the session id's first eight characters, and removes
// the emptied session folder. A folder no seat claims stays, and is reaped on
// the session-id rule. Loose files at the root are never touched.
func Migrate(root string, seatOf func(session string) (name string, id string, found bool)) int {
	entries, err := os.ReadDir(sessionsDir(root))
	if err != nil {
		return 0
	}

	moved := 0

	for _, entry := range entries {
		if !entry.IsDir() || !isSession(entry.Name()) {
			continue
		}

		name, id, found := seatOf(entry.Name())
		if !found {
			continue
		}

		// A keep at the seat's name takes nothing: what moved into it would
		// never be swept.
		Seat(root, name, id)

		if markOf(SeatDir(root, name)) != id {
			continue
		}

		if moveInto(filepath.Join(sessionsDir(root), entry.Name()), SeatDir(root, name), entry.Name()[:8]) {
			moved++
		}
	}

	return moved
}

func moveInto(source string, target string, clash string) bool {
	entries, err := os.ReadDir(source)
	if err != nil {
		return false
	}

	for _, entry := range entries {
		at := filepath.Join(target, entry.Name())

		_, err = os.Lstat(at)
		if err == nil {
			err = os.MkdirAll(filepath.Join(target, clash), 0o700)
			if err != nil {
				continue
			}

			at = filepath.Join(target, clash, entry.Name())

			_, err = os.Lstat(at)
			if err == nil {
				continue
			}
		}

		_ = os.Rename(filepath.Join(source, entry.Name()), at)
	}

	return os.Remove(source) == nil
}

// markOf is the seat id a folder's marker names, or nothing where the folder
// is a link, or holds no marker as a plain file.
func markOf(dir string) string {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return ""
	}

	marker := filepath.Join(dir, Marker)

	info, err = os.Lstat(marker)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}

	data, err := os.ReadFile(marker)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(data))
}

// newestIn is the latest modification time of anything inside dir, the
// marker aside, or the folder's own where it holds nothing else; false where
// the folder holds more than the walk reads.
func newestIn(dir string) (time.Time, bool) {
	var newest time.Time

	info, err := os.Lstat(dir)
	if err != nil {
		return newest, false
	}

	newest = info.ModTime()
	seen := 0

	err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		seen++
		if seen > walkLimit {
			return fs.SkipAll
		}

		if path == dir || entry.Name() == Marker && filepath.Dir(path) == dir {
			return nil
		}

		held, err := entry.Info()
		if err != nil {
			return err
		}

		if held.ModTime().After(newest) {
			newest = held.ModTime()
		}

		return nil
	})

	return newest, err == nil && seen <= walkLimit
}
