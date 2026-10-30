// Package memorylog is the append-only event log behind agentic-memory.
//
// A log directory holds an open segment, `tail.jnl`, a journal of one event
// record each (journal.go); sealed, immutable segments named
// `<sha256[:8]>.seg`; `manifest.json` listing the sealed segments oldest
// first; and `.lock`, taken around every write. Readers take no lock: they map
// the sealed segments and read the tail, retrying while a write moves files
// under them.
//
// A seal commits through the manifest. The manifest naming the new segment also
// records the digest and size of the tail bytes it sealed, until the tail has
// been emptied and the manifest rewritten without that record. A reader skips a
// tail still carrying those bytes, and the next locked write empties it, so a
// crash at any step neither loses an event nor duplicates one.
package memorylog

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
)

type Kind string

const (
	KindNew       Kind = "new"
	KindRetire    Kind = "retire"
	KindSupersede Kind = "supersede"
	KindMerge     Kind = "merge"
	KindConfirm   Kind = "confirm"
	KindRetain    Kind = "retain"
	KindRevive    Kind = "revive"
	KindRetitle   Kind = "retitle"
	KindTag       Kind = "tag"
	KindLink      Kind = "link"
	KindUnlink    Kind = "unlink"
	KindUntag     Kind = "untag"
	// KindState overwrites a seat's state slot and names no entry.
	KindState  Kind = "state"
	KindRekind Kind = "rekind"
	// KindSettle settles a do-now item a rule owed: done by proof or ack, or
	// declined.
	KindSettle Kind = "settle"
	// KindAttach keeps a file with an entry: its name, hash, size and form.
	KindAttach Kind = "attach"
	// KindItem changes one item of a todo's todolist, or their order.
	KindItem Kind = "item"
	// KindDue sets or clears the time an entry comes due.
	KindDue Kind = "due"
)

// kinds is the segment encoding of a kind: its index here. Only ever append.
var kinds = []Kind{
	KindNew, KindRetire, KindSupersede, KindMerge, KindConfirm, KindRetain, KindRevive, KindRetitle, KindTag,
	KindLink, KindUnlink, KindUntag, KindState, KindRekind, KindSettle, KindAttach, KindItem, KindDue,
}

const (
	SealBytes   = 65536
	MaxSegments = 8
)

const (
	// TailName is the open segment's file.
	TailName       = "tail.jnl"
	tailName       = TailName
	legacyTailName = "tail.jsonl"
	manifestName   = "manifest.json"
	lockName       = ".lock"
	tempPrefix     = ".tmp-"
	segmentExt     = ".seg"
)

var (
	ErrUnknownKind = errors.New("unknown event kind")
	ErrCorrupt     = errors.New("corrupt log")
	ErrFault       = errors.New("fault reading a mapped segment")
	ErrUnstable    = errors.New("the log kept changing while being read")
	ErrNoRow       = errors.New("no such row")
	ErrClosed      = errors.New("snapshot closed")
	ErrCollision   = errors.New("segment name already taken by different content")
)

// Event is one change to an entry. Payload is opaque JSON, stored compacted;
// an empty payload and JSON null both read back as nil.
type Event struct {
	Kind    Kind
	Entry   string
	Time    time.Time
	Seat    string
	Target  string
	Payload []byte
}

type Row struct {
	Kind   Kind
	Entry  string
	Time   time.Time
	Seat   string
	Target string
}

// Policy decides what a merge keeps of every sealed row. payload reads the
// payload of rows[index].
type Policy func(rows []Row, payload func(index int) ([]byte, error)) (kept []Event, err error)

type Log struct {
	root string
	dir  string
	// writing serializes the writers sharing this Log: flock is held per open
	// file description, so it alone does not exclude a goroutine of the same
	// process holding the same descriptor.
	writing sync.Mutex
}

func Open(root string, dir string) (*Log, error) {
	err := safefs.MkdirAll(root, dir, 0o700)
	if err != nil {
		return nil, fmt.Errorf("creating the log directory: %w", err)
	}

	return &Log{root: root, dir: dir}, nil
}

func (l *Log) Append(ctx context.Context, events ...Event) error {
	return l.AppendWith(ctx, nil, events...)
}

// AppendWith appends events to the tail, seals the tail once it outgrows
// SealBytes, and merges every sealed segment through policy once a seal leaves
// MaxSegments of them. A nil policy keeps every event.
func (l *Log) AppendWith(ctx context.Context, policy Policy, events ...Event) error {
	framed, err := encodeRecords(events)
	if err != nil {
		return err
	}

	return l.locked(ctx, func(current manifest) error {
		size, err := l.appendTail(framed)
		if err != nil {
			return err
		}

		if size <= SealBytes {
			return nil
		}

		current, err = l.seal(current)
		if err != nil {
			return err
		}

		if len(current.Segments) < MaxSegments {
			return nil
		}

		return l.merge(current, policy)
	})
}

