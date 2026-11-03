package slots

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func openStore(t *testing.T) *File {
	t.Helper()

	dir := t.TempDir()

	return Open(dir, filepath.Join(dir, "sessions.slots"))
}

func mustPut(t *testing.T, file *File, key string, value []byte) {
	t.Helper()

	err := file.Put(context.Background(), key, value)
	if err != nil {
		t.Fatalf("putting %s: %v", key, err)
	}
}

func mustGet(t *testing.T, file *File, key string) ([]byte, bool) {
	t.Helper()

	value, found, err := file.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("getting %s: %v", key, err)
	}

	return value, found
}

// classHolders is, per class, how many of its pairs hold key.
func classHolders(t *testing.T, file *File, key string) [len(classWidths)]int {
	t.Helper()

	var held [len(classWidths)]int

	for class := range classWidths {
		slots, err := Open(file.Root, file.classPath(class)).All(context.Background())
		if err != nil {
			t.Fatalf("reading class %d: %v", class, err)
		}

		for _, slot := range slots {
			if slot.Key == key {
				held[class]++
			}
		}
	}

	return held
}

func headerOf(t *testing.T, path string) Header {
	t.Helper()

	opened, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}

	defer func() { _ = opened.Close() }()

	header, err := readHeader(opened)
	if err != nil {
		t.Fatalf("reading the header: %v", err)
	}

	return header
}

func TestAValueMovesBetweenInlineAndItsClass(t *testing.T) {
	file := openStore(t)
	key := "ladder/session"

	// The directory's twin holds a value up to 512 bytes less its head, the
	// key and the tag; a class twin up to its width less the head and key.
	steps := []struct {
		size  int
		class int
	}{
		{10, -1}, {1_000, 0}, {1_200, 0}, {5_000, 1}, {20_000, 2}, {MaxValue, 3}, {512 - slotHead - len(key) - 1, -1},
		{30_000, 2}, {2048 - slotHead - len(key), 0}, {2048 - slotHead - len(key) + 1, 1}, {10, -1},
	}

	for _, step := range steps {
		size := step.size
		value := bytes.Repeat([]byte{byte('a' + size%26)}, size)
		mustPut(t, file, key, value)

		got, found := mustGet(t, file, key)
		if !found || !bytes.Equal(got, value) {
			t.Fatalf("a value of %d bytes should read back whole", size)
		}

		want := [len(classWidths)]int{}
		if step.class >= 0 {
			want[step.class] = 1
		}

		if held := classHolders(t, file, key); held != want {
			t.Errorf("a value of %d bytes should lie in its smallest class alone, or inline, held %v", size, held)
		}
	}

	if header := headerOf(t, file.Path); header.Format != formatDirectory || header.Width != directoryWidth {
		t.Errorf("the store's directory should be of 512-byte twins, got %+v", header)
	}
}

// tearClass writes a class twin of key as a crash leaves it: the next
// generation in the twin the directory does not name, torn by tear, the
// directory left as it stood.
func tearClass(t *testing.T, file *File, key string, value []byte, tear func([]byte) []byte) {
	t.Helper()

	err := file.lockedSet(context.Background(), func(s *set) error {
		place, err := seek(s.dir, s.header, key)
		if err != nil {
			return err
		}

		_, prior, _, err := s.current(place, key)
		if err != nil {
			return err
		}

		class := classFor(key, value)

		classFile, err := s.class(class)
		if err != nil {
			return err
		}

		pair, twins, keep, err := s.classPair(classFile, class, prior)
		if err != nil {
			return err
		}

		side := 0
		if twins[0].valid && twins[0].generation == keep {
			side = 1
		}

		_, err = classFile.WriteAt(tear(encodeSlot(key, value, keep+1, time.Now())), twinOffset(classHeader(class), pair, side))

		return err
	})
	if err != nil {
		t.Fatalf("tearing: %v", err)
	}
}

