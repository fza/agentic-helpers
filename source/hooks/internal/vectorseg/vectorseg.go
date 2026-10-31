// Package vectorseg keeps entry vectors in immutable segment files a search
// maps read-only, after sdd's search segments. A write adds one segment holding
// only the rows it embedded, so its cost follows the rows written, never the
// store; merges fold segments of a similar size, so a row is rewritten a
// logarithmic number of times over its life. Which rows count is the caller's
// to say, by entry id and content hash; the store's manifest records only the
// cap its rows' texts were cut to.
//
// A segment file is only ever created by rename and removed by unlink, and a
// mapping is released only once the last snapshot holding it closes, so a
// search never reads a shrinking file. Files are not synced: a sync on darwin
// is a full flush of the drive, and a segment a crash left torn fails its size
// check, is skipped, and its entries are embedded again.
package vectorseg

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/atomicfile"
	"github.com/fza/agentic-helpers/source/hooks/internal/filelock"
	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
)

var (
	ErrCorrupt = errors.New("unreadable vector segment")
	ErrDims    = errors.New("vector dimensions differ")
	ErrFault   = errors.New("a vector segment faulted while read")
)

const (
	segmentExt = ".seg"
	tempPrefix = ".segment-"
	lockName   = "segments.lock"
	fillName   = "fill.lock"
	// manifestName holds the Manifest, named as sdd names its own.
	manifestName = "manifest.json"
	// mergeRatio is how much larger each size class is than the one below it;
	// a class holding this many segments merges into one.
	mergeRatio = 4
)

// Row is one entry's chunk vectors, embedded from the content Hash names.
type Row struct {
	ID     string
	Hash   string
	Chunks [][]float32
}

// Live says whether a row counts: its entry is live and the row holds the
// entry's current content.
type Live func(id string, hash string) bool

// Manifest is what a store records of how its rows were built: MaxRunes is
// the cap every text of its rows was cut to, 0 where none is recorded. A fill
// writes it; a search cuts its query to it.
type Manifest struct {
	MaxRunes int `json:"max_runes,omitempty"`
}

// ErrManifest is a manifest that is there and does not read.
var ErrManifest = errors.New("unreadable vector store manifest")

type Store struct {
	root string
	dir  string
}

func Open(root string, dir string) *Store {
	return &Store{root: root, dir: dir}
}

func (store *Store) Dir() string {
	return store.dir
}

// Manifest is the store's manifest, the zero one where none is written yet.
func (store *Store) Manifest() (Manifest, error) {
	path := filepath.Join(store.dir, manifestName)

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Manifest{}, nil
	}

	if err != nil {
		return Manifest{}, fmt.Errorf("reading the vector store manifest: %w", err)
	}

	var held Manifest

	err = json.Unmarshal(data, &held)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w %s: %w", ErrManifest, path, err)
	}

	return held, nil
}

// SetManifest replaces the store's manifest whole.
func (store *Store) SetManifest(held Manifest) error {
	data, err := json.Marshal(held)
	if err != nil {
		return fmt.Errorf("encoding the vector store manifest: %w", err)
	}

	err = safefs.MkdirAll(store.root, store.dir, 0o700)
	if err != nil {
		return fmt.Errorf("creating the vector segments: %w", err)
	}

	return atomicfile.Write(store.root, filepath.Join(store.dir, manifestName), data, 0o600)
}

func (store *Store) names() ([]string, error) {
	listed, err := os.ReadDir(store.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("listing the vector segments: %w", err)
	}

	var names []string

	for _, entry := range listed {
		if strings.HasSuffix(entry.Name(), segmentExt) && !strings.HasPrefix(entry.Name(), ".") {
			names = append(names, entry.Name())
		}
	}

	return names, nil
}

// Write adds rows as one new segment, then merges under the store's lock.
// live decides which rows a merge keeps.
func (store *Store) Write(rows []Row, live Live) error {
	dims := 0

	for _, held := range rows {
		if len(held.Chunks) > 0 {
			dims = len(held.Chunks[0])

			break
		}
	}

	if dims == 0 {
		return nil
	}

	err := safefs.MkdirAll(store.root, store.dir, 0o700)
	if err != nil {
		return fmt.Errorf("creating the vector segments: %w", err)
	}

	unlock, err := store.lock()
	if err != nil {
		return err
	}
	defer unlock()

	_, err = store.writeSegment(dims, rows)
	if err != nil {
		return err
	}

	return store.merge(live)
}

func (store *Store) lock() (func(), error) {
	file, err := safefs.Lock(store.root, filepath.Join(store.dir, lockName), 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening the vector segment lock: %w", err)
	}

	err = filelock.Exclusive(file)
	if err != nil {
		_ = file.Close()

		return nil, fmt.Errorf("locking the vector segments: %w", err)
	}

	return func() {
		_ = filelock.Unlock(file)
		_ = file.Close()
	}, nil
}

// TryFill takes the store's fill lock without waiting, as sdd's lazy index
// fill does: a process finding another one filling embeds nothing and reads
// the rows held so far. It answers false while another process holds it.
func (store *Store) TryFill() (release func(), taken bool, err error) {
	err = safefs.MkdirAll(store.root, store.dir, 0o700)
	if err != nil {
		return nil, false, fmt.Errorf("creating the vector segments: %w", err)
	}

	file, err := safefs.Lock(store.root, filepath.Join(store.dir, fillName), 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("opening the vector fill lock: %w", err)
	}

	taken, err = filelock.TryExclusive(file)
	if err != nil {
		_ = file.Close()

		return nil, false, fmt.Errorf("locking the vector fill: %w", err)
	}

	if !taken {
		_ = file.Close()

		return nil, false, nil
	}

	return func() {
		_ = filelock.Unlock(file)
		_ = file.Close()
	}, true, nil
}

