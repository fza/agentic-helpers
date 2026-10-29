package memorylog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"slices"
)

// seal moves every tail event into a new segment. Each step leaves a state
// repair settles: the segment is a stray until the manifest lists it, and the
// manifest names the tail bytes it sealed until the tail no longer holds them.
func (l *Log) seal(current manifest) (manifest, error) {
	tail, err := os.ReadFile(l.path(tailName))
	if err != nil {
		return manifest{}, fmt.Errorf("reading the tail to seal: %w", err)
	}

	events, err := decodeTail(tail)
	if err != nil {
		return manifest{}, err
	}

	name, err := l.writeSegment(events)
	if err != nil {
		return manifest{}, err
	}

	next := manifest{Segments: append(slices.Clone(current.Segments), name), Sealing: newSealing(tail)}

	err = l.writeManifest(next)
	if err != nil {
		return manifest{}, fmt.Errorf("listing the sealed segment: %w", err)
	}

	err = l.finishSeal(next)
	if err != nil {
		return manifest{}, err
	}

	next.Sealing = nil

	return next, nil
}

// merge replaces every sealed segment with one holding what policy keeps. The
// new segment is a stray until the manifest lists it alone; the old ones are
// strays from then on.
func (l *Log) merge(current manifest, policy Policy) error {
	kept, err := l.keep(current, policy)
	if err != nil {
		return err
	}

	next := manifest{Segments: []string{}}

	if len(kept) > 0 {
		name, err := l.writeSegment(kept)
		if err != nil {
			return err
		}

		next.Segments = []string{name}
	}

	err = l.writeManifest(next)
	if err != nil {
		return fmt.Errorf("listing the merged segment: %w", err)
	}

	return l.removeStrays(next)
}

func (l *Log) keep(current manifest, policy Policy) ([]Event, error) {
	sealed, err := l.mapSegments(current.Segments)
	if errors.Is(err, errMoved) {
		return nil, fmt.Errorf("%w: a listed segment is missing", ErrCorrupt)
	}

	if err != nil {
		return nil, err
	}
	defer sealed.close()

	if policy == nil {
		return sealed.events()
	}

	kept, err := policy(sealed.headers(), sealed.payload)
	if err != nil {
		return nil, fmt.Errorf("running the merge policy: %w", err)
	}

	return kept, nil
}

func (l *Log) writeSegment(events []Event) (string, error) {
	segment, err := encodeSegment(events)
	if err != nil {
		return "", err
	}

	digest := sha256.Sum256(segment)
	name := hex.EncodeToString(digest[:4]) + segmentExt

	existing, err := os.ReadFile(l.path(name))
	if err == nil {
		if !bytes.Equal(existing, segment) {
			return "", fmt.Errorf("%w: %s", ErrCollision, name)
		}

		return name, nil
	}

	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("checking for %s: %w", name, err)
	}

	err = l.replaceFile(name, segment)
	if err != nil {
		return "", err
	}

	return name, nil
}

// decodeTail is every event of a journal tail, up to where its read stops.
func decodeTail(tail []byte) ([]Event, error) {
	records, _, err := ReadJournal(tail)
	if errors.Is(err, ErrNotJournal) {
		return nil, fmt.Errorf("%w: the tail: %w", ErrCorrupt, err)
	}

	return eventsOf(records)
}