// tearDirectory writes key's value into its class whole, then the directory
// twin naming it torn by tear.
func tearDirectory(t *testing.T, file *File, key string, value []byte, tear func([]byte) []byte) {
	t.Helper()

	err := file.lockedSet(context.Background(), func(s *set) error {
		place, err := seek(s.dir, s.header, key)
		if err != nil {
			return err
		}

		_, prior, _, err := s.current(place, key)
		if err != nil {
			return err
		}

		class := classFor(key, value)

		classFile, err := s.class(class)
		if err != nil {
			return err
		}

		pair, twins, keep, err := s.classPair(classFile, class, prior)
		if err != nil {
			return err
		}

		generation, err := writeTwinKeeping(classFile, classHeader(class), pair, twins, key, value, time.Now(), keep)
		if err != nil {
			return err
		}

		side := 0
		if place.twins[0].valid && place.twins[0].generation >= place.twins[1].generation {
			side = 1
		}

		pointer := encodeSlot(key, encodePointer(class, pair, generation), place.twins[1-side].generation+1, time.Now())
		_, err = s.dir.WriteAt(tear(pointer), twinOffset(s.header, place.pair, side))

		return err
	})
	if err != nil {
		t.Fatalf("tearing: %v", err)
	}
}

func TestATornWriteLeavesTheOlderValueReadable(t *testing.T) {
	tears := []struct {
		name string
		tear func(slot []byte) []byte
	}{
		{name: "whole", tear: func(slot []byte) []byte { return slot }},
		{name: "cut after its head", tear: func(slot []byte) []byte { return slot[:slotHead+10] }},
		{name: "cut inside its head", tear: func(slot []byte) []byte { return slot[:12] }},
		{name: "a value byte flipped", tear: func(slot []byte) []byte {
			torn := bytes.Clone(slot)
			torn[len(torn)-3] ^= 0x40

			return torn
		}},
	}

	cases := []struct {
		name      string
		older     []byte
		newer     []byte
		directory bool
	}{
		{"a class value rewritten in its class, the class twin", bytes.Repeat([]byte("o"), 1_500), bytes.Repeat([]byte("n"), 1_600), false},
		{"an inline value moving into a class, the directory twin", []byte("older"), bytes.Repeat([]byte("n"), 1_600), true},
		{"a class value moving to a larger class, the directory twin", bytes.Repeat([]byte("o"), 1_500), bytes.Repeat([]byte("n"), 6_000), true},
		{"a class value rewritten in its class, the directory twin", bytes.Repeat([]byte("o"), 1_500), bytes.Repeat([]byte("n"), 1_600), true},
	}

	for _, tc := range cases {
		for _, tear := range tears {
			t.Run(tc.name+" "+tear.name, func(t *testing.T) {
				file := openStore(t)
				key := "ladder/s1"

				mustPut(t, file, key, []byte("oldest"))
				mustPut(t, file, key, tc.older)

				if tc.directory {
					tearDirectory(t, file, key, tc.newer, tear.tear)
				} else {
					tearClass(t, file, key, tc.newer, tear.tear)
				}

				got, found := mustGet(t, file, key)

				if tc.directory && tear.name == "whole" {
					if !found || !bytes.Equal(got, tc.newer) {
						t.Errorf("a directory twin written whole should name the newer value")
					}
				} else if !found || !bytes.Equal(got, tc.older) {
					t.Errorf("a torn write should read as the value before it, got %d bytes, found %v", len(got), found)
				}

				after := bytes.Repeat([]byte("a"), 1_700)
				mustPut(t, file, key, after)

				if got, _ := mustGet(t, file, key); !bytes.Equal(got, after) {
					t.Errorf("a write after a torn one should read back")
				}

				if !tc.directory && !classPairHolds(t, file, tc.older) {
					t.Errorf("a write into a pair whose other twin a crash left unnamed should keep the twin the directory named")
				}

				err := file.Reclaim(context.Background(), func(Slot) bool { return false })
				if err != nil {
					t.Fatalf("reclaiming: %v", err)
				}

				if got, _ := mustGet(t, file, key); !bytes.Equal(got, after) {
					t.Errorf("a reclaim keeping the key should keep its value")
				}

				if held := classHolders(t, file, key); held != [len(classWidths)]int{1} {
					t.Errorf("a reclaim should clear every class twin no directory value names, held %v", held)
				}
			})
		}
	}
}

