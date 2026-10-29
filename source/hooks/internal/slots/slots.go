// Package slots keeps per-session state in one preallocated file per store:
// fixed-width slots, two per key, so a write never touches another key's
// bytes and a write a crash tore leaves the key's previous value readable.
//
// The file opens with a header slot, then holds pairs of twin slots:
//
//	header  magic "AGSLOT\x00", format, uint32 width, uint32 count, uint32 crc32c of those 16 bytes
//	slot    uint32 head crc32c over bytes 4 to the key's end
//	        uint32 value crc32c
//	        uint64 generation
//	        int64  written, Unix nanoseconds
//	        uint16 key length, uint16 zero, uint32 value length
//	        key, value
//
// Every number is little-endian, and the file is always count times width
// bytes long. Of a pair's twins the valid one of the higher generation holds
// the key's value; a write goes to the other twin with the next generation.
// A twin whose head or value fails its checksum is not valid, so a torn write
// reads as the twin before it. A key is cleared by writing an empty key the
// same way, which frees its pair for the next key that needs one. A pair whose
// twins are both blank has never been written; the pairs after it have not
// either, since a new key takes the first free pair.
//
// The slot file is never opened through a link, so one planted at its path
// is refused rather than written through.
//
// Writers take the lock file beside the slot file, so two never race for a
// free pair; readers take no lock. A file out of free pairs is written anew
// with twice the pairs it needs and renamed over the old one, so a reader
// holding the old file still reads it whole. Writes are not synced: what the file holds
// is a session's state, which a power loss may cost, never corrupt.
package slots

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"math/bits"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/filelock"
	"github.com/fza/agentic-helpers/source/hooks/internal/home"
)

const (
	magic      = "AGSLOT\x00"
	format     = 1
	headerSize = 20
	slotHead   = 32
	// MaxKey bounds a key, so a slot's head and key are read in one go.
	MaxKey = 255
	// headRead is how much of a twin a scan reads: its head, its key and a
	// small value.
	headRead = 512
	// MinWidth is the narrowest slot a file may have.
	MinWidth     = headRead
	initialPairs = 8
	// maxPairs bounds the pairs a file read from disk may claim.
	maxPairs = 1 << 20
)

var (
	ErrNotSlotFile = errors.New("not a slot file")
	ErrTooLarge    = errors.New("the value does not fit a slot")
	ErrBadKey      = errors.New("a slot key is 1 to 255 bytes")
	ErrUnchanged   = errors.New("the value stays as it is")
)

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// File is a slot file at Path. Width is the slot width a file created by a
// write gets; a file on disk keeps the width its header names.
type File struct {
	Path  string
	Width int
	Now   func() time.Time
}

// Open is the slot file at path, created by the first write with slots of
// width bytes.
func Open(path string, width int) *File {
	return &File{Path: path, Width: max(width, MinWidth)}
}

// Slot is one key's value as its newest valid twin holds it.
type Slot struct {
	Key        string
	Generation uint64
	Written    time.Time
	Value      []byte
}

// Header is what a slot file's header slot says.
type Header struct {
	Width int
	Count int
}

type twin struct {
	valid      bool
	blank      bool
	generation uint64
	written    int64
	key        string
	valueSum   uint32
	valueLen   int
	prefix     []byte
	offset     int64
}

func (f *File) now() time.Time {
	if f.Now == nil {
		return time.Now()
	}

	return f.Now()
}

func checkKey(key string) error {
	if key == "" || len(key) > MaxKey {
		return fmt.Errorf("%w: %q", ErrBadKey, key)
	}

	return nil
}

// Get is the value key holds, and whether it holds one.
func (f *File) Get(ctx context.Context, key string) ([]byte, bool, error) {
	err := checkKey(key)
	if err != nil {
		return nil, false, err
	}

	err = ctx.Err()
	if err != nil {
		return nil, false, fmt.Errorf("reading %s: %w", key, err)
	}

	file, err := os.OpenFile(f.Path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}

	if err != nil {
		return nil, false, fmt.Errorf("opening %s: %w", f.Path, err)
	}

	defer func() { _ = file.Close() }()

	header, err := readHeader(file)
	if err != nil {
		return nil, false, err
	}

	for pair := range pairsOf(header) {
		twins, err := readPair(file, header, pair)
		if err != nil {
			return nil, false, err
		}

		if twins[0].blank && twins[1].blank {
			return nil, false, nil
		}

		slot, found, err := valueOf(file, twins, key)
		if err != nil || found {
			return slot.Value, found, err
		}
	}

	return nil, false, nil
}

