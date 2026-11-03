package slots

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Fitted is a kind of value a slot holds as JSON. Ceiling bounds its JSON in
// bytes, never in words or runes: a run without spaces is one word of any
// length. Limit applies the kind's caps; Shrink takes the value one step
// toward its minimal form, which always fits, and answers false once there.
// A ceiling stays within MaxValue, so a fitted value is never too large for
// a slot.
type Fitted interface {
	Ceiling() int
	Limit()
	Shrink() bool
}

var ErrOverCeiling = errors.New("the value stays over its kind's ceiling")

// Encode is value's JSON once its caps are applied, shrunk until it stays
// within its kind's ceiling.
func Encode(value Fitted) ([]byte, error) {
	value.Limit()

	for {
		data, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encoding a slot value: %w", err)
		}

		if len(data) <= value.Ceiling() {
			return data, nil
		}

		if !value.Shrink() {
			return nil, fmt.Errorf("%w: %d bytes over %d", ErrOverCeiling, len(data), value.Ceiling())
		}
	}
}

// EncodedLen is how many bytes value takes as JSON.
func EncodedLen(value any) int {
	data, err := json.Marshal(value)
	if err != nil {
		return 0
	}

	return len(data)
}

// CutJSON is the longest start of text, cut at a rune boundary, whose JSON
// string takes at most limit bytes, quotes included.
func CutJSON(text string, limit int) string {
	if EncodedLen(text) <= limit {
		return text
	}

	var boundaries []int
	for index := range text {
		boundaries = append(boundaries, index)
	}

	low, high := 0, len(boundaries)-1
	for low < high {
		middle := (low + high + 1) / 2
		if EncodedLen(text[:boundaries[middle]]) <= limit {
			low = middle
		} else {
			high = middle - 1
		}
	}

	return text[:boundaries[low]]
}

// NewerHalf is the later half of list, an odd list's middle item dropped, so
// a list of one item shrinks to none.
func NewerHalf[T any](list []T) []T {
	return list[len(list)/2+len(list)%2:]
}
