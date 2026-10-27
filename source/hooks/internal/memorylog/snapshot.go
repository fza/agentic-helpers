package memorylog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"
)

const snapshotAttempts = 16

// errMoved is a write replacing files under a reader, which then starts over.
var errMoved = errors.New("the log moved under the reader")

// Snapshot is the log as it stood when taken: sealed segments mapped, tail
// read. It stays valid while writers go on, until Close.
type Snapshot struct {
	dir      string
	sealed   sealedSet
	segments []SegmentStamp
	tail     []Event
	manifest os.FileInfo
	tailInfo os.FileInfo
	closed   bool
}

// SegmentStamp is a sealed segment as a reader found it: its name, size and
// modification time. A reader keeping something derived from the sealed
// segments keys it by their stamps.
type SegmentStamp struct {
	Name    string
	Size    int64
	ModTime time.Time
}

// Segments is the stamp of every sealed segment the snapshot holds, in order.
func (s *Snapshot) Segments() []SegmentStamp {
	return slices.Clone(s.segments)
}

// Sealed is how many of Rows the sealed segments hold; the rest are the
// tail's, in the order they were appended.
func (s *Snapshot) Sealed() int {
	return s.sealed.count()
}

func (l *Log) stampSegments(names []string) ([]SegmentStamp, error) {
	stamps := make([]SegmentStamp, 0, len(names))

	for _, name := range names {
		info, err := os.Stat(l.path(name))
		if errors.Is(err, os.ErrNotExist) {
			return nil, errMoved
		}

		if err != nil {
			return nil, fmt.Errorf("measuring %s: %w", name, err)
		}

		stamps = append(stamps, SegmentStamp{Name: name, Size: info.Size(), ModTime: info.ModTime()})
	}

	return stamps, nil
}

// Unsealed is the stamps of the log's sealed segments and the events of its
// tail as they stand, read without mapping a segment: what a reader holding
// something derived from the sealed segments needs to take in only what was
// appended since.
func (l *Log) Unsealed(ctx context.Context) ([]SegmentStamp, []Event, error) {
	err := ctx.Err()
	if err != nil {
		return nil, nil, fmt.Errorf("reading the tail: %w", err)
	}

	for range snapshotAttempts {
		current, manifestInfo, err := readManifest(l.path(manifestName))
		if err != nil {
			return nil, nil, err
		}

		stamps, err := l.stampSegments(current.Segments)
		if errors.Is(err, errMoved) {
			continue
		}

		if err != nil {
			return nil, nil, err
		}

		reading := &Snapshot{dir: l.dir}

		err = reading.readTail(current.Sealing)
		if err != nil {
			return nil, nil, err
		}

		after, err := statFile(l.path(manifestName))
		if err != nil {
			return nil, nil, err
		}

		if sameFile(manifestInfo, after) {
			return stamps, reading.tail, nil
		}
	}

	return nil, nil, ErrUnstable
}

func (l *Log) Snapshot(ctx context.Context) (*Snapshot, error) {
	err := ctx.Err()
	if err != nil {
		return nil, fmt.Errorf("taking a snapshot: %w", err)
	}

	for range snapshotAttempts {
		snapshot, err := l.read()
		if errors.Is(err, errMoved) {
			continue
		}

		if err != nil {
			return nil, err
		}

		return snapshot, nil
	}

	return nil, ErrUnstable
}

// read takes the manifest, every segment it lists, then the tail, and checks
// the manifest is still the one it took. A seal replaces the manifest before
// it empties the tail, and a merge before it removes segments, so an unchanged
// manifest means the tail read belongs to it.
func (l *Log) read() (*Snapshot, error) {
	current, manifestInfo, err := readManifest(l.path(manifestName))
	if err != nil {
		return nil, err
	}

	sealed, err := l.mapSegments(current.Segments)
	if err != nil {
		return nil, err
	}

	snapshot := &Snapshot{dir: l.dir, sealed: sealed, manifest: manifestInfo}

	snapshot.segments, err = l.stampSegments(current.Segments)
	if err != nil {
		_ = sealed.close()

		return nil, err
	}

	err = snapshot.readTail(current.Sealing)
	if err != nil {
		_ = sealed.close()

		return nil, err
	}

	after, err := statFile(l.path(manifestName))
	if err != nil {
		_ = sealed.close()

		return nil, err
	}

	if !sameFile(manifestInfo, after) {
		_ = sealed.close()

		return nil, errMoved
	}

	return snapshot, nil
}

