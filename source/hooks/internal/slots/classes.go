package slots

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
)

const (
	// directoryWidth is a directory's twin: a value up to about 430 bytes
	// lives in it.
	directoryWidth = MinWidth
	// maxProbe bounds how far from its home pair a directory places a key, so
	// a lookup reads at most maxProbe+1 pairs, 65 KiB. Linear probing in a
	// half-full table of tens of thousands of keys runs past 16 pairs; a bound
	// that tight would grow a large store's directory on chance alone.
	maxProbe = 64
	// settleProbe is how far from its home pair a write places a new key
	// before it grows the directory instead.
	settleProbe = 16
	// readTries bounds how often a reader follows a pointer a writer raced.
	readTries = 3

	tagInline = 0
	// pointerSize is a tag naming the class, a uint32 pair and a uint64
	// generation.
	pointerSize = 13
)

var (
	classWidths   = [...]int{2 << 10, 8 << 10, 32 << 10, 128 << 10}
	classSuffixes = [...]string{"-2k", "-8k", "-32k", "-128k"}
)

// entry is what a directory value says of where its key's value lies.
type entry struct {
	inline     []byte
	class      int
	pair       int
	generation uint64
}

func (held entry) inClass() bool { return held.class >= 0 }

func encodeInline(value []byte) []byte {
	return append([]byte{tagInline}, value...)
}

func encodePointer(class int, pair int, generation uint64) []byte {
	data := []byte{byte(1 + class)}
	data = binary.LittleEndian.AppendUint32(data, uint32(pair))

	return binary.LittleEndian.AppendUint64(data, generation)
}

func decodeEntry(value []byte) (entry, bool) {
	switch {
	case len(value) >= 1 && value[0] == tagInline:
		return entry{inline: value[1:], class: -1}, true
	case len(value) == pointerSize && int(value[0]) >= 1 && int(value[0]) <= len(classWidths):
		return entry{
			class: int(value[0]) - 1, pair: int(binary.LittleEndian.Uint32(value[1:5])),
			generation: binary.LittleEndian.Uint64(value[5:13]),
		}, true
	}

	return entry{}, false
}

// classFor is the smallest class whose twin holds key and value, -1 where the
// directory's twin holds them inline.
func classFor(key string, value []byte) int {
	if slotHead+len(key)+1+len(value) <= directoryWidth {
		return -1
	}

	for class, width := range classWidths {
		if slotHead+len(key)+len(value) <= width {
			return class
		}
	}

	return len(classWidths) - 1
}

// directoryPairs is the pairs a directory written anew for held keys gets:
// twice as many, at least initialPairs, so it is half full. A power of two,
// as a fixed-width file gets, would leave a large store's directory up to
// three quarters empty at 1 KiB a pair.
func directoryPairs(held int) int {
	return max(initialPairs, 2*held)
}

// roomless is the error of keys no directory a reader takes finds room for:
// a directory grows by half while a key finds none, and keys whose hashes
// crowd one run would otherwise grow it without end.
func roomless(keys int) error {
	return fmt.Errorf("%w: %d keys find no room in a directory of %d pairs", ErrTooLarge, keys, maxPairs)
}

// grownPairs is the pairs a directory gets when a key finds no room within
// its probe run in a table of pairs: half as many again.
func grownPairs(pairs int) int {
	return pairs + pairs/2
}

// homePair is where key's probe run starts in a table of pairs: the FNV-1a
// sum of key modulo the pairs.
func homePair(key string, pairs int) int {
	sum := fnv.New64a()
	_, _ = sum.Write([]byte(key))

	return int(sum.Sum64() % uint64(pairs))
}

// spot is where a key stands in a table: the pair holding it, else the
// first pair of its probe run a write of it may take.
type spot struct {
	pair     int
	twins    [2]twin
	found    bool
	open     bool
	distance int
}

// seek is the spot of key: in a directory along its probe run, in a fixed
// file along its pairs up to the first blank one.
func seek(file io.ReaderAt, header Header, key string) (spot, error) {
	pairs := pairsOf(header)
	start, reach := 0, pairs

	if header.Format == formatDirectory {
		start, reach = homePair(key, pairs), min(pairs, maxProbe+1)
	}

	var free spot

	for distance := range reach {
		pair := (start + distance) % pairs

		twins, err := readPair(file, header, pair)
		if err != nil {
			return spot{}, err
		}

		winner := newest(twins)
		if winner >= 0 && twins[winner].key == key {
			return spot{pair: pair, twins: twins, found: true}, nil
		}

		if !free.open && (winner < 0 || twins[winner].key == "") {
			free = spot{pair: pair, twins: twins, open: true, distance: distance}
		}

		if twins[0].blank && twins[1].blank {
			break
		}
	}

	return free, nil
}

