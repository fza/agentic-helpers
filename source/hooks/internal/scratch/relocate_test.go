package scratch_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/scratch"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const day = 24 * time.Hour

// aged writes path holding content and dates it and every folder from it up
// to the root's `.tmp/` age ago.
func aged(t *testing.T, root string, path string, content string, age time.Duration) {
	t.Helper()

	test.WriteFile(t, path, content)

	for at := path; at != filepath.Join(root, ".tmp"); at = filepath.Dir(at) {
		test.Age(t, at, age)
	}
}

// contents is every file's content under root, by hash, whatever its path:
// what a move keeps and a removal loses.
func contents(t *testing.T, root string) []string {
	t.Helper()

	var sums []string

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || entry.Name() == scratch.Marker {
			return err
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		sum := sha256.Sum256(data)
		sums = append(sums, hex.EncodeToString(sum[:]))

		return nil
	})
	if err != nil {
		t.Fatalf("reading the tree: %v", err)
	}

	slices.Sort(sums)

	return sums
}

func relocated(t *testing.T, root string) []string {
	t.Helper()

	return scratch.Relocate(context.Background(), root, time.Now())
}

func TestAMarkedLegacySeatFolderMovesUnderSeats(t *testing.T) {
	root := project(t)
	legacy := filepath.Join(root, ".tmp", "claude")
	test.WriteFile(t, filepath.Join(legacy, "main", scratch.Marker), seatID+"\n")
	test.WriteFile(t, filepath.Join(legacy, "main", "deep", "probe.py"), "print(1)\n")
	test.WriteFile(t, filepath.Join(legacy, "lead", scratch.Marker), "s-zzz999\n")

	said := scratch.RelocateSeats(root, func(id string) bool { return id == seatID })

	if test.ReadFile(t, filepath.Join(scratch.SeatDir(root, "main"), "deep", "probe.py")) != "print(1)\n" ||
		test.ReadFile(t, filepath.Join(scratch.SeatDir(root, "main"), scratch.Marker)) != seatID+"\n" {
		t.Error("the seat's folder should land under .tmp/seats/ with its contents and marker")
	}

	if !slices.Contains(said, "Scratch: moved .tmp/claude/main/ to .tmp/seats/main/") {
		t.Errorf("the move should be said, got %q", said)
	}

	if !test.Exists(t, filepath.Join(legacy, "lead", scratch.Marker)) || test.Exists(t, scratch.SeatDir(root, "lead")) {
		t.Error("a folder marked for a seat of another store should stay")
	}
}

func TestALegacySessionFolderMovesUnderSeatsAtOnce(t *testing.T) {
	root := project(t)
	legacy := filepath.Join(root, ".tmp", "claude", test.Session)
	test.WriteFile(t, filepath.Join(legacy, "notes.md"), "notes\n")

	said := relocated(t, root)

	if test.ReadFile(t, filepath.Join(root, ".tmp", "seats", test.Session, "notes.md")) != "notes\n" {
		t.Error("a session folder should land under .tmp/seats/ with its contents, however fresh")
	}

	if !slices.Contains(said, "Scratch: moved .tmp/claude/"+test.Session+"/ to .tmp/seats/"+test.Session+"/") {
		t.Errorf("the move should be said, got %q", said)
	}
}

func TestALegacyFolderMeetingATakenNameStays(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(t *testing.T, root string)
		source string
		target string
		run    func(root string) []string
	}{
		{
			name:   "a session folder",
			source: filepath.Join("claude", test.Session),
			target: filepath.Join("seats", test.Session),
			run:    func(root string) []string { return scratch.Relocate(context.Background(), root, time.Now()) },
		},
		{
			name:   "a seat folder",
			source: filepath.Join("claude", "main"),
			target: filepath.Join("seats", "main"),
			setup: func(t *testing.T, root string) {
				t.Helper()
				test.WriteFile(t, filepath.Join(root, ".tmp", "claude", "main", scratch.Marker), seatID+"\n")
			},
			run: func(root string) []string {
				return scratch.RelocateSeats(root, func(id string) bool { return id == seatID })
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := project(t)
			test.WriteFile(t, filepath.Join(root, ".tmp", tc.source, "probe.py"), "print(1)\n")
			test.WriteFile(t, filepath.Join(root, ".tmp", tc.target, "probe.py"), "print(2)\n")

			if tc.setup != nil {
				tc.setup(t, root)
			}

			said := tc.run(root)

			if test.ReadFile(t, filepath.Join(root, ".tmp", tc.source, "probe.py")) != "print(1)\n" ||
				test.ReadFile(t, filepath.Join(root, ".tmp", tc.target, "probe.py")) != "print(2)\n" {
				t.Error("both folders should stay as they are")
			}

			if !strings.Contains(strings.Join(said, "\n"), "Scratch: left .tmp/"+filepath.ToSlash(tc.source)+"/ where it is") {
				t.Errorf("the folder left behind should be said, got %q", said)
			}
		})
	}
}