// Put sets key's value.
func (f *File) Put(ctx context.Context, key string, value []byte) error {
	return f.Update(ctx, key, func([]byte, bool) ([]byte, error) { return value, nil })
}

// Update sets key's value to what change makes of the value it holds, holding
// the writer lock across both, so writers changing one value in turn never
// lose each other's change. A change answering ErrUnchanged writes nothing.
func (f *File) Update(ctx context.Context, key string, change func(value []byte, found bool) ([]byte, error)) error {
	err := checkKey(key)
	if err != nil {
		return err
	}

	return f.locked(ctx, func(file *os.File, header Header) error {
		pair, twins, found, err := find(file, header, key)
		if err != nil {
			return err
		}

		var current []byte

		if found {
			slot, held, err := valueOf(file, twins, key)
			if err != nil {
				return err
			}

			if held {
				current = slot.Value
			}
		}

		value, err := change(current, found)
		if errors.Is(err, ErrUnchanged) {
			return nil
		}

		if err != nil {
			return err
		}

		if slotHead+len(key)+len(value) > header.Width {
			return fmt.Errorf("%w: %d bytes for %s", ErrTooLarge, len(value), key)
		}

		if !found {
			pair, twins, found, err = freePair(file, header)
			if err != nil {
				return err
			}

			if !found {
				return f.grow(file, header, Slot{Key: key, Generation: 1, Written: f.now(), Value: value})
			}
		}

		return writeTwin(file, header, pair, twins, key, value, f.now())
	})
}

// Delete clears key, freeing its pair for reuse.
func (f *File) Delete(ctx context.Context, key string) error {
	err := checkKey(key)
	if err != nil {
		return err
	}

	if !exists(f.Path) {
		return nil
	}

	return f.locked(ctx, func(file *os.File, header Header) error {
		pair, twins, found, err := find(file, header, key)
		if err != nil || !found {
			return err
		}

		return writeTwin(file, header, pair, twins, "", nil, f.now())
	})
}

// Reclaim clears every key drop names; drop sees each slot's key, generation
// and time, never its value.
func (f *File) Reclaim(ctx context.Context, drop func(Slot) bool) error {
	if !exists(f.Path) {
		return nil
	}

	return f.locked(ctx, func(file *os.File, header Header) error {
		for pair := range pairsOf(header) {
			twins, err := readPair(file, header, pair)
			if err != nil {
				return err
			}

			if twins[0].blank && twins[1].blank {
				return nil
			}

			winner := newest(twins)
			if winner < 0 || twins[winner].key == "" {
				continue
			}

			held := twins[winner]
			if drop(Slot{Key: held.key, Generation: held.generation, Written: time.Unix(0, held.written)}) {
				err = writeTwin(file, header, pair, twins, "", nil, f.now())
				if err != nil {
					return err
				}
			}
		}

		return nil
	})
}

// All is every key the file holds with its value, in pair order.
func (f *File) All(ctx context.Context) ([]Slot, error) {
	err := ctx.Err()
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", f.Path, err)
	}

	file, err := os.OpenFile(f.Path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", f.Path, err)
	}

	defer func() { _ = file.Close() }()

	_, held, err := Read(file)

	return held, err
}

// Read is a slot file's header and every key it holds with its value.
func Read(file io.ReaderAt) (Header, []Slot, error) {
	header, err := readHeader(file)
	if err != nil {
		return Header{}, nil, err
	}

	var held []Slot

	for pair := range pairsOf(header) {
		twins, err := readPair(file, header, pair)
		if err != nil {
			return header, held, err
		}

		if twins[0].blank && twins[1].blank {
			break
		}

		winner := newest(twins)
		if winner < 0 || twins[winner].key == "" {
			continue
		}

		slot, found, err := valueOf(file, twins, twins[winner].key)
		if err != nil {
			return header, held, err
		}

		if found {
			held = append(held, slot)
		}
	}

	return header, held, nil
}

// IsSlotFile is whether data opens with a slot file's header.
func IsSlotFile(data []byte) bool {
	return bytes.HasPrefix(data, []byte(magic))
}

func exists(path string) bool {
	_, err := os.Lstat(path)

	return err == nil
}

func pairsOf(header Header) int {
	return (header.Count - 1) / 2
}

func twinOffset(header Header, pair int, side int) int64 {
	return int64(header.Width) * int64(1+2*pair+side)
}

func encodeHeader(header Header) []byte {
	data := append([]byte(magic), format)
	data = binary.LittleEndian.AppendUint32(data, uint32(header.Width))
	data = binary.LittleEndian.AppendUint32(data, uint32(header.Count))

	return binary.LittleEndian.AppendUint32(data, crc32.Checksum(data, castagnoli))
}

