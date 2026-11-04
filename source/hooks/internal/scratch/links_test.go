package scratch_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/scratch"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

// linkedScratch is a project whose committed `.tmp` or `.tmp/seats` is a
// link to a folder outside it, as one pointing at Claude Code's
// file-history: session-named folders, a week old, and a marked seat
// folder, each holding a file.
func linkedScratch(t *testing.T, link string) (string, []string) {
	t.Helper()

	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "file-history")
	seats := outside

	if link == ".tmp" {
		seats = filepath.Join(outside, "seats")
	}

	var held []string

	for _, name := range []string{test.Session, test.OtherSession, "main"} {
		dir := filepath.Join(seats, name)
		test.WriteFile(t, filepath.Join(dir, "kept.txt"), "kept")

		if name == "main" {
			test.WriteFile(t, filepath.Join(dir, scratch.Marker), seatID+"\n")
		}

		test.Age(t, filepath.Join(dir, "kept.txt"), 30*24*time.Hour)
		test.Age(t, dir, 30*24*time.Hour)
		held = append(held, filepath.Join(dir, "kept.txt"))
	}

	at := filepath.Join(root, filepath.FromSlash(link))

	err := os.MkdirAll(filepath.Dir(at), 0o755)
	if err == nil {
		err = os.Symlink(outside, at)
	}

	if err != nil {
		t.Fatalf("planting the link: %v", err)
	}

	return root, held
}

func TestTheSweepNeverRemovesThroughACommittedLink(t *testing.T) {
	sweeps := []struct {
		name  string
		sweep func(t *testing.T, root string)
	}{
		{"the session's end", func(t *testing.T, root string) { run(t, root, "end", payload(test.Session)) }},
		{"the session start's reap", func(t *testing.T, root string) { run(t, root, "start", payload(test.Session)) }},
		{"the seat reap", func(_ *testing.T, root string) {
			scratch.ReapSeats(context.Background(), root, func(string) bool { return true }, time.Now())
		}},
		{"the seat's release", func(_ *testing.T, root string) {
			scratch.RemoveSeat(context.Background(), root, "main", seatID)
		}},
	}

	for _, link := range []string{".tmp", ".tmp/seats"} {
		for _, sweep := range sweeps {
			t.Run(link+" "+sweep.name, func(t *testing.T) {
				root, held := linkedScratch(t, link)

				sweep.sweep(t, root)

				for _, kept := range held {
					if !test.Exists(t, kept) {
						t.Errorf("a sweep should never remove what a committed %s link leads to", link)
					}
				}
			})
		}
	}
}

func TestEndLeavesAFileNamedForTheSession(t *testing.T) {
	root := project(t)
	held := filepath.Join(root, ".tmp", "seats", test.Session)
	test.WriteFile(t, held, "a file, no session folder")

	run(t, root, "end", payload(test.Session))

	if !test.Exists(t, held) {
		t.Error("the session's end should remove a folder alone, never a file at its name")
	}
}

func TestTheMoveIntoASeatNeverRemovesThroughACommittedLink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "file-history")
	emptied := filepath.Join(outside, test.Session)

	err := os.MkdirAll(emptied, 0o755)
	if err == nil {
		err = os.MkdirAll(filepath.Join(root, ".tmp"), 0o755)
	}

	if err == nil {
		err = os.Symlink(outside, filepath.Join(root, ".tmp", "claude"))
	}

	if err != nil {
		t.Fatalf("planting the link: %v", err)
	}

	scratch.Migrate(root, func(string) (string, string, bool) { return "main", seatID, true })

	if !test.Exists(t, emptied) {
		t.Error("the move into a seat's folder should never remove a folder a committed .tmp/claude link leads to")
	}
}

func TestACommittedScratchLinkGetsNoWriteThroughIt(t *testing.T) {
	root := t.TempDir()
	outside := test.Outside(t)

	err := os.Symlink(outside, filepath.Join(root, ".tmp"))
	if err != nil {
		t.Fatalf("planting the link: %v", err)
	}

	scratch.Seat(root, "main", seatID)

	if !test.Untouched(t, outside) {
		t.Error("a seat's folder should never be made through a committed .tmp link")
	}
}

func TestASeatsMarkerNeverLandsAtAPlantedLink(t *testing.T) {
	for _, planting := range test.Plantings() {
		t.Run(planting.Name, func(t *testing.T) {
			root := t.TempDir()
			outside := test.Outside(t)
			test.WriteFile(t, filepath.Join(root, ".tmp", "seats", "old", scratch.Marker), seatID+"\n")
			planting.Plant(t, filepath.Join(scratch.SeatDir(root, "main"), scratch.Marker), outside)

			scratch.Seat(root, "main", seatID)
			scratch.MoveSeat(root, "old", seatID, "main", "s-def456")

			if !test.Untouched(t, outside) {
				t.Error("a seat's marker should never be written through a planted link")
			}
		})
	}
}
