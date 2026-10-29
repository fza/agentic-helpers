package memorylog

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"time"
)

// Dump prints the events of a segment, a journal tail or a `tail.jsonl` an
// earlier release left, one per line: time, kind, entry, seat, target and
// payload, a dash standing for an empty field.
func Dump(ctx context.Context, w io.Writer, path string) error {
	err := ctx.Err()
	if err != nil {
		return fmt.Errorf("dumping %s: %w", path, err)
	}

	data, release, err := mapFile(path)
	if err != nil {
		return err
	}
	defer release()

	var events []Event

	switch {
	case bytes.HasPrefix(data, []byte(segmentMagic)):
		err = guardFault(func() error {
			var decodeErr error

			events, decodeErr = decodeSegmentEvents(data)

			return decodeErr
		})
	case IsJournal(data):
		err = guardFault(func() error {
			var decodeErr error

			events, decodeErr = decodeTail(data)

			return decodeErr
		})
	default:
		err = guardFault(func() error {
			var decodeErr error

			events, decodeErr = decodeLines(data)

			return decodeErr
		})
	}

	if err != nil {
		return fmt.Errorf("decoding %s: %w", path, err)
	}

	out := bufio.NewWriter(w)

	for _, event := range events {
		fmt.Fprintf(out, "%s %s %s %s %s %s\n",
			event.Time.UTC().Format(time.RFC3339), event.Kind,
			orDash(event.Entry), orDash(event.Seat), orDash(event.Target), orDash(string(event.Payload)))
	}

	err = out.Flush()
	if err != nil {
		return fmt.Errorf("writing the dump of %s: %w", path, err)
	}

	return nil
}

// SegmentEvents is every event of a sealed segment's bytes.
func SegmentEvents(data []byte) ([]Event, error) {
	var events []Event

	err := guardFault(func() error {
		var decodeErr error

		events, decodeErr = decodeSegmentEvents(data)

		return decodeErr
	})

	return events, err
}

// IsSegment is whether data opens with a sealed segment's header.
func IsSegment(data []byte) bool {
	return bytes.HasPrefix(data, []byte(segmentMagic))
}

func decodeSegmentEvents(data []byte) ([]Event, error) {
	rows, err := decodeSegment(data)
	if err != nil {
		return nil, err
	}

	events := make([]Event, 0, len(rows))
	for _, row := range rows {
		events = append(events, Event{
			Kind: row.Kind, Entry: row.Entry, Time: row.Time, Seat: row.Seat, Target: row.Target,
			Payload: normalPayload(bytes.Clone(row.payload)),
		})
	}

	return events, nil
}

func orDash(field string) string {
	if field == "" {
		return "-"
	}

	return field
}