func readHeader(file io.ReaderAt) (Header, error) {
	data := make([]byte, headerSize)

	_, err := file.ReadAt(data, 0)
	if err != nil {
		return Header{}, fmt.Errorf("%w: reading the header: %w", ErrNotSlotFile, err)
	}

	if !IsSlotFile(data) || data[len(magic)] != format {
		return Header{}, ErrNotSlotFile
	}

	if crc32.Checksum(data[:16], castagnoli) != binary.LittleEndian.Uint32(data[16:20]) {
		return Header{}, fmt.Errorf("%w: the header fails its checksum", ErrNotSlotFile)
	}

	header := Header{
		Width: int(binary.LittleEndian.Uint32(data[8:12])),
		Count: int(binary.LittleEndian.Uint32(data[12:16])),
	}

	if header.Width < MinWidth || header.Width > 1<<24 || header.Count < 1 || header.Count%2 != 1 ||
		pairsOf(header) > maxPairs {
		return Header{}, fmt.Errorf("%w: width %d, count %d", ErrNotSlotFile, header.Width, header.Count)
	}

	return header, nil
}

func readPair(file io.ReaderAt, header Header, pair int) ([2]twin, error) {
	var twins [2]twin

	for side := range twins {
		held, err := readTwin(file, header, twinOffset(header, pair, side))
		if err != nil {
			return twins, err
		}

		twins[side] = held
	}

	return twins, nil
}

func readTwin(file io.ReaderAt, header Header, offset int64) (twin, error) {
	prefix := make([]byte, min(headRead, header.Width))

	read, err := file.ReadAt(prefix, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return twin{}, fmt.Errorf("reading a slot: %w", err)
	}

	prefix = prefix[:read]
	held := twin{offset: offset, blank: true}

	for _, value := range prefix[:min(len(prefix), slotHead)] {
		if value != 0 {
			held.blank = false
		}
	}

	if len(prefix) < slotHead {
		return held, nil
	}

	keyLen := int(binary.LittleEndian.Uint16(prefix[24:26]))
	valueLen := int(binary.LittleEndian.Uint32(prefix[28:32]))

	if keyLen > MaxKey || binary.LittleEndian.Uint16(prefix[26:28]) != 0 || slotHead+keyLen > len(prefix) ||
		slotHead+keyLen+valueLen > header.Width {
		return held, nil
	}

	if crc32.Checksum(prefix[4:slotHead+keyLen], castagnoli) != binary.LittleEndian.Uint32(prefix[:4]) {
		return held, nil
	}

	held.valid = true
	held.valueSum = binary.LittleEndian.Uint32(prefix[4:8])
	held.generation = binary.LittleEndian.Uint64(prefix[8:16])
	held.written = int64(binary.LittleEndian.Uint64(prefix[16:24]))
	held.key = string(prefix[slotHead : slotHead+keyLen])
	held.valueLen = valueLen
	held.prefix = prefix[slotHead+keyLen:]

	return held, nil
}

// newest is the side of the valid twin of the higher generation, -1 where
// neither is valid.
func newest(twins [2]twin) int {
	switch {
	case twins[0].valid && twins[1].valid:
		if twins[1].generation > twins[0].generation {
			return 1
		}

		return 0
	case twins[0].valid:
		return 0
	case twins[1].valid:
		return 1
	}

	return -1
}

// valueOf is key's value where the pair holds it: the newest twin's, or the
// older twin's where the newest one's value fails its checksum.
func valueOf(file io.ReaderAt, twins [2]twin, key string) (Slot, bool, error) {
	winner := newest(twins)
	if winner < 0 || twins[winner].key != key {
		return Slot{}, false, nil
	}

	for _, side := range []int{winner, 1 - winner} {
		held := twins[side]
		if !held.valid || held.key != key {
			continue
		}

		value, err := fullValue(file, held)
		if err != nil {
			return Slot{}, false, err
		}

		if crc32.Checksum(value, castagnoli) == held.valueSum {
			return Slot{Key: key, Generation: held.generation, Written: time.Unix(0, held.written), Value: value}, true, nil
		}
	}

	return Slot{}, false, nil
}

func fullValue(file io.ReaderAt, held twin) ([]byte, error) {
	if held.valueLen <= len(held.prefix) {
		return bytes.Clone(held.prefix[:held.valueLen]), nil
	}

	value := make([]byte, held.valueLen)

	_, err := file.ReadAt(value, held.offset+slotHead+int64(len(held.key)))
	if err != nil {
		return nil, fmt.Errorf("reading a slot's value: %w", err)
	}

	return value, nil
}