// Build writes events as a whole new log into dir, which must hold no log yet:
// one segment carrying every event, listed by a manifest, and an empty tail.
func Build(ctx context.Context, root string, dir string, events []Event) error {
	entries, err := os.ReadDir(dir)
	if err == nil && len(entries) > 0 {
		return fmt.Errorf("building a log into %s, which already holds one", dir)
	}

	l, err := Open(root, dir)
	if err != nil {
		return err
	}

	return l.locked(ctx, func(current manifest) error {
		next := manifest{Segments: []string{}}

		if len(events) > 0 {
			name, err := l.writeSegment(events)
			if err != nil {
				return err
			}

			next.Segments = []string{name}
		}

		err := l.writeManifest(next)
		if err != nil {
			return fmt.Errorf("listing the built segment: %w", err)
		}

		return l.replaceFile(tailName, nil)
	})
}

func (l *Log) Merge(ctx context.Context, policy Policy) error {
	return l.locked(ctx, func(current manifest) error {
		return l.merge(current, policy)
	})
}

// Rewrite seals the tail, then merges every segment through policy: every
// event the log holds passes the policy, the tail's included.
func (l *Log) Rewrite(ctx context.Context, policy Policy) error {
	return l.locked(ctx, func(current manifest) error {
		info, err := os.Stat(l.path(tailName))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("measuring the tail: %w", err)
		}

		if err == nil && info.Size() > 0 {
			current, err = l.seal(current)
			if err != nil {
				return err
			}
		}

		return l.merge(current, policy)
	})
}

// locked runs write holding the log lock, on a repaired log. The context is
// checked once the lock is held: a flock wait cannot be interrupted.
func (l *Log) locked(ctx context.Context, write func(current manifest) error) error {
	l.writing.Lock()
	defer l.writing.Unlock()

	lock, err := safefs.Lock(l.root, l.path(lockName), 0o600)
	if err != nil {
		return fmt.Errorf("opening the log lock: %w", err)
	}
	defer lock.Close()

	err = lockFile(lock)
	if err != nil {
		return fmt.Errorf("taking the log lock: %w", err)
	}
	defer unlockFile(lock)

	err = ctx.Err()
	if err != nil {
		return fmt.Errorf("holding the log lock: %w", err)
	}

	current, err := l.repair()
	if err != nil {
		return err
	}

	return write(current)
}

func (l *Log) path(name string) string {
	return filepath.Join(l.dir, name)
}

// appendTail appends framed records to the tail, opening an empty one with
// the journal header. The caller holds the log lock, so the append needs no
// atomicity of its own.
func (l *Log) appendTail(framed []byte) (int64, error) {
	if len(framed) == 0 {
		return statSize(l.path(tailName))
	}

	_, err := os.Stat(l.path(tailName))
	created := errors.Is(err, os.ErrNotExist)

	tail, err := safefs.Append(l.root, l.path(tailName), 0o600)
	if err != nil {
		return 0, fmt.Errorf("opening the tail: %w", err)
	}
	defer tail.Close()

	opened, err := tail.Stat()
	if err != nil {
		return 0, fmt.Errorf("measuring the tail: %w", err)
	}

	if opened.Size() == 0 {
		framed = append(JournalHeader(), framed...)
	}

	_, err = tail.Write(framed)
	if err != nil {
		return 0, fmt.Errorf("appending to the tail: %w", err)
	}

	err = tail.Sync()
	if err != nil {
		return 0, fmt.Errorf("syncing the tail: %w", err)
	}

	info, err := tail.Stat()
	if err != nil {
		return 0, fmt.Errorf("measuring the tail: %w", err)
	}

	if created {
		err = syncDir(l.dir)
		if err != nil {
			return 0, err
		}
	}

	return info.Size(), nil
}

// Repair settles the log under its lock: what a crash left of an earlier
// write, and a `tail.jsonl` an earlier release left, sealed into a segment.
func (l *Log) Repair(ctx context.Context) error {
	return l.locked(ctx, func(manifest) error { return nil })
}

// repair returns the log to a state every write may start from, after a
// crash at any step of an earlier one: stray temp files and unlisted segments
// go, a tail still holding sealed events is emptied, a torn last record is
// cut, and a legacy `tail.jsonl` is sealed.
func (l *Log) repair() (manifest, error) {
	current, _, err := readManifest(l.path(manifestName))
	if err != nil {
		return manifest{}, err
	}

	if current.Sealing != nil {
		err = l.finishSeal(current)
		if err != nil {
			return manifest{}, err
		}

		current.Sealing = nil
	}

	err = l.removeStrays(current)
	if err != nil {
		return manifest{}, err
	}

	err = l.cutTornTail()
	if err != nil {
		return manifest{}, err
	}

	return l.sealLegacy(current)
}

