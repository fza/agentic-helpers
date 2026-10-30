package scratch_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/scratch"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const seatID = "s-abc123"

// seatFolder is a seat's folder claimed under root, its files aged by age.
func seatFolder(t *testing.T, root string, age time.Duration) string {
	t.Helper()

	scratch.Seat(root, "main", seatID)

	dir := scratch.SeatDir(root, "main")
	test.WriteFile(t, filepath.Join(dir, "probe.py"), "print(1)\n")

	if age > 0 {
		test.Age(t, filepath.Join(dir, "probe.py"), age)
		test.Age(t, filepath.Join(dir, scratch.Marker), age)
		test.Age(t, dir, age)
	}

	return dir
}

func TestAClaimedSeatGetsItsMarkedFolder(t *testing.T) {
	root := project(t)

	line := scratch.Seat(root, "main", seatID)
	if line != "scratch: .tmp/claude/main/" {
		t.Errorf("the claim should name the seat's folder, got %q", line)
	}

	marker, err := os.ReadFile(filepath.Join(scratch.SeatDir(root, "main"), scratch.Marker))
	if err != nil || strings.TrimSpace(string(marker)) != seatID {
		t.Errorf("the folder should carry a marker naming the seat's id, got %q, %v", marker, err)
	}

	if scratch.Seat(root, "main", seatID) != line {
		t.Error("a second claim of the seat should name the same folder")
	}
}

func TestAProjectWithoutTmpGetsNoSeatFolder(t *testing.T) {
	root := t.TempDir()

	if scratch.Seat(root, "main", seatID) != "" {
		t.Error("a project without .tmp/ should hear of no scratch folder")
	}

	if test.Exists(t, filepath.Join(root, ".tmp")) {
		t.Error("a project without .tmp/ should get none")
	}
}

func TestAFolderWithoutTheSeatsMarkerIsAKeep(t *testing.T) {
	for name, marker := range map[string]string{"no marker": "", "another seat's marker": "s-zzz999\n"} {
		t.Run(name, func(t *testing.T) {
			root := project(t)
			dir := scratch.SeatDir(root, "main")
			test.WriteFile(t, filepath.Join(dir, "keep.sh"), "echo keep\n")

			if marker != "" {
				test.WriteFile(t, filepath.Join(dir, scratch.Marker), marker)
			}

			test.Age(t, filepath.Join(dir, "keep.sh"), 30*24*time.Hour)
			test.Age(t, dir, 30*24*time.Hour)
			before := test.Tree(t, root)

			if !strings.HasSuffix(scratch.Seat(root, "main", seatID), "(a keep, never swept)") {
				t.Error("the claim should name a folder it did not mark as a keep")
			}

			if scratch.RemoveSeat(t.Context(), root, "main", seatID) {
				t.Error("a release should not remove a keep")
			}

			if scratch.ReapSeats(t.Context(), root, func(id string) bool { return id == seatID }, time.Now()) != 0 {
				t.Error("the reap should not remove a keep of any age")
			}

			if test.Tree(t, root) != before {
				t.Error("a keep should be left byte-identical")
			}
		})
	}
}

func TestReleaseRemovesOnlyTheSeatsOwnFolder(t *testing.T) {
	root := project(t)
	dir := seatFolder(t, root, 0)

	if scratch.RemoveSeat(t.Context(), root, "main", "s-zzz999") {
		t.Error("another seat's release should not remove the folder")
	}

	if !scratch.RemoveSeat(t.Context(), root, "main", seatID) || test.Exists(t, dir) {
		t.Error("the seat's release should remove its folder")
	}
}

func TestTheReapTakesOnlyAWeekIdleFolderOfAKnownSeat(t *testing.T) {
	cases := []struct {
		name  string
		age   time.Duration
		ids   map[string]bool
		fresh bool
		gone  bool
	}{
		{name: "idle a week", age: week + time.Hour, ids: map[string]bool{seatID: true}, gone: true},
		{name: "used within the week", age: week - time.Hour, ids: map[string]bool{seatID: true}},
		{name: "one fresh file deep inside", age: week + time.Hour, ids: map[string]bool{seatID: true}, fresh: true},
		{name: "a seat of another store", age: week + time.Hour, ids: map[string]bool{"s-zzz999": true}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := project(t)
			dir := seatFolder(t, root, tc.age)

			if tc.fresh {
				test.WriteFile(t, filepath.Join(dir, "deep", "fresh.txt"), "now")
				test.Age(t, filepath.Join(dir, "deep"), tc.age)
				test.Age(t, dir, tc.age)
			}

			gone := scratch.ReapSeats(t.Context(), root, func(id string) bool { return tc.ids[id] }, time.Now())
			if (gone == 1) != tc.gone || test.Exists(t, dir) == tc.gone {
				t.Errorf("the reap should remove only a known seat's folder idle a week, removed %d", gone)
			}
		})
	}
}

func TestARenamedSeatTakesItsFolderAlong(t *testing.T) {
	root := project(t)
	seatFolder(t, root, 0)

	scratch.MoveSeat(root, "main", seatID, "lead", seatID)

	if test.Exists(t, scratch.SeatDir(root, "main")) || !test.Exists(t, filepath.Join(scratch.SeatDir(root, "lead"), "probe.py")) {
		t.Error("a renamed seat's folder should move to the new name")
	}

	test.WriteFile(t, filepath.Join(scratch.SeatDir(root, "main"), "keep.sh"), "echo keep\n")
	scratch.MoveSeat(root, "lead", seatID, "main", seatID)

	if !test.Exists(t, scratch.SeatDir(root, "lead")) {
		t.Error("a move onto a name another folder holds should leave the seat's folder where it is")
	}
}

