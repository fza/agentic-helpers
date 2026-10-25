package memorylog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

type tailLine struct {
	Kind    Kind            `json:"kind"`
	Entry   string          `json:"entry"`
	Time    int64           `json:"time"`
	Seat    string          `json:"seat"`
	Target  string          `json:"target"`
	Payload json.RawMessage `json:"payload"`
}

func encodeLines(events []Event) ([]byte, error) {
	var lines bytes.Buffer

	encoder := json.NewEncoder(&lines)
	encoder.SetEscapeHTML(false)

	for _, event := range events {
		if !slices.Contains(kinds, event.Kind) {
			return nil, fmt.Errorf("%w: %q", ErrUnknownKind, event.Kind)
		}

		line := tailLine{
			Kind:   event.Kind,
			Entry:  event.Entry,
			Time:   event.Time.UnixNano(),
			Seat:   event.Seat,
			Target: event.Target,
		}
		if len(event.Payload) > 0 {
			line.Payload = event.Payload
		}

		err := encoder.Encode(line)
		if err != nil {
			return nil, fmt.Errorf("encoding the %s event of %s: %w", event.Kind, event.Entry, err)
		}
	}

	return lines.Bytes(), nil
}

// completeLines is tail up to its last newline: a line without one is a write
// still under way, or one a crash tore.
func completeLines(tail []byte) []byte {
	end := bytes.LastIndexByte(tail, '\n')

	return tail[:end+1]
}

func decodeLines(tail []byte) ([]Event, error) {
	var events []Event

	for number, line := range bytes.Split(completeLines(tail), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}

		var decoded tailLine

		err := json.Unmarshal(line, &decoded)
		if err != nil {
			return nil, fmt.Errorf("%w: tail line %d: %w", ErrCorrupt, number+1, err)
		}

		if !slices.Contains(kinds, decoded.Kind) {
			return nil, fmt.Errorf("%w: tail line %d: %w: %q", ErrCorrupt, number+1, ErrUnknownKind, decoded.Kind)
		}

		events = append(events, Event{
			Kind:    decoded.Kind,
			Entry:   decoded.Entry,
			Time:    time.Unix(0, decoded.Time).UTC(),
			Seat:    decoded.Seat,
			Target:  decoded.Target,
			Payload: normalPayload(decoded.Payload),
		})
	}

	return events, nil
}

func normalPayload(payload []byte) []byte {
	if len(payload) == 0 || string(payload) == "null" {
		return nil
	}

	return payload
}