func find(file io.ReaderAt, header Header, key string) (int, [2]twin, bool, error) {
	for pair := range pairsOf(header) {
		twins, err := readPair(file, header, pair)
		if err != nil {
			return 0, twins, false, err
		}

		if twins[0].blank && twins[1].blank {
			return 0, [2]twin{}, false, nil
		}

		winner := newest(twins)
		if winner >= 0 && twins[winner].key == key {
			return pair, twins, true, nil
		}
	}

	return 0, [2]twin{}, false, nil
}

// freePair is the first pair holding no key, and whether there is one.
func freePair(file io.ReaderAt, header Header) (int, [2]twin, bool, error) {
	for pair := range pairsOf(header) {
		twins, err := readPair(file, header, pair)
		if err != nil {
			return 0, twins, false, err
		}

		winner := newest(twins)
		if winner < 0 || twins[winner].key == "" {
			return pair, twins, true, nil
		}
	}

	return 0, [2]twin{}, false, nil
}

// grow replaces the file with one of more pairs holding every key it holds
// and added. A reader holding the old file reads it whole; one opening the
// path after the rename reads the new one.
func (f *File) grow(file io.ReaderAt, header Header, added Slot) error {
	_, held, err := Read(file)
	if err != nil {
		return err
	}

	held = append(held, added)

	return replace(f.Path, Header{Width: header.Width, Count: 1 + 2*pairsFor(len(held))}, held)
}

// pairsFor is the pairs a file holding held keys gets: twice as many, at
// least initialPairs, a power of two.
func pairsFor(held int) int {
	return max(initialPairs, 1<<bits.Len(uint(2*held-1)))
}

// replace writes a file of header holding held in its first pairs beside path,
// then renames it over path.
func replace(path string, header Header, held []Slot) error {
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("creating a slot file: %w", err)
	}

	err = fill(temp, header, held)

	closeErr := temp.Close()
	if err == nil {
		err = closeErr
	}

	if err == nil {
		err = os.Rename(temp.Name(), path)
	}

	if err != nil {
		_ = os.Remove(temp.Name())

		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}

func fill(file *os.File, header Header, held []Slot) error {
	err := file.Truncate(int64(header.Width) * int64(header.Count))
	if err != nil {
		return fmt.Errorf("sizing the slot file: %w", err)
	}

	_, err = file.WriteAt(encodeHeader(header), 0)
	if err != nil {
		return fmt.Errorf("writing the slot file's header: %w", err)
	}

	for pair, slot := range held {
		_, err = file.WriteAt(encodeSlot(slot.Key, slot.Value, slot.Generation, slot.Written), twinOffset(header, pair, 0))
		if err != nil {
			return fmt.Errorf("writing %s: %w", slot.Key, err)
		}
	}

	return nil
}

func encodeSlot(key string, value []byte, generation uint64, written time.Time) []byte {
	data := make([]byte, slotHead, slotHead+len(key)+len(value))
	binary.LittleEndian.PutUint32(data[4:8], crc32.Checksum(value, castagnoli))
	binary.LittleEndian.PutUint64(data[8:16], generation)
	binary.LittleEndian.PutUint64(data[16:24], uint64(written.UnixNano()))
	binary.LittleEndian.PutUint16(data[24:26], uint16(len(key)))
	binary.LittleEndian.PutUint32(data[28:32], uint32(len(value)))
	data = append(data, key...)
	binary.LittleEndian.PutUint32(data[0:4], crc32.Checksum(data[4:], castagnoli))

	return append(data, value...)
}

// writeTwin writes key and value into the pair's twin not holding its newest
// value, one generation on, in one positional write.
func writeTwin(file *os.File, header Header, pair int, twins [2]twin, key string, value []byte, at time.Time) error {
	side := 0

	var generation uint64

	for index, held := range twins {
		if held.valid && held.generation >= generation {
			generation = held.generation
			side = 1 - index
		}
	}

	_, err := file.WriteAt(encodeSlot(key, value, generation+1, at), twinOffset(header, pair, side))
	if err != nil {
		return fmt.Errorf("writing a slot: %w", err)
	}

	return nil
}