func (f *File) classPath(class int) string {
	return strings.TrimSuffix(f.Path, ".slots") + classSuffixes[class] + ".slots"
}

// classPairs is how many pairs a class file of size holds: every pair its
// size reaches, a pair a torn append left short among them.
func classPairs(size int64, width int) int {
	beyond := size - int64(width)
	if beyond <= 0 {
		return 0
	}

	return int((beyond + int64(2*width) - 1) / int64(2*width))
}

func classHeader(class int) Header {
	return Header{Format: formatClass, Width: classWidths[class]}
}

// classValue is key's value in the class twin of generation, if the pair
// holds it.
func classValue(file io.ReaderAt, class int, pair int, key string, generation uint64) ([]byte, bool, error) {
	twins, err := readPair(file, classHeader(class), pair)
	if err != nil {
		return nil, false, err
	}

	for _, held := range twins {
		if !held.valid || held.key != key || held.generation != generation {
			continue
		}

		value, err := fullValue(file, held)
		if err != nil {
			return nil, false, err
		}

		return value, checksumHolds(value, held), nil
	}

	return nil, false, nil
}

// lookup is key's slot as a reader finds it, its value read from the class
// it points at; a pointer to a twin a writer moved on reads the directory
// again.
func (f *File) lookup(key string) (Slot, bool, error) {
	for range readTries {
		slot, held, found, err := f.directorySlot(key)
		if err != nil || !found {
			return Slot{}, false, err
		}

		if !held.inClass() {
			slot.Value = held.inline

			return slot, true, nil
		}

		value, found, err := f.readClass(held, key)
		if err != nil {
			return Slot{}, false, err
		}

		if found {
			slot.Value = value

			return slot, true, nil
		}
	}

	return Slot{}, false, nil
}

func (f *File) directorySlot(key string) (Slot, entry, bool, error) {
	file, err := os.OpenFile(f.Path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return Slot{}, entry{}, false, nil
	}

	if err != nil {
		return Slot{}, entry{}, false, fmt.Errorf("opening %s: %w", f.Path, err)
	}

	defer func() { _ = file.Close() }()

	header, err := readHeader(file)
	if err != nil {
		return Slot{}, entry{}, false, err
	}

	if header.Format == formatClass {
		return Slot{}, entry{}, false, fmt.Errorf("%w: %s is a class file", ErrNotSlotFile, f.Path)
	}

	place, err := seek(file, header, key)
	if err != nil || !place.found {
		return Slot{}, entry{}, false, err
	}

	slot, found, err := valueOf(file, place.twins, key)
	if err != nil || !found {
		return Slot{}, entry{}, false, err
	}

	if header.Format == formatFixed {
		return slot, entry{inline: slot.Value, class: -1}, true, nil
	}

	held, valid := decodeEntry(slot.Value)

	return slot, held, valid, nil
}

func (f *File) readClass(held entry, key string) ([]byte, bool, error) {
	file, err := os.OpenFile(f.classPath(held.class), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}

	if err != nil {
		return nil, false, fmt.Errorf("opening %s: %w", f.classPath(held.class), err)
	}

	defer func() { _ = file.Close() }()

	return classValue(file, held.class, held.pair, key, held.generation)
}

// set is a store held under the writers' lock: its directory, and each
// class file once a write needs it.
type set struct {
	f       *File
	dir     *os.File
	header  Header
	classes [len(classWidths)]*os.File
}

func (s *set) close() {
	_ = s.dir.Close()

	for _, file := range s.classes {
		if file != nil {
			_ = file.Close()
		}
	}
}