func TestAStrayMovesToKeepOnlyAfterADayQuiet(t *testing.T) {
	cases := []struct {
		name   string
		parent string
		entry  string
		file   string
		age    time.Duration
		fresh  bool
		moves  bool
	}{
		{name: "an unmarked legacy folder a day quiet", parent: "claude", entry: "probes", file: "probes/run.sh", age: day + time.Hour, moves: true},
		{name: "a loose legacy file a day quiet", parent: "claude", entry: "notes.md", file: "notes.md", age: day + time.Hour, moves: true},
		{name: "an unmarked legacy folder changed within the day", parent: "claude", entry: "probes", file: "probes/run.sh", age: day - time.Hour},
		{name: "a legacy folder with one fresh file deep inside", parent: "claude", entry: "probes", file: "probes/run.sh", age: day + time.Hour, fresh: true},
		{name: "an unmarked folder under seats a day quiet", parent: "seats", entry: "probes", file: "probes/run.sh", age: day + time.Hour, moves: true},
		{name: "a loose file under seats a day quiet", parent: "seats", entry: "notes.md", file: "notes.md", age: day + time.Hour, moves: true},
		{name: "an unmarked folder under seats changed within the day", parent: "seats", entry: "probes", file: "probes/run.sh", age: day - time.Hour},
		{name: "a session folder under seats", parent: "seats", entry: test.Session, file: test.Session + "/run.sh", age: 3 * day},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := project(t)
			source := filepath.Join(root, ".tmp", tc.parent, tc.entry)
			aged(t, root, filepath.Join(root, ".tmp", tc.parent, tc.file), "echo stray\n", tc.age)

			if tc.fresh {
				test.WriteFile(t, filepath.Join(source, "deep", "fresh.txt"), "now")
				test.Age(t, filepath.Join(source, "deep"), tc.age)
				test.Age(t, source, tc.age)
			}

			said := relocated(t, root)
			kept := filepath.Join(root, ".tmp", "keep", tc.entry)

			if test.Exists(t, kept) != tc.moves || test.Exists(t, source) == tc.moves {
				t.Errorf("a stray should move to .tmp/keep/ exactly once nothing in it changed for a day, said %q", said)
			}

			if tc.moves && !strings.Contains(strings.Join(said, "\n"), "Scratch: moved .tmp/"+tc.parent+"/"+tc.entry) {
				t.Errorf("the move should be said, got %q", said)
			}
		})
	}
}

func TestAMarkedFolderUnderSeatsIsNoStray(t *testing.T) {
	root := project(t)
	dir := scratch.SeatDir(root, "main")
	aged(t, root, filepath.Join(dir, scratch.Marker), "s-zzz999\n", 3*day)

	relocated(t, root)

	if !test.Exists(t, filepath.Join(dir, scratch.Marker)) {
		t.Error("a marked folder under .tmp/seats/ should stay, whoever's seat it names")
	}
}

func TestATakenKeepNameGetsTheNextSuffix(t *testing.T) {
	root := project(t)
	test.WriteFile(t, filepath.Join(root, ".tmp", "keep", "probes", "a.sh"), "a")
	test.WriteFile(t, filepath.Join(root, ".tmp", "keep", "probes-2"), "b")
	aged(t, root, filepath.Join(root, ".tmp", "claude", "probes", "c.sh"), "c", 2*day)

	said := relocated(t, root)

	if test.ReadFile(t, filepath.Join(root, ".tmp", "keep", "probes-3", "c.sh")) != "c" {
		t.Errorf("the stray should take the first free suffix, said %q", said)
	}

	if test.ReadFile(t, filepath.Join(root, ".tmp", "keep", "probes", "a.sh")) != "a" ||
		test.ReadFile(t, filepath.Join(root, ".tmp", "keep", "probes-2")) != "b" {
		t.Error("what .tmp/keep/ held should stay as it was")
	}
}

func TestALooseFileNeverReplacesAKeep(t *testing.T) {
	cases := []struct {
		name string
		kept string
	}{
		{name: "notes.md", kept: "notes-2.md"},
		{name: "Makefile", kept: "Makefile-2"},
		{name: ".env", kept: ".env-2"},
		{name: "archive.tar.gz", kept: "archive.tar-2.gz"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := project(t)
			test.WriteFile(t, filepath.Join(root, ".tmp", "keep", tc.name), "kept\n")
			aged(t, root, filepath.Join(root, ".tmp", "claude", tc.name), "stray\n", 2*day)

			relocated(t, root)

			if test.ReadFile(t, filepath.Join(root, ".tmp", "keep", tc.name)) != "kept\n" ||
				test.ReadFile(t, filepath.Join(root, ".tmp", "keep", tc.kept)) != "stray\n" {
				t.Error("a loose file should take the next free name, suffixed before its last extension, and leave the keep at its own")
			}
		})
	}
}