// classPairHolds says a twin of the 2 KiB class's first pair holds value.
func classPairHolds(t *testing.T, file *File, value []byte) bool {
	t.Helper()

	opened, err := os.Open(file.classPath(0))
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = opened.Close() }()

	twins, err := readPair(opened, classHeader(0), 0)
	if err != nil {
		t.Fatal(err)
	}

	for _, held := range twins {
		if !held.valid {
			continue
		}

		held, err := fullValue(opened, held)
		if err != nil {
			t.Fatal(err)
		}

		if bytes.Equal(held, value) {
			return true
		}
	}

	return false
}

func TestAClassPairFreedIsTakenAgain(t *testing.T) {
	file := openStore(t)
	first, second, third := bytes.Repeat([]byte("1"), 1_000), bytes.Repeat([]byte("2"), 1_000), bytes.Repeat([]byte("3"), 1_000)

	mustPut(t, file, "session/one", first)
	mustPut(t, file, "session/two", second)

	if got, _ := mustGet(t, file, "session/one"); !bytes.Equal(got, first) {
		t.Fatal("two keys of one class should each keep their own pair")
	}

	before, err := os.Stat(file.classPath(0))
	if err != nil {
		t.Fatal(err)
	}

	err = file.Delete(context.Background(), "session/one")
	if err != nil {
		t.Fatal(err)
	}

	mustPut(t, file, "session/three", third)

	after, err := os.Stat(file.classPath(0))
	if err != nil {
		t.Fatal(err)
	}

	if after.Size() != before.Size() {
		t.Errorf("a key cleared should free its class pair for the next key, the class grew from %d to %d bytes", before.Size(), after.Size())
	}

	got, _ := mustGet(t, file, "session/two")
	gotThird, _ := mustGet(t, file, "session/three")

	if !bytes.Equal(got, second) || !bytes.Equal(gotThird, third) {
		t.Error("a key taking a freed pair should leave the other keys of its class as they were")
	}
}

func TestAFileOfAnotherShapeIsRefused(t *testing.T) {
	cases := []struct {
		name  string
		plant func(t *testing.T, file *File)
	}{
		{"a class file at the directory's path", func(t *testing.T, file *File) {
			copyFile(t, file.classPath(0), file.Path)
		}},
		{"a class file of another width at a class's path", func(t *testing.T, file *File) {
			mustPut(t, file, "session/eight", bytes.Repeat([]byte("8"), 5_000))
			copyFile(t, file.classPath(1), file.classPath(0))
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file := openStore(t)
			mustPut(t, file, "session/two", bytes.Repeat([]byte("2"), 1_000))
			tc.plant(t, file)

			err := file.Put(context.Background(), "session/other", bytes.Repeat([]byte("o"), 1_000))
			if !errors.Is(err, ErrNotSlotFile) {
				t.Errorf("a write should refuse a file of another shape, got %v", err)
			}
		})
	}
}

func TestADirectoryReadRefusesAClassFile(t *testing.T) {
	file := openStore(t)
	mustPut(t, file, "session/two", bytes.Repeat([]byte("2"), 1_000))

	_, _, err := Open(file.Root, file.classPath(0)).Get(context.Background(), "session/two")
	if !errors.Is(err, ErrNotSlotFile) {
		t.Errorf("a lookup should refuse a class file as a directory, got %v", err)
	}
}

func copyFile(t *testing.T, from string, to string) {
	t.Helper()

	data, err := os.ReadFile(from)
	if err == nil {
		err = os.WriteFile(to, data, 0o600)
	}

	if err != nil {
		t.Fatal(err)
	}
}