func (s *Snapshot) readTail(sealing *sealing) error {
	file, err := os.Open(s.path(tailName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("opening the tail: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("measuring the tail: %w", err)
	}

	// Only the bytes the stat counted, so Fresh compares against what was read.
	tail := make([]byte, info.Size())

	_, err = io.ReadFull(file, tail)
	if err != nil {
		return fmt.Errorf("reading the tail: %w", err)
	}

	tail, _ = sealing.strip(tail)

	events, err := decodeLines(tail)
	if err != nil {
		return err
	}

	s.tail = events
	s.tailInfo = info

	return nil
}

func (s *Snapshot) path(name string) string {
	return filepath.Join(s.dir, name)
}

func (s *Snapshot) Rows() []Row {
	rows := s.sealed.headers()
	for _, event := range s.tail {
		rows = append(rows, Row{Kind: event.Kind, Entry: event.Entry, Time: event.Time, Seat: event.Seat, Target: event.Target})
	}

	return rows
}

func (s *Snapshot) Payload(index int) ([]byte, error) {
	if s.closed {
		return nil, ErrClosed
	}

	if index >= s.sealed.count() && index-s.sealed.count() < len(s.tail) {
		return slices.Clone(s.tail[index-s.sealed.count()].Payload), nil
	}

	return s.sealed.payload(index)
}

// Fresh is whether the manifest and the tail are the very files, unchanged, the
// snapshot read. A long-running reader takes a new snapshot once it is not.
func (s *Snapshot) Fresh() bool {
	manifestInfo, err := statFile(s.path(manifestName))
	if err != nil || !sameFile(s.manifest, manifestInfo) {
		return false
	}

	tailInfo, err := statFile(s.path(tailName))

	return err == nil && sameFile(s.tailInfo, tailInfo)
}

func (s *Snapshot) Close() error {
	if s.closed {
		return nil
	}

	s.closed = true

	return s.sealed.close()
}

type mappedSegment struct {
	rows    []sealedRow
	release func() error
}

type sealedSet []mappedSegment

func (l *Log) mapSegments(names []string) (sealedSet, error) {
	sealed := make(sealedSet, 0, len(names))

	for _, name := range names {
		segment, err := mapSegment(l.path(name))
		if err != nil {
			_ = sealed.close()

			return nil, err
		}

		sealed = append(sealed, segment)
	}

	return sealed, nil
}

func mapSegment(path string) (mappedSegment, error) {
	data, release, err := mapFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return mappedSegment{}, errMoved
	}

	if err != nil {
		return mappedSegment{}, err
	}

	var rows []sealedRow

	err = guardFault(func() error {
		var decodeErr error

		rows, decodeErr = decodeSegment(data)

		return decodeErr
	})
	if err != nil {
		_ = release()

		return mappedSegment{}, fmt.Errorf("decoding %s: %w", path, err)
	}

	return mappedSegment{rows: rows, release: release}, nil
}

func (sealed sealedSet) count() int {
	total := 0
	for _, segment := range sealed {
		total += len(segment.rows)
	}

	return total
}

func (sealed sealedSet) headers() []Row {
	rows := make([]Row, 0, sealed.count())
	for _, segment := range sealed {
		for _, row := range segment.rows {
			rows = append(rows, row.Row)
		}
	}

	return rows
}

func (sealed sealedSet) row(index int) (sealedRow, error) {
	if index >= 0 {
		for _, segment := range sealed {
			if index < len(segment.rows) {
				return segment.rows[index], nil
			}

			index -= len(segment.rows)
		}
	}

	return sealedRow{}, ErrNoRow
}

func (sealed sealedSet) payload(index int) ([]byte, error) {
	row, err := sealed.row(index)
	if err != nil {
		return nil, fmt.Errorf("reading payload %d: %w", index, err)
	}

	if len(row.payload) == 0 {
		return nil, nil
	}

	var payload []byte

	err = guardFault(func() error {
		payload = slices.Clone(row.payload)

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading payload %d: %w", index, err)
	}

	return payload, nil
}

func (sealed sealedSet) events() ([]Event, error) {
	events := make([]Event, 0, sealed.count())

	for index, row := range sealed.headers() {
		payload, err := sealed.payload(index)
		if err != nil {
			return nil, err
		}

		events = append(events, Event{
			Kind: row.Kind, Entry: row.Entry, Time: row.Time, Seat: row.Seat, Target: row.Target, Payload: payload,
		})
	}

	return events, nil
}

func (sealed sealedSet) close() error {
	var errs []error
	for _, segment := range sealed {
		errs = append(errs, segment.release())
	}

	return errors.Join(errs...)
}