// openSet opens the store under the writers' lock the caller holds,
// creating its directory where it is missing and rewriting a fixed-width
// file there as a directory with its classes.
func (f *File) openSet() (*set, error) {
	if !exists(f.Path) {
		err := replace(f.Root, f.Path, Header{Format: formatDirectory, Width: directoryWidth, Count: 1 + 2*initialPairs}, nil)
		if err != nil {
			return nil, err
		}
	}

	dir, header, err := reopen(f.Root, f.Path)
	if err != nil {
		return nil, err
	}

	if header.Format == formatFixed {
		_ = dir.Close()

		err = f.relayoutLocked(nil)
		if err != nil {
			return nil, err
		}

		dir, header, err = reopen(f.Root, f.Path)
		if err != nil {
			return nil, err
		}
	}

	if header.Format != formatDirectory {
		_ = dir.Close()

		return nil, fmt.Errorf("%w: %s is no directory", ErrNotSlotFile, f.Path)
	}

	return &set{f: f, dir: dir, header: header}, nil
}

func (f *File) lockedSet(ctx context.Context, write func(s *set) error) error {
	release, err := f.takeLock(ctx)
	if err != nil {
		return err
	}

	defer release()

	s, err := f.openSet()
	if err != nil {
		return err
	}

	defer s.close()

	return write(s)
}

// class is the class file, created holding its header alone where it is
// missing.
func (s *set) class(class int) (*os.File, error) {
	if s.classes[class] != nil {
		return s.classes[class], nil
	}

	path := s.f.classPath(class)
	if !exists(path) {
		err := replace(s.f.Root, path, classHeader(class), nil)
		if err != nil {
			return nil, err
		}
	}

	file, header, err := reopen(s.f.Root, path)
	if err != nil {
		return nil, err
	}

	if header != classHeader(class) {
		_ = file.Close()

		return nil, fmt.Errorf("%w: %s is no class file of width %d", ErrNotSlotFile, path, classWidths[class])
	}

	s.classes[class] = file

	return file, nil
}

// current is key's slot at place and where its value lies.
func (s *set) current(place spot, key string) (Slot, entry, bool, error) {
	if !place.found {
		return Slot{}, entry{class: -1}, false, nil
	}

	slot, found, err := valueOf(s.dir, place.twins, key)
	if err != nil || !found {
		return Slot{}, entry{class: -1}, false, err
	}

	held, valid := decodeEntry(slot.Value)
	if !valid {
		return Slot{}, entry{class: -1}, false, nil
	}

	if !held.inClass() {
		slot.Value = held.inline

		return slot, held, true, nil
	}

	file, err := s.class(held.class)
	if err != nil {
		return Slot{}, held, false, err
	}

	value, found, err := classValue(file, held.class, held.pair, key, held.generation)
	slot.Value = value

	return slot, held, found, err
}

// put writes key's value at place, written at, moving it between the
// directory's twin and the classes as its size asks, data before the
// directory twin naming it; prior is where the value it replaces lay.
func (s *set) put(place spot, key string, value []byte, at time.Time, prior entry) error {
	class := classFor(key, value)
	if class < 0 {
		err := s.writeDirectory(place, key, encodeInline(value), at)
		if err == nil && prior.inClass() {
			err = s.clearClass(prior, key)
		}

		return err
	}

	file, err := s.class(class)
	if err != nil {
		return err
	}

	pair, twins, keep, err := s.classPair(file, class, prior)
	if err != nil {
		return err
	}

	generation, err := writeTwinKeeping(file, classHeader(class), pair, twins, key, value, at, keep)
	if err != nil {
		return err
	}

	err = s.writeDirectory(place, key, encodePointer(class, pair, generation), at)
	if err == nil && prior.inClass() && prior.class != class {
		err = s.clearClass(prior, key)
	}

	return err
}

// classPair is the pair a value of class goes to: the one its prior value
// holds in the same class, keeping the twin the directory names, else the
// first free one, else a new pair appended.
func (s *set) classPair(file *os.File, class int, prior entry) (int, [2]twin, uint64, error) {
	header := classHeader(class)

	if prior.inClass() && prior.class == class {
		twins, err := readPair(file, header, prior.pair)

		return prior.pair, twins, prior.generation, err
	}

	pairs, err := sizedPairs(file, header.Width)
	if err != nil {
		return 0, [2]twin{}, 0, err
	}

	for pair := range pairs {
		twins, err := readPair(file, header, pair)
		if err != nil {
			return 0, twins, 0, err
		}

		winner := newest(twins)
		if winner < 0 || twins[winner].key == "" {
			return pair, twins, 0, nil
		}
	}

	return pairs, [2]twin{{blank: true}, {blank: true}}, 0, nil
}