func TestAMoveTakesOnlyTheSeatsOwnFolderOntoAFreeName(t *testing.T) {
	root := project(t)
	keep := filepath.Join(scratch.SeatDir(root, "main"), "keep.sh")
	test.WriteFile(t, keep, "echo keep\n")

	scratch.MoveSeat(root, "main", seatID, "lead", seatID)

	if !test.Exists(t, keep) || test.Exists(t, scratch.SeatDir(root, "lead")) {
		t.Error("a keep at the old name should stay where it is")
	}

	other := project(t)
	seatFolder(t, other, 0)

	err := os.Mkdir(scratch.SeatDir(other, "lead"), 0o700)
	if err != nil {
		t.Fatalf("making an empty keep: %v", err)
	}

	scratch.MoveSeat(other, "main", seatID, "lead", seatID)

	if !test.Exists(t, filepath.Join(scratch.SeatDir(other, "main"), "probe.py")) {
		t.Error("an empty folder at the new name should keep the seat's folder where it is")
	}
}

func TestAMarkerThatIsALinkMarksNothing(t *testing.T) {
	root := project(t)
	dir := scratch.SeatDir(root, "main")
	target := filepath.Join(t.TempDir(), "id")
	test.WriteFile(t, target, seatID+"\n")
	test.WriteFile(t, filepath.Join(dir, "keep.sh"), "echo keep\n")

	err := os.Symlink(target, filepath.Join(dir, scratch.Marker))
	if err != nil {
		t.Fatalf("linking the marker: %v", err)
	}

	if scratch.RemoveSeat(t.Context(), root, "main", seatID) || !test.Exists(t, dir) {
		t.Error("a folder whose marker is a link should be a keep")
	}
}

func TestASeatMoveRemarksTheFolderForTheNewSeat(t *testing.T) {
	root := project(t)
	seatFolder(t, root, 0)

	scratch.MoveSeat(root, "main", seatID, "lead", "s-new456")

	marker, err := os.ReadFile(filepath.Join(scratch.SeatDir(root, "lead"), scratch.Marker))
	if err != nil || strings.TrimSpace(string(marker)) != "s-new456" {
		t.Errorf("a moved folder should be marked for the seat it moved to, got %q, %v", marker, err)
	}

	if !scratch.RemoveSeat(t.Context(), root, "lead", "s-new456") {
		t.Error("the new seat's release should remove the moved folder")
	}
}

func TestOldSessionFoldersMoveIntoTheirSeatsFolder(t *testing.T) {
	root := project(t)
	seatFolder(t, root, 0)

	mapped := sessionDir(t, root, test.Session, 0)
	test.WriteFile(t, filepath.Join(mapped, "probe.py"), "print(2)\n")
	unmapped := sessionDir(t, root, test.OtherSession, 0)
	loose := filepath.Join(root, ".tmp", "claude", "loose.txt")
	test.WriteFile(t, loose, "loose")

	moved := scratch.Migrate(root, func(session string) (string, string, bool) {
		return "main", seatID, session == test.Session
	})

	dir := scratch.SeatDir(root, "main")
	clash := filepath.Join(dir, test.Session[:8], "probe.py")

	switch {
	case moved != 1:
		t.Errorf("one session folder should move, got %d", moved)
	case test.Exists(t, mapped):
		t.Error("the moved session's folder should be removed")
	case !test.Exists(t, filepath.Join(dir, "probe.txt")):
		t.Error("a file should land in the seat's folder")
	case !test.Exists(t, clash):
		t.Error("a file whose name the seat's folder holds should land in the session's subfolder")
	case !test.Exists(t, unmapped) || !test.Exists(t, loose):
		t.Error("an unmapped session folder and a loose file should stay")
	}
}

func TestAKeepAtTheSeatsNameTakesNoSessionFolder(t *testing.T) {
	root := project(t)
	test.WriteFile(t, filepath.Join(scratch.SeatDir(root, "main"), "keep.sh"), "echo keep\n")
	mapped := sessionDir(t, root, test.Session, 0)

	if scratch.Migrate(root, func(string) (string, string, bool) { return "main", seatID, true }) != 0 || !test.Exists(t, mapped) {
		t.Error("a session folder should not move into a keep")
	}
}

func TestTheSessionStartReapsSeatFoldersOnlyWithMemoryOff(t *testing.T) {
	for _, memoryOn := range []bool{true, false} {
		t.Run(map[bool]string{true: "memory on", false: "memory off"}[memoryOn], func(t *testing.T) {
			root := project(t)
			dir := seatFolder(t, root, week+time.Hour)

			settings := filepath.Join(t.TempDir(), "settings.json")
			test.WriteFile(t, settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"memory_enabled":`+
				map[bool]string{true: "true", false: "false"}[memoryOn]+`}}}}`)

			captured := &test.Captured{}
			code := scratch.Main(t.Context(), []string{"start"}, scratch.Env{Root: root, Settings: []string{settings}},
				captured.Streams(payload(test.Session)))

			if code != 0 || test.Exists(t, dir) != memoryOn {
				t.Errorf("agentic-scratch should reap a week-idle seat folder exactly while memory is off, exit %d", code)
			}

			if !memoryOn && !strings.Contains(captured.Out.String(), "Scratch: reaped 1 seat folder(s) nobody came back to.") {
				t.Errorf("the reap should be said, got %q", captured.Out.String())
			}
		})
	}
}