func TestAHeaderOfAShapeThisReleaseDoesNotReadIsRefused(t *testing.T) {
	cases := []struct {
		name   string
		header Header
	}{
		{"a directory of wider twins", Header{Format: formatDirectory, Width: 4096, Count: 17}},
		{"a directory of no pair", Header{Format: formatDirectory, Width: directoryWidth, Count: 1}},
		{"a class of another width", Header{Format: formatClass, Width: 3_000}},
		{"a class counting its pairs", Header{Format: formatClass, Width: classWidths[0], Count: 3}},
		{"a fixed file narrower than a slot", Header{Format: formatFixed, Width: 100, Count: 17}},
		{"an unknown format", Header{Format: 4, Width: directoryWidth, Count: 17}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := readHeader(bytes.NewReader(encodeHeader(tc.header)))
			if !errors.Is(err, ErrNotSlotFile) {
				t.Errorf("a header of a shape this release does not read should be refused, got %v", err)
			}
		})
	}
}

// pairReads counts the twins a seek reads.
type pairReads struct {
	file    io.ReaderAt
	offsets map[int64]bool
}

func (reads *pairReads) ReadAt(data []byte, offset int64) (int, error) {
	reads.offsets[offset] = true

	return reads.file.ReadAt(data, offset)
}

func TestALookupOfAMissingKeyReadsItsProbeRunAlone(t *testing.T) {
	file := openStore(t)

	for key := range 3_000 {
		mustPut(t, file, fmt.Sprintf("shown/%04d", key), []byte("held"))
	}

	opened, err := os.Open(file.Path)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = opened.Close() }()

	header, err := readHeader(opened)
	if err != nil {
		t.Fatal(err)
	}

	if pairsOf(header) <= 3_000 {
		t.Fatalf("the directory should hold more pairs than keys, holds %d", pairsOf(header))
	}

	// Keys alike but for a few bytes spread over the directory rather than
	// crowding probe runs into growing it past what its load asks.
	if pairsOf(header) > 8_192 {
		t.Errorf("keys alike should spread over their directory, which grew to %d pairs", pairsOf(header))
	}

	const lookups = 500

	read := 0

	for missing := range lookups {
		reads := &pairReads{file: opened, offsets: map[int64]bool{}}

		place, err := seek(reads, header, fmt.Sprintf("absent/%04d", missing))
		if err != nil || place.found {
			t.Fatalf("a missing key should not be found: %v", err)
		}

		if len(reads.offsets) > 2*(maxProbe+1) {
			t.Fatalf("a lookup of a missing key should read its probe run alone, read %d twins", len(reads.offsets))
		}

		read += len(reads.offsets)
	}

	// A probe run ends at its first blank pair, so a lookup in a directory
	// less than half full reads one or two pairs on average.
	if pairs := float64(read) / 2 / lookups; pairs > 3 {
		t.Errorf("a lookup of a missing key should stop at the first blank pair, read %.1f pairs on average", pairs)
	}
}

// Keys of the shapes a large store holds, a session id or a seat id after
// each purpose, alike but for a few bytes: laid out at once they fit the
// directory their count asks, no probe run passing its bound.
func TestKeysOfAStoresShapesFitTheirDirectory(t *testing.T) {
	random := rand.New(rand.NewPCG(1, 2))
	alphabet := "0123456789abcdefghijklmnopqrstuvwxyz"

	seatID := func() string {
		id := []byte("s-")
		for range 6 {
			id = append(id, alphabet[random.IntN(len(alphabet))])
		}

		return string(id)
	}

	sessionID := func() string {
		return fmt.Sprintf("%08x-%04x-4%03x-%04x-%012x", random.Uint32(), random.Uint32()&0xffff, random.Uint32()&0xfff,
			random.Uint32()&0xffff|0x8000, random.Uint64()&0xffffffffffff)
	}

	var records [][][]byte

	for _, purpose := range []string{"waiter", "ladder", "graph-seen", "shown"} {
		for range 3_000 {
			records = append(records, [][]byte{encodeSlot(purpose+"/"+sessionID(), nil, 1, time.Now())})
		}
	}

	for _, purpose := range []string{"brief", "seen", "traps"} {
		for range 2_600 {
			records = append(records, [][]byte{encodeSlot(purpose+"/"+seatID(), nil, 1, time.Now())})
		}
	}

	if _, fits := placeRecords(records, directoryPairs(len(records))); !fits {
		t.Errorf("%d keys of a store's shapes should fit the directory their count asks", len(records))
	}
}