func sizedPairs(file *os.File, width int) (int, error) {
	info, err := file.Stat()
	if err != nil {
		return 0, fmt.Errorf("sizing a class file: %w", err)
	}

	return classPairs(info.Size(), width), nil
}

// clearClass clears key from the class pair where its value lay.
func (s *set) clearClass(held entry, key string) error {
	file, err := s.class(held.class)
	if err != nil {
		return err
	}

	twins, err := readPair(file, classHeader(held.class), held.pair)
	if err != nil {
		return err
	}

	winner := newest(twins)
	if winner < 0 || twins[winner].key != key {
		return nil
	}

	_, err = writeTwin(file, classHeader(held.class), held.pair, twins, "", nil, s.f.now())

	return err
}

// sweepClasses clears every class twin holding a key that no directory value
// names, as a crash between a class write and its directory write leaves,
// then cuts each class file after its last pair holding a value.
func (s *set) sweepClasses(named map[[2]int]bool) error {
	for class := range classWidths {
		if !exists(s.f.classPath(class)) {
			continue
		}

		file, err := s.class(class)
		if err != nil {
			return err
		}

		header := classHeader(class)

		pairs, err := sizedPairs(file, header.Width)
		if err != nil {
			return err
		}

		used := 0

		for pair := range pairs {
			twins, err := readPair(file, header, pair)
			if err != nil {
				return err
			}

			winner := newest(twins)
			if winner < 0 || twins[winner].key == "" {
				continue
			}

			if named[[2]int{class, pair}] {
				used = pair + 1

				continue
			}

			_, err = writeTwin(file, header, pair, twins, "", nil, s.f.now())
			if err != nil {
				return err
			}
		}

		if used < pairs {
			err = file.Truncate(int64(header.Width) * int64(1+2*used))
			if err != nil {
				return fmt.Errorf("cutting %s: %w", s.f.classPath(class), err)
			}
		}
	}

	return nil
}

// writeDirectory writes key's directory value at place. A new key whose free
// pair lies past settleProbe from its home writes the directory anew, half as
// large again: runs that long mean a directory filling up, whose lookups of a
// missing key would read ever longer runs.
func (s *set) writeDirectory(place spot, key string, value []byte, at time.Time) error {
	if place.found || (place.open && place.distance <= settleProbe) {
		_, err := writeTwin(s.dir, s.header, place.pair, place.twins, key, value, at)

		return err
	}

	return s.rebuild(encodeSlot(key, value, 1, at), grownPairs(pairsOf(s.header)))
}

// rebuild writes the directory anew holding every key it holds, each pair's
// twins of that key as they stand, and added where there is one; at the
// pairs its keys need, least at the fewest, grown by half while a key finds
// no pair within its probe run.
func (s *set) rebuild(added []byte, least int) error {
	records, err := directoryRecords(s.dir, s.header)
	if err != nil {
		return err
	}

	if added != nil {
		records = append(records, [][]byte{added})
	}

	for pairs := max(directoryPairs(len(records)), least); ; pairs = grownPairs(pairs) {
		if pairs > maxPairs {
			return roomless(len(records))
		}

		placed, fits := placeRecords(records, pairs)
		if !fits {
			continue
		}

		err = writeRaw(s.f.Root, s.f.Path, Header{Format: formatDirectory, Width: directoryWidth, Count: 1 + 2*pairs}, pairs, placed)
		if err != nil {
			return err
		}

		_ = s.dir.Close()

		s.dir, s.header, err = reopen(s.f.Root, s.f.Path)

		return err
	}
}

// directoryRecords is, per key the directory holds, the raw twins of its pair
// that hold it.
func directoryRecords(file io.ReaderAt, header Header) ([][][]byte, error) {
	var records [][][]byte

	for pair := range pairsOf(header) {
		twins, err := readPair(file, header, pair)
		if err != nil {
			return nil, err
		}

		winner := newest(twins)
		if winner < 0 || twins[winner].key == "" {
			continue
		}

		var raw [][]byte

		for _, held := range twins {
			if !held.valid || held.key != twins[winner].key {
				continue
			}

			data := make([]byte, slotHead+len(held.key)+held.valueLen)

			_, err = file.ReadAt(data, held.offset)
			if err != nil {
				return nil, fmt.Errorf("reading a slot: %w", err)
			}

			raw = append(raw, data)
		}

		records = append(records, raw)
	}

	return records, nil
}

