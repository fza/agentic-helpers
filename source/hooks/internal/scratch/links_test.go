package scratch_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/scratch"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

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
			test.WriteFile(t, filepath.Join(root, ".tmp", "claude", "old", scratch.Marker), seatID+"\n")
			planting.Plant(t, filepath.Join(scratch.SeatDir(root, "main"), scratch.Marker), outside)

			scratch.Seat(root, "main", seatID)
			scratch.MoveSeat(root, "old", seatID, "main", "s-def456")

			if !test.Untouched(t, outside) {
				t.Error("a seat's marker should never be written through a planted link")
			}
		})
	}
}