// A directory whose every pair holds a key, which no write leaves but a
// lookup must still bound: a missing key reads its probe run and stops.
func TestALookupInAFullDirectoryReadsItsProbeRunAlone(t *testing.T) {
	const pairs = 200

	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.slots")
	placed := map[int][][]byte{}

	for pair := range pairs {
		placed[pair] = [][]byte{encodeSlot(fmt.Sprintf("held/%02d", pair), encodeInline([]byte("v")), 1, time.Now())}
	}

	err := writeRaw(dir, path, Header{Format: formatDirectory, Width: directoryWidth, Count: 1 + 2*pairs}, pairs, placed)
	if err != nil {
		t.Fatal(err)
	}

	opened, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = opened.Close() }()

	header, err := readHeader(opened)
	if err != nil {
		t.Fatal(err)
	}

	reads := &pairReads{file: opened, offsets: map[int64]bool{}}

	place, err := seek(reads, header, "absent")
	if err != nil || place.found || place.open {
		t.Fatalf("a missing key should find neither itself nor room, %+v %v", place, err)
	}

	if len(reads.offsets) != 2*(maxProbe+1) {
		t.Errorf("a lookup in a full directory should read its probe run and stop, read %d twins", len(reads.offsets))
	}
}

func fixedStore(t *testing.T) (*File, []Slot) {
	t.Helper()

	dir := t.TempDir()
	fixed := OpenFixed(dir, filepath.Join(dir, "sessions.slots"), 128<<10)
	at := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	fixed.Now = func() time.Time { return at }

	for index, size := range []int{5, 400, 1_000, 3_000, 9_000, 40_000, 120_000, 2, 1_900} {
		key := fmt.Sprintf("key/%d", index)

		for round := range index%3 + 1 {
			mustPut(t, fixed, key, bytes.Repeat([]byte{byte('a' + round)}, size))
		}

		at = at.Add(time.Minute)
	}

	held, err := fixed.All(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	return Open(fixed.Root, fixed.Path), held
}

func sameSlots(t *testing.T, got []Slot, want []Slot) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("every key should stay, got %d of %d", len(got), len(want))
	}

	byKey := map[string]Slot{}
	for _, slot := range got {
		byKey[slot.Key] = slot
	}

	for _, slot := range want {
		held := byKey[slot.Key]
		if !bytes.Equal(held.Value, slot.Value) || held.Generation != slot.Generation || !held.Written.Equal(slot.Written) {
			t.Errorf("%s should keep its value, generation and time", slot.Key)
		}
	}
}

