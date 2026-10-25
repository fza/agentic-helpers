package memorylog

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"time"
)

// Dump prints the events of a segment or a tail, one per line: time, kind,
// entry, seat, target and payload, a dash standing for an empty field.
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

	if bytes.HasPrefix(data, []byte(segmentMagic)) {
		err = guardFault(func() error {
			var decodeErr error

			events, decodeErr = decodeSegmentEvents(data)

			return decodeErr
		})
	} else {
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
