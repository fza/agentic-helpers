package vectorseg

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"runtime/debug"
	"sync"

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
)

// snapshotAttempts bounds how often a snapshot lists the store again after a
// merge unlinked a segment between its listing and its open.
const snapshotAttempts = 16

type segment struct {
	path    string
	dims    int
	rows    []row
	vectors []float32
	unmap   func() error
	refs    int
}

func openSegment(path string) (*segment, error) {
	data, unmap, err := mapFile(path)
	if err != nil {
		return nil, err
	}

	var decoded parsed

	err = guardFault(func() error {
		var parseErr error

		decoded, parseErr = parse(data)

		return parseErr
	})
	if err != nil {
		_ = unmap()

		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return &segment{path: path, dims: decoded.dims, rows: decoded.rows, vectors: decoded.vectors, unmap: unmap, refs: 1}, nil
}

func (opened *segment) release() error {
	opened.refs--
	if opened.refs > 0 {
		return nil
	}

	return opened.unmap()
}

// mapped keeps each segment a process mapped, holding a reference of its own,
// so a long-running process maps a segment once across its searches.
var mapped = struct {
	sync.Mutex
	segments map[string]*segment
}{segments: map[string]*segment{}}

// Snapshot is the store's segments as listed when it was taken. It stays
// readable whatever merges unlink meanwhile, until Close.
type Snapshot struct {
	segments []*segment
	holds    map[string]bool
}

func (store *Store) Snapshot() (*Snapshot, error) {
	for range snapshotAttempts {
		snapshot, err := store.snapshot()
		if !errors.Is(err, fs.ErrNotExist) {
			return snapshot, err
		}
	}

	return nil, fmt.Errorf("listing the vector segments: they moved on every one of %d tries", snapshotAttempts)
}

func (store *Store) snapshot() (*Snapshot, error) {
	names, err := store.names()
	if err != nil {
		return nil, err
	}

	mapped.Lock()
	defer mapped.Unlock()

	snapshot := &Snapshot{}
	listed := map[string]bool{}

	for _, name := range names {
		path := filepath.Join(store.dir, name)
		listed[path] = true

		opened := mapped.segments[path]
		if opened == nil {
			opened, err = openSegment(path)
			if errors.Is(err, ErrCorrupt) {
				continue
			}

			if err != nil {
				snapshot.releaseLocked()

				return nil, err
			}

			mapped.segments[path] = opened
		}

		opened.refs++
		snapshot.segments = append(snapshot.segments, opened)
	}

	for path, opened := range mapped.segments {
		if filepath.Dir(path) == store.dir && !listed[path] {
			delete(mapped.segments, path)
			_ = opened.release()
		}
	}

	return snapshot, nil
}

func (snapshot *Snapshot) Close() error {
	mapped.Lock()
	defer mapped.Unlock()

	return snapshot.releaseLocked()
}

func (snapshot *Snapshot) releaseLocked() error {
	var errs []error

	for _, opened := range snapshot.segments {
		errs = append(errs, opened.release())
	}

	snapshot.segments = nil

	return errors.Join(errs...)
}

// Holds says whether a segment holds the rows embedded from hash for id.
func (snapshot *Snapshot) Holds(id string, hash string) bool {
	if snapshot.holds == nil {
		snapshot.holds = map[string]bool{}

		for _, opened := range snapshot.segments {
			for _, held := range opened.rows {
				snapshot.holds[held.id+"\x00"+held.hash] = true
			}
		}
	}

	return snapshot.holds[id+"\x00"+hash]
}

// Scores is each counted entry's best cosine against query, over its chunks.
// The first counted row of an entry decides: rows sharing a hash were embedded
// from the same content.
func (snapshot *Snapshot) Scores(query []float32, live Live) (map[string]float32, error) {
	scores := map[string]float32{}

	err := guardFault(func() error {
		for _, opened := range snapshot.segments {
			if opened.dims != len(query) {
				continue
			}

			for _, held := range opened.rows {
				if _, scored := scores[held.id]; scored || held.count == 0 || !live(held.id, held.hash) {
					continue
				}

				best := float32(-1)

				for chunk := held.first; chunk < held.first+held.count; chunk++ {
					score := embed.Cosine(query, opened.vectors[chunk*opened.dims:(chunk+1)*opened.dims])
					if score > best {
						best = score
					}
				}

				scores[held.id] = best
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return scores, nil
}

// guardFault runs read over mapped segments, turning a fault in a mapping (its
// file truncated or its storage gone) into ErrFault. Any other panic is a bug
// and stays one.
func guardFault(read func() error) (err error) {
	previous := debug.SetPanicOnFault(true)

	defer func() {
		debug.SetPanicOnFault(previous)

		recovered := recover()
		if recovered == nil {
			return
		}

		fault, isFault := recovered.(interface{ Addr() uintptr })
		if !isFault {
			panic(recovered)
		}

		err = fmt.Errorf("%w at %#x", ErrFault, fault.Addr())
	}()

	return read()
}
