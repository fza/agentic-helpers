package memorylog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

// tailLine is a line of the `tail.jsonl` earlier releases kept.
type tailLine struct {
	Kind    Kind            `json:"kind"`
	Entry   string          `json:"entry"`
	Time    lineTime        `json:"time"`
	Seat    string          `json:"seat"`
	Target  string          `json:"target"`
	Payload json.RawMessage `json:"payload"`
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
			Time:    time.Time(decoded.Time).UTC(),
			Seat:    decoded.Seat,
			Target:  decoded.Target,
			Payload: normalPayload(decoded.Payload),
		})
	}

	return events, nil
}

// lineTime is a tail line's time: written as RFC 3339 in UTC with every
// nanosecond, so a person reads it as a date and a row reads back to the instant
// it held; read as that, or as the Unix nanoseconds earlier releases wrote.
type lineTime time.Time

func (at lineTime) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Time(at).UTC().Format(time.RFC3339Nano))
}

func (at *lineTime) UnmarshalJSON(raw []byte) error {
	var nanos int64

	err := json.Unmarshal(raw, &nanos)
	if err == nil {
		*at = lineTime(time.Unix(0, nanos).UTC())

		return nil
	}

	var text string

	err = json.Unmarshal(raw, &text)
	if err != nil {
		return fmt.Errorf("a time that is neither RFC 3339 nor Unix nanoseconds: %s", raw)
	}

	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return fmt.Errorf("reading the time %q: %w", text, err)
	}

	*at = lineTime(parsed.UTC())

	return nil
}

func normalPayload(payload []byte) []byte {
	if len(payload) == 0 || string(payload) == "null" {
		return nil
	}

	return payload
}