func (l *Log) finishSeal(current manifest) error {
	name := tailName
	if current.Sealing.Legacy {
		name = legacyTailName
	}

	tail, err := os.ReadFile(l.path(name))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reading the tail: %w", err)
	}

	rest, sealed := current.Sealing.strip(tail)

	switch {
	case sealed && current.Sealing.Legacy && len(completeLines(rest)) == 0:
		err = os.Remove(l.path(name))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("removing the sealed %s: %w", name, err)
		}
	case sealed && !current.Sealing.Legacy && len(rest) > 0:
		err = l.replaceFile(name, append(JournalHeader(), rest...))
	case sealed:
		err = l.replaceFile(name, rest)
	}

	if err != nil {
		return fmt.Errorf("emptying the sealed tail: %w", err)
	}

	settled := current
	settled.Sealing = nil

	return l.writeManifest(settled)
}

func (l *Log) removeStrays(current manifest) error {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return fmt.Errorf("listing the log directory: %w", err)
	}

	for _, entry := range entries {
		name := entry.Name()

		stray := strings.HasPrefix(name, tempPrefix) ||
			(filepath.Ext(name) == segmentExt && !slices.Contains(current.Segments, name))
		if !stray {
			continue
		}

		err = os.Remove(l.path(name))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("removing the stray %s: %w", name, err)
		}
	}

	return nil
}

// cutTornTail cuts the tail where its read stops: a record a crash tore, or
// one failing its checksum, and every record after it, which no read reaches.
func (l *Log) cutTornTail() error {
	tail, err := os.ReadFile(l.path(tailName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("reading the tail: %w", err)
	}

	_, end, err := ReadJournal(tail)
	if errors.Is(err, ErrNotJournal) {
		return fmt.Errorf("%w: the tail: %w", ErrCorrupt, err)
	}

	if err == nil {
		return nil
	}

	if end <= JournalHeaderSize {
		end = 0
	}

	opened, err := safefs.OpenChecked(l.root, l.path(tailName), os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("cutting a torn tail record: %w", err)
	}

	err = opened.Truncate(int64(end))
	closeErr := opened.Close()

	if err != nil || closeErr != nil {
		return fmt.Errorf("cutting a torn tail record: %w", cmp.Or(err, closeErr))
	}

	return nil
}

// sealLegacy seals a `tail.jsonl` an earlier release left into a segment,
// through the steps of every seal, so a crash at any of them neither loses
// its events nor reads them twice.
func (l *Log) sealLegacy(current manifest) (manifest, error) {
	legacy, err := os.ReadFile(l.path(legacyTailName))
	if errors.Is(err, os.ErrNotExist) {
		return current, nil
	}

	if err != nil {
		return manifest{}, fmt.Errorf("reading %s: %w", legacyTailName, err)
	}

	complete := completeLines(legacy)

	events, err := decodeLines(complete)
	if err != nil {
		return manifest{}, err
	}

	next := current

	if len(events) > 0 {
		name, err := l.writeSegment(events)
		if err != nil {
			return manifest{}, err
		}

		next.Segments = append(slices.Clone(current.Segments), name)
	}

	next.Sealing = newSealing(complete)
	next.Sealing.Legacy = true

	err = l.writeManifest(next)
	if err != nil {
		return manifest{}, fmt.Errorf("listing the sealed %s: %w", legacyTailName, err)
	}

	err = l.finishSeal(next)
	if err != nil {
		return manifest{}, err
	}

	next.Sealing = nil

	return next, nil
}

func statSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}

	if err != nil {
		return 0, fmt.Errorf("measuring %s: %w", filepath.Base(path), err)
	}

	return info.Size(), nil
}

// replaceFile swaps content in under name: a temp file in the log directory,
// synced, renamed over name, then the directory synced.
func (l *Log) replaceFile(name string, content []byte) error {
	folder, err := safefs.At(l.root, l.dir)
	if err != nil {
		return fmt.Errorf("writing %s: %w", name, err)
	}

	defer folder.Close()

	err = folder.ReplaceWith(name, tempPrefix+"*", 0o600, func(temp *os.File) error {
		_, err := temp.Write(content)
		if err == nil {
			err = temp.Sync()
		}

		return err
	})
	if err != nil {
		return fmt.Errorf("writing %s: %w", name, err)
	}

	return syncDir(l.dir)
}