func TestAStrayFolderTakesItsSuffixAtTheEnd(t *testing.T) {
	root := project(t)
	test.WriteFile(t, filepath.Join(root, ".tmp", "keep", "probe.v1", "a.sh"), "a")
	aged(t, root, filepath.Join(root, ".tmp", "claude", "probe.v1", "b.sh"), "b", 2*day)

	said := relocated(t, root)

	if test.ReadFile(t, filepath.Join(root, ".tmp", "keep", "probe.v1-2", "b.sh")) != "b" {
		t.Errorf("a folder's name should take the suffix at its end, a dot in it being no extension, said %q", said)
	}
}

func TestTheMigrationRemovesNothingButAnEmptyLegacyFolder(t *testing.T) {
	root := project(t)
	legacy := filepath.Join(root, ".tmp", "claude")
	test.WriteFile(t, filepath.Join(legacy, "main", scratch.Marker), seatID+"\n")
	test.WriteFile(t, filepath.Join(legacy, "main", "probe.py"), "print(1)\n")
	test.WriteFile(t, filepath.Join(legacy, test.Session, "notes.md"), "notes\n")
	aged(t, root, filepath.Join(legacy, "probes", "run.sh"), "echo run\n", 2*day)
	aged(t, root, filepath.Join(legacy, "loose.txt"), "loose\n", 2*day)
	test.WriteFile(t, filepath.Join(root, ".tmp", "drafts", "entry.md"), "draft\n")
	test.WriteFile(t, filepath.Join(root, ".tmp", "project.log"), "log\n")
	before := contents(t, root)

	said := scratch.RelocateSeats(root, func(id string) bool { return id == seatID })
	said = append(said, relocated(t, root)...)

	if !slices.Equal(contents(t, root), before) {
		t.Error("every file should survive the migration")
	}

	if test.Exists(t, legacy) {
		t.Error("the emptied .tmp/claude/ should go")
	}

	if !slices.Contains(said, "Scratch: removed the empty .tmp/claude/") {
		t.Errorf("the removal should be said, got %q", said)
	}

	if test.ReadFile(t, filepath.Join(root, ".tmp", "drafts", "entry.md")) != "draft\n" ||
		test.ReadFile(t, filepath.Join(root, ".tmp", "project.log")) != "log\n" {
		t.Error("the project's own files under .tmp/ should stay where they are")
	}
}

func TestALegacyFolderStillHoldingAnythingStays(t *testing.T) {
	root := project(t)
	fresh := filepath.Join(root, ".tmp", "claude", "probes", "run.sh")
	test.WriteFile(t, fresh, "echo run\n")

	said := relocated(t, root)

	if !test.Exists(t, fresh) || slices.Contains(said, "Scratch: removed the empty .tmp/claude/") {
		t.Error("a .tmp/claude/ still holding anything should stay")
	}
}

func TestALegacyFolderThatIsALinkIsNeverRemoved(t *testing.T) {
	root := project(t)
	empty := filepath.Join(t.TempDir(), "empty")

	err := os.Mkdir(empty, 0o700)
	if err != nil {
		t.Fatalf("making the link's target: %v", err)
	}

	err = os.Symlink(empty, filepath.Join(root, ".tmp", "claude"))
	if err != nil {
		t.Fatalf("planting the link: %v", err)
	}

	relocated(t, root)

	if !test.Exists(t, filepath.Join(root, ".tmp", "claude")) || !test.Exists(t, empty) {
		t.Error("a link at .tmp/claude should never be removed")
	}
}

func TestAStrayTooLargeToDateStays(t *testing.T) {
	root := project(t)
	stray := filepath.Join(root, ".tmp", "claude", "probes")

	for index := range 10001 {
		test.WriteFile(t, filepath.Join(stray, strconv.Itoa(index)), "")
		test.Age(t, filepath.Join(stray, strconv.Itoa(index)), 2*day)
	}

	test.Age(t, stray, 2*day)

	relocated(t, root)

	if !test.Exists(t, stray) || test.Exists(t, filepath.Join(root, ".tmp", "keep")) {
		t.Error("a stray holding more than the walk reads should stay where it is")
	}
}