// writeSegment names a segment by its content, so two writers filing the same
// rows publish one file.
func (store *Store) writeSegment(dims int, rows []Row) (string, error) {
	data, err := encode(dims, rows)
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(data)
	name := hex.EncodeToString(sum[:8]) + segmentExt

	folder, err := safefs.At(store.root, store.dir)
	if err != nil {
		return "", fmt.Errorf("writing a vector segment: %w", err)
	}

	defer folder.Close()

	err = folder.ReplaceWith(name, tempPrefix+"*", 0o600, func(temporary *os.File) error {
		_, err := temporary.Write(data)

		return err
	})
	if err != nil {
		return "", fmt.Errorf("writing a vector segment: %w", err)
	}

	return name, nil
}

type held struct {
	name    string
	segment *segment
	live    []row
	dead    int
}

// merge runs under the store's lock, where every temporary file is one a
// killed writer left behind.
func (store *Store) merge(live Live) error {
	err := store.removeStrays()
	if err != nil {
		return err
	}

	names, err := store.names()
	if err != nil {
		return err
	}

	var segments []held

	defer func() {
		for _, each := range segments {
			_ = each.segment.release()
		}
	}()

	seen := map[string]bool{}

	for _, name := range names {
		opened, err := openSegment(filepath.Join(store.dir, name))
		if errors.Is(err, ErrCorrupt) {
			removeIfThere(filepath.Join(store.dir, name))

			continue
		}

		if err != nil {
			return err
		}

		each := held{name: name, segment: opened}

		for _, candidate := range opened.rows {
			key := candidate.id + "\x00" + candidate.hash
			if seen[key] || !live(candidate.id, candidate.hash) {
				each.dead++

				continue
			}

			seen[key] = true
			each.live = append(each.live, candidate)
		}

		segments = append(segments, each)
	}

	stats := make([]segmentStat, len(segments))
	for index, each := range segments {
		stats[index] = segmentStat{live: len(each.live), dead: each.dead}
	}

	for _, group := range planMerge(stats) {
		err = store.rewrite(segments, group)
		if err != nil {
			return err
		}
	}

	return nil
}

// rewrite writes the live rows of a group as one segment before it unlinks the
// group's files, so a crash between the two leaves copies a reader dedupes.
func (store *Store) rewrite(segments []held, group []int) error {
	var rows []Row

	dims := 0

	err := guardFault(func() error {
		for _, index := range group {
			each := segments[index]
			dims = each.segment.dims

			for _, kept := range each.live {
				chunks := make([][]float32, kept.count)
				for chunk := range chunks {
					start := (kept.first + chunk) * each.segment.dims
					chunks[chunk] = slices.Clone(each.segment.vectors[start : start+each.segment.dims])
				}

				rows = append(rows, Row{ID: kept.id, Hash: kept.hash, Chunks: chunks})
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	written := ""
	if len(rows) > 0 {
		written, err = store.writeSegment(dims, rows)
		if err != nil {
			return err
		}
	}

	for _, index := range group {
		if segments[index].name != written {
			removeIfThere(filepath.Join(store.dir, segments[index].name))
		}
	}

	return nil
}

func (store *Store) removeStrays() error {
	listed, err := os.ReadDir(store.dir)
	if err != nil {
		return fmt.Errorf("listing the vector segments: %w", err)
	}

	for _, entry := range listed {
		if strings.HasPrefix(entry.Name(), tempPrefix) {
			removeIfThere(filepath.Join(store.dir, entry.Name()))
		}
	}

	return nil
}

func removeIfThere(path string) {
	_ = os.Remove(path)
}

type segmentStat struct {
	live int
	dead int
}

// planMerge groups the segments stats describes into the segments a merge
// writes: one whose dead rows outnumber its live ones is rewritten alone, which
// for a segment with no live row writes nothing, and a size class holding
// mergeRatio segments merges into one, the result taking its own class, so each
// class holds fewer than mergeRatio.
func planMerge(stats []segmentStat) [][]int {
	type pending struct {
		members []int
		live    int
		rewrite bool
	}

	var items []pending

	for index, stat := range stats {
		items = append(items, pending{members: []int{index}, live: stat.live, rewrite: stat.dead > stat.live})
	}

	for {
		classes := map[int][]int{}
		lowest := math.MaxInt

		for index, item := range items {
			class := sizeClass(item.live)
			classes[class] = append(classes[class], index)

			if len(classes[class]) >= mergeRatio && class < lowest {
				lowest = class
			}
		}

		if lowest == math.MaxInt {
			break
		}

		folded := pending{rewrite: true}

		for _, index := range classes[lowest] {
			folded.members = append(folded.members, items[index].members...)
			folded.live += items[index].live
		}

		items = slices.DeleteFunc(items, func(item pending) bool {
			return slices.Contains(folded.members, item.members[0])
		})
		items = append(items, folded)
	}

	var groups [][]int

	for _, item := range items {
		if item.rewrite {
			groups = append(groups, item.members)
		}
	}

	return groups
}

func sizeClass(live int) int {
	class := 0
	for live >= mergeRatio {
		live /= mergeRatio
		class++
	}

	return class
}