func TestARelayoutKeepsEveryKeysValue(t *testing.T) {
	cases := []struct {
		name  string
		start func(t *testing.T, file *File)
	}{
		{"a fixed file", func(*testing.T, *File) {}},
		{"a fixed file beside the classes a stopped relayout left", func(t *testing.T, file *File) {
			for class := range classWidths {
				err := os.WriteFile(file.classPath(class), []byte("left by a stopped run"), 0o600)
				if err != nil {
					t.Fatal(err)
				}
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file, want := fixedStore(t)
			tc.start(t, file)

			err := file.Relayout(context.Background(), nil)
			if err != nil {
				t.Fatalf("laying out: %v", err)
			}

			// Nine keys fill a directory of 18 pairs to half.
			if header := headerOf(t, file.Path); header.Format != formatDirectory || header.Count != 1+2*18 {
				t.Errorf("the fixed file should become a directory sized to its keys, %+v", header)
			}

			got, err := file.All(context.Background())
			if err != nil {
				t.Fatal(err)
			}

			sameSlots(t, got, want)

			err = file.Relayout(context.Background(), nil)
			if err != nil {
				t.Fatalf("laying out a directory again: %v", err)
			}

			again, _ := file.All(context.Background())
			sameSlots(t, again, want)
		})
	}
}

func TestARelayoutKeepsWhatItsFitKeeps(t *testing.T) {
	file, want := fixedStore(t)

	err := file.Relayout(context.Background(), func(slot Slot) ([]byte, bool) {
		if slot.Key == "key/0" {
			return nil, false
		}

		if slot.Key == "key/6" {
			return []byte("fitted"), true
		}

		return slot.Value, true
	})
	if err != nil {
		t.Fatalf("laying out: %v", err)
	}

	if _, found := mustGet(t, file, "key/0"); found {
		t.Error("a value its fit drops should be left out")
	}

	if value, _ := mustGet(t, file, "key/6"); string(value) != "fitted" {
		t.Errorf("a value its fit changes should be kept as fitted, got %d bytes", len(value))
	}

	if value, _ := mustGet(t, file, "key/5"); !bytes.Equal(value, want[5].Value) {
		t.Error("a value its fit keeps should stay as it was")
	}
}

// key/3 is the fixed store's one value of the 8 KiB class: a relayout
// leaving it out leaves that class without a file, whatever a stopped run
// left there.
func TestARelayoutClearsTheClassesAStoppedRunLeft(t *testing.T) {
	file, _ := fixedStore(t)

	err := os.WriteFile(file.classPath(1), []byte("left by a stopped run"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = file.Relayout(context.Background(), func(slot Slot) ([]byte, bool) { return slot.Value, slot.Key != "key/3" })
	if err != nil {
		t.Fatalf("laying out: %v", err)
	}

	_, err = os.Lstat(file.classPath(1))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a class file a stopped run left should be cleared, got %v", err)
	}
}

func TestTheFirstWriteLaysAFixedFileOut(t *testing.T) {
	file, want := fixedStore(t)

	mustPut(t, file, "key/new", []byte("new"))

	if header := headerOf(t, file.Path); header.Format != formatDirectory {
		t.Errorf("a write should lay a fixed file out as a directory, format %d", header.Format)
	}

	got, err := file.All(context.Background())
	if err != nil || len(got) != len(want)+1 {
		t.Fatalf("the directory should hold every key and the new one, got %d %v", len(got), err)
	}

	byKey := map[string][]byte{}
	for _, slot := range got {
		byKey[slot.Key] = slot.Value
	}

	for _, slot := range want {
		if !bytes.Equal(byKey[slot.Key], slot.Value) {
			t.Errorf("%s should keep its value", slot.Key)
		}
	}
}

func TestAReclaimShrinksTheDirectoryAndCutsTheClasses(t *testing.T) {
	file := openStore(t)

	for key := range 64 {
		mustPut(t, file, fmt.Sprintf("session/%02d", key), bytes.Repeat([]byte("v"), 1_000))
	}

	grown := headerOf(t, file.Path)

	err := file.Reclaim(context.Background(), func(slot Slot) bool {
		return slot.Key != "session/00" && slot.Key != "session/01"
	})
	if err != nil {
		t.Fatalf("reclaiming: %v", err)
	}

	shrunk := headerOf(t, file.Path)
	if shrunk.Count >= grown.Count || shrunk.Count != 1+2*initialPairs {
		t.Errorf("a directory left a quarter full or less should shrink, got %d slots from %d", shrunk.Count, grown.Count)
	}

	info, err := os.Stat(file.classPath(0))
	if err != nil {
		t.Fatal(err)
	}

	if info.Size() != int64(classWidths[0])*(1+2*2) {
		t.Errorf("a class file should be cut after its last pair holding a value, %d bytes", info.Size())
	}

	for key := range 64 {
		_, found := mustGet(t, file, fmt.Sprintf("session/%02d", key))
		if found != (key < 2) {
			t.Errorf("a reclaim should keep exactly the kept keys, wrong at %d", key)
		}
	}
}