func TestNoMoveGoesThroughAPlantedLink(t *testing.T) {
	for _, planted := range []string{"keep", "seats", "claude"} {
		t.Run(planted, func(t *testing.T) {
			root := project(t)
			outside := test.Outside(t)
			test.Age(t, filepath.Join(outside, "keep.txt"), 2*day)
			test.Age(t, outside, 2*day)
			aged(t, root, filepath.Join(root, ".tmp", "claude", "probes", "run.sh"), "echo run\n", 2*day)
			test.WriteFile(t, filepath.Join(root, ".tmp", "claude", test.Session, "notes.md"), "notes\n")

			link := filepath.Join(root, ".tmp", planted)

			err := os.RemoveAll(link)
			if err != nil {
				t.Fatalf("clearing the name: %v", err)
			}

			err = os.Symlink(outside, link)
			if err != nil {
				t.Fatalf("planting the link: %v", err)
			}

			relocated(t, root)

			if !test.Untouched(t, outside) {
				t.Error("nothing should move into or out of a tree a planted link reaches")
			}
		})
	}
}

func TestTheSessionStartRelocates(t *testing.T) {
	for _, memoryOn := range []bool{true, false} {
		t.Run(map[bool]string{true: "memory on", false: "memory off"}[memoryOn], func(t *testing.T) {
			root := project(t)
			legacy := filepath.Join(root, ".tmp", "claude")
			test.WriteFile(t, filepath.Join(legacy, "main", scratch.Marker), seatID+"\n")
			test.WriteFile(t, filepath.Join(legacy, test.Session, "notes.md"), "notes\n")
			aged(t, root, filepath.Join(legacy, "probes", "run.sh"), "echo run\n", 2*day)

			settings := filepath.Join(t.TempDir(), "settings.json")
			test.WriteFile(t, settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"memory_enabled":`+
				strconv.FormatBool(memoryOn)+`}}}}`)

			captured := &test.Captured{}
			code := scratch.Main(t.Context(), []string{"start"}, scratch.Env{Root: root, Settings: []string{settings}},
				captured.Streams(payload(test.Session)))

			if code != 0 || !test.Exists(t, filepath.Join(root, ".tmp", "seats", test.Session, "notes.md")) ||
				!test.Exists(t, filepath.Join(root, ".tmp", "keep", "probes", "run.sh")) {
				t.Errorf("the session start should move session folders and quiet strays, exit %d", code)
			}

			if test.Exists(t, filepath.Join(scratch.SeatDir(root, "main"), scratch.Marker)) == memoryOn {
				t.Error("agentic-scratch should move a marked seat folder exactly while memory is off")
			}

			if !strings.Contains(captured.Out.String(), "Scratch: moved .tmp/claude/probes/ to .tmp/keep/probes/") {
				t.Errorf("each move should be said, got %q", captured.Out.String())
			}
		})
	}
}

func TestAMarkedLegacyFolderIsNoStray(t *testing.T) {
	root := project(t)
	dir := filepath.Join(root, ".tmp", "claude", "main")
	aged(t, root, filepath.Join(dir, scratch.Marker), "s-zzz999\n", 3*day)

	relocated(t, root)

	if !test.Exists(t, filepath.Join(dir, scratch.Marker)) || test.Exists(t, filepath.Join(root, ".tmp", "keep")) {
		t.Error("a marked folder under .tmp/claude/ is RelocateSeats' to move, never a stray")
	}
}

func TestACancelledStartMovesNothing(t *testing.T) {
	for _, parent := range []string{"claude", "seats"} {
		t.Run(parent, func(t *testing.T) {
			root := project(t)
			stray := filepath.Join(root, ".tmp", parent, "probes")
			aged(t, root, filepath.Join(stray, "run.sh"), "echo run\n", 3*day)

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			scratch.Relocate(ctx, root, time.Now())

			if !test.Exists(t, stray) {
				t.Error("a cancelled start should leave every entry where it is")
			}
		})
	}
}

func TestAClaimWhoseKeepMoveFailsMarksNothing(t *testing.T) {
	root := project(t)
	outside := test.Outside(t)
	held := filepath.Join(scratch.SeatDir(root, "main"), "keep.sh")
	test.WriteFile(t, held, "echo keep\n")

	err := os.Symlink(outside, filepath.Join(root, ".tmp", "keep"))
	if err != nil {
		t.Fatalf("planting the link: %v", err)
	}

	if scratch.Seat(root, "main", seatID) != "" {
		t.Error("a claim that could not move the folder away should name no scratch folder")
	}

	if test.Exists(t, filepath.Join(scratch.SeatDir(root, "main"), scratch.Marker)) || !test.Exists(t, held) || !test.Untouched(t, outside) {
		t.Error("the folder at the seat's name should stay as it was, unmarked")
	}
}