// locked runs write on the file holding the writers' lock, creating the file
// where it does not exist.
func (f *File) locked(ctx context.Context, write func(file *os.File, header Header) error) error {
	err := os.MkdirAll(filepath.Dir(f.Path), 0o700)
	if err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(f.Path), err)
	}

	lock, err := os.OpenFile(f.Path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("opening the lock of %s: %w", f.Path, err)
	}

	defer func() { _ = lock.Close() }()

	err = filelock.Exclusive(lock)
	if err != nil {
		return fmt.Errorf("locking %s: %w", f.Path, err)
	}

	defer func() { _ = filelock.Unlock(lock) }()

	err = ctx.Err()
	if err != nil {
		return fmt.Errorf("holding the lock of %s: %w", f.Path, err)
	}

	if !exists(f.Path) {
		err = replace(f.Path, Header{Width: f.Width, Count: 1 + 2*initialPairs}, nil)
		if err != nil {
			return err
		}
	}

	file, err := os.OpenFile(f.Path, os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("opening %s: %w", f.Path, err)
	}

	defer func() { _ = file.Close() }()

	header, err := readHeader(file)
	if err != nil {
		return fmt.Errorf("%s: %w", f.Path, err)
	}

	return write(file, header)
}

// Legacy is a file an earlier release kept for one session, and the key its
// content goes under.
type Legacy struct {
	Path string
	Key  string
}

// Import puts every legacy file's content under its key, written at the
// file's modification time, holding the writers' lock throughout. A file that
// cannot be read, or does not fit a slot, is left out: what it held is a
// session's state, not data.
func (f *File) Import(ctx context.Context, legacy []Legacy) error {
	if len(legacy) == 0 {
		return nil
	}

	return f.locked(ctx, func(opened *os.File, header Header) error {
		return f.importLocked(opened, header, legacy)
	})
}

// Adopt takes in, once, the per-session files an earlier release left: where
// the component's format.json in dir records a format below version, it
// imports every file legacy lists, writes format.json, and only then removes
// the files, all holding the writers' lock, so a failed run leaves the old
// files in place and no write lands in between. A newer format is refused.
func (f *File) Adopt(ctx context.Context, dir string, version int, legacy func() []Legacy) error {
	current, err := home.CheckComponent(dir, version)
	if err != nil || current == version {
		return err
	}

	return f.locked(ctx, func(opened *os.File, header Header) error {
		current, err := home.CheckComponent(dir, version)
		if err != nil || current == version {
			return err
		}

		found := legacy()

		err = f.importLocked(opened, header, found)
		if err != nil {
			return err
		}

		err = home.WriteComponentFormat(dir, version)
		if err != nil {
			return err
		}

		for _, held := range found {
			_ = os.Remove(held.Path)
		}

		return nil
	})
}

// GetJSON decodes key's value into target, answering whether it held one
// that decoded.
func (f *File) GetJSON(ctx context.Context, key string, target any) bool {
	value, found, err := f.Get(ctx, key)
	if err != nil || !found {
		return false
	}

	return json.Unmarshal(value, target) == nil
}

// PutJSON sets key's value to value encoded as JSON.
func (f *File) PutJSON(ctx context.Context, key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encoding %s: %w", key, err)
	}

	return f.Put(ctx, key, data)
}

func (f *File) importLocked(opened *os.File, header Header, legacy []Legacy) error {
	file := opened

	defer func() {
		if file != opened {
			_ = file.Close()
		}
	}()

	for _, held := range legacy {
		info, err := os.Lstat(held.Path)
		if err != nil || !info.Mode().IsRegular() || int(info.Size())+slotHead+len(held.Key) > header.Width ||
			checkKey(held.Key) != nil {
			continue
		}

		value, err := os.ReadFile(held.Path)
		if err != nil {
			continue
		}

		pair, twins, found, err := find(file, header, held.Key)
		if err != nil {
			return err
		}

		if !found {
			pair, twins, found, err = freePair(file, header)
			if err != nil {
				return err
			}
		}

		if !found {
			err = f.grow(file, header, Slot{Key: held.Key, Generation: 1, Written: info.ModTime(), Value: value})
			if err != nil {
				return err
			}

			grown, grownHeader, err := reopen(f.Path)
			if err != nil {
				return err
			}

			if file != opened {
				_ = file.Close()
			}

			file, header = grown, grownHeader

			continue
		}

		err = writeTwin(file, header, pair, twins, held.Key, value, info.ModTime())
		if err != nil {
			return err
		}
	}

	return nil
}

// reopen opens the file a grow renamed over path.
func reopen(path string) (*os.File, Header, error) {
	file, err := os.OpenFile(path, os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, Header{}, fmt.Errorf("opening %s: %w", path, err)
	}

	header, err := readHeader(file)
	if err != nil {
		_ = file.Close()

		return nil, Header{}, fmt.Errorf("%s: %w", path, err)
	}

	return file, header, nil
}