func rawKey(raw []byte) string {
	length := int(binary.LittleEndian.Uint16(raw[24:26]))

	return string(raw[slotHead : slotHead+length])
}

// placeRecords lays each key's twins at the first free pair of its probe
// run in a table of pairs, failing where a run holds no free pair.
func placeRecords(records [][][]byte, pairs int) (map[int][][]byte, bool) {
	placed := map[int][][]byte{}

	for _, raw := range records {
		start := homePair(rawKey(raw[0]), pairs)
		taken := false

		for distance := range min(pairs, maxProbe+1) {
			pair := (start + distance) % pairs
			if _, used := placed[pair]; !used {
				placed[pair] = raw
				taken = true

				break
			}
		}

		if !taken {
			return nil, false
		}
	}

	return placed, true
}

// writeRaw writes a file of header and pairs holding each pair's raw twins,
// then renames it over path.
func writeRaw(root string, path string, header Header, pairs int, placed map[int][][]byte) error {
	return replaceWith(root, path, func(file *os.File) error {
		err := sizeAndHead(file, header, int64(header.Width)*int64(1+2*pairs))
		if err != nil {
			return err
		}

		for pair, raw := range placed {
			for side, data := range raw {
				_, err = file.WriteAt(data, twinOffset(header, pair, side))
				if err != nil {
					return fmt.Errorf("writing a slot: %w", err)
				}
			}
		}

		return nil
	})
}

// Fit is what a value becomes when a store is laid out anew, and whether it
// is kept: a kind's value an earlier release wrote past its ceiling, fitted.
type Fit func(Slot) ([]byte, bool)

// Relayout rewrites a fixed-width file at the path as a directory with its
// classes, under the writers' lock, each value as fit keeps it and at the
// generation and time it held; a file already a directory, or none, is left
// as it is.
func (f *File) Relayout(ctx context.Context, fit Fit) error {
	if !exists(f.Path) {
		return nil
	}

	release, err := f.takeLock(ctx)
	if err != nil {
		return err
	}

	defer release()

	return f.relayoutLocked(fit)
}

// relayoutLocked writes the classes first and renames the directory over the
// fixed file last, so a run stopped anywhere finds the fixed file and runs
// again from its start, clearing the classes it left. Without a fit, a value
// is kept as it is where a slot takes it.
func (f *File) relayoutLocked(fit Fit) error {
	file, err := safefs.OpenChecked(f.Root, f.Path, os.O_RDONLY, 0o600)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("opening %s: %w", f.Path, err)
	}

	header, held, err := Read(file)
	_ = file.Close()

	if err != nil || header.Format != formatFixed {
		return err
	}

	var (
		directory [][][]byte
		classes   [len(classWidths)]map[int][][]byte
	)

	for _, slot := range held {
		value, keep := slot.Value, true
		if fit != nil {
			value, keep = fit(slot)
		}

		if !keep || len(value) > MaxValue {
			continue
		}

		class := classFor(slot.Key, value)
		stored := encodeInline(value)

		if class >= 0 {
			if classes[class] == nil {
				classes[class] = map[int][][]byte{}
			}

			pair := len(classes[class])
			classes[class][pair] = [][]byte{encodeSlot(slot.Key, value, slot.Generation, slot.Written)}
			stored = encodePointer(class, pair, slot.Generation)
		}

		directory = append(directory, [][]byte{encodeSlot(slot.Key, stored, slot.Generation, slot.Written)})
	}

	for class := range classWidths {
		err = os.Remove(f.classPath(class))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("clearing %s: %w", f.classPath(class), err)
		}

		if classes[class] != nil {
			err = writeRaw(f.Root, f.classPath(class), classHeader(class), len(classes[class]), classes[class])
			if err != nil {
				return err
			}
		}
	}

	for pairs := directoryPairs(len(directory)); ; pairs = grownPairs(pairs) {
		if pairs > maxPairs {
			return roomless(len(directory))
		}

		placed, fits := placeRecords(directory, pairs)
		if fits {
			return writeRaw(f.Root, f.Path, Header{Format: formatDirectory, Width: directoryWidth, Count: 1 + 2*pairs}, pairs, placed)
		}
	}
}
