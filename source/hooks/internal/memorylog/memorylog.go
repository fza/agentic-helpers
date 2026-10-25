// Package memorylog is the append-only event log behind agentic-memory.
//
// A log directory holds an open segment, `tail.jsonl`, one JSON event per
// line; sealed, immutable segments named `<sha256[:8]>.seg`; `manifest.json`
// listing the sealed segments oldest first; and `.lock`, taken around every
// write. Readers take no lock: they map the sealed segments and read the tail,
// retrying while a write moves files under them.
//
// A seal commits through the manifest. The manifest naming the new segment also
// records the digest and size of the tail bytes it sealed, until the tail has
// been emptied and the manifest rewritten without that record. A reader skips a
// tail still carrying those bytes, and the next locked write empties it, so a
// crash at any step neither loses an event nor duplicates one.
package memorylog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
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
)

// kinds is the segment encoding of a kind: its index here. Only ever append.
var kinds = []Kind{
	KindNew, KindRetire, KindSupersede, KindMerge, KindConfirm, KindRetain, KindRevive, KindRetitle, KindTag,
}

const (
	SealBytes   = 65536
	MaxSegments = 8
)

const (
	tailName     = "tail.jsonl"
	manifestName = "manifest.json"
	lockName     = ".lock"
	tempPrefix   = ".tmp-"
	segmentExt   = ".seg"
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
	dir string
	// writing serializes the writers sharing this Log: flock is held per open
	// file description, so it alone does not exclude a goroutine of the same
	// process holding the same descriptor.
	writing sync.Mutex
}

func Open(dir string) (*Log, error) {
	err := os.MkdirAll(dir, 0o700)
	if err != nil {
		return nil, fmt.Errorf("creating the log directory: %w", err)
	}

	return &Log{dir: dir}, nil
}

func (l *Log) Append(ctx context.Context, events ...Event) error {
	return l.AppendWith(ctx, nil, events...)
}

// AppendWith appends events to the tail, seals the tail once it outgrows
// SealBytes, and merges every sealed segment through policy once a seal leaves
// MaxSegments of them. A nil policy keeps every event.
func (l *Log) AppendWith(ctx context.Context, policy Policy, events ...Event) error {
	lines, err := encodeLines(events)
	if err != nil {
		return err
	}

	return l.locked(ctx, func(current manifest) error {
		size, err := l.appendTail(lines)
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

func (l *Log) Merge(ctx context.Context, policy Policy) error {
	return l.locked(ctx, func(current manifest) error {
		return l.merge(current, policy)
	})
}

// locked runs write holding the log lock, on a repaired log. The context is
// checked once the lock is held: a flock wait cannot be interrupted.
func (l *Log) locked(ctx context.Context, write func(current manifest) error) error {
	l.writing.Lock()
	defer l.writing.Unlock()

	lock, err := os.OpenFile(l.path(lockName), os.O_CREATE|os.O_RDWR, 0o600)
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

func (l *Log) appendTail(lines []byte) (int64, error) {
	_, err := os.Stat(l.path(tailName))
	created := errors.Is(err, os.ErrNotExist)

	tail, err := os.OpenFile(l.path(tailName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 0, fmt.Errorf("opening the tail: %w", err)
	}
	defer tail.Close()

	_, err = tail.Write(lines)
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

// repair returns the log to a state every write may start from, after a
// crash at any step of an earlier one: stray temp files and unlisted segments
// go, a tail still holding sealed events is emptied, a torn last line is cut.
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

	err = l.cutTornLine()
	if err != nil {
		return manifest{}, err
	}

	return current, nil
}

func (l *Log) finishSeal(current manifest) error {
	tail, err := os.ReadFile(l.path(tailName))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reading the tail: %w", err)
	}

	rest, sealed := current.Sealing.strip(tail)
	if sealed {
		err = l.replaceFile(tailName, rest)
		if err != nil {
			return fmt.Errorf("emptying the sealed tail: %w", err)
		}
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

func (l *Log) cutTornLine() error {
	tail, err := os.ReadFile(l.path(tailName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("reading the tail: %w", err)
	}

	complete := completeLines(tail)
	if len(complete) == len(tail) {
		return nil
	}

	err = os.Truncate(l.path(tailName), int64(len(complete)))
	if err != nil {
		return fmt.Errorf("cutting a torn tail line: %w", err)
	}

	return nil
}

// replaceFile swaps content in under name: a temp file in the log directory,
// synced, renamed over name, then the directory synced.
func (l *Log) replaceFile(name string, content []byte) error {
	temp, err := os.CreateTemp(l.dir, tempPrefix+"*")
	if err != nil {
		return fmt.Errorf("creating a temp file: %w", err)
	}

	_, err = temp.Write(content)
	if err == nil {
		err = temp.Sync()
	}

	closeErr := temp.Close()
	if err == nil {
		err = closeErr
	}

	if err == nil {
		err = os.Rename(temp.Name(), l.path(name))
	}

	if err != nil {
		_ = os.Remove(temp.Name())

		return fmt.Errorf("writing %s: %w", name, err)
	}

	return syncDir(l.dir)
}
