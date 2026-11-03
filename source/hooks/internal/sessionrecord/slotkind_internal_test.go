package sessionrecord

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
)

// worst is text of size bytes JSON spends the most on: every `<` takes six,
// and so does every control character.
func worst(size int) string {
	return strings.Repeat("<\x01", size/2+1)[:size]
}

// worstAtCaps is a record whose every text field takes exactly its cap, in
// characters spelled out as escapes, so no field is dropped by its cap and
// only the fit can bring the record within its ceiling.
func worstAtCaps() *entry {
	at := time.Date(2026, time.November, 3, 9, 59, 59, 999_000_000, time.FixedZone("", -12*3600-45*60))
	held := &entry{Schema: Schema, EventAt: at, WaitingSince: at}
	held.PID = 1<<31 - 1
	held.Started = at

	for _, field := range held.fields() {
		*field.value = slots.CutJSON(strings.Repeat("\x01", field.cap), field.cap)
	}

	return held
}

// TestEveryKindStoresItsWorstCase builds the record at its worst, every text
// field at its cap and past it in the bytes JSON spends the most on: each
// fits its ceiling and is stored under the longest key a session takes.
func TestEveryKindStoresItsWorstCase(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	file := slots.Open(root, filepath.Join(root, slotsName))

	pastCaps := worstAtCaps()
	for _, field := range pastCaps.fields() {
		*field.value = worst(field.cap * 4)
	}

	cases := []struct {
		name  string
		value *entry
	}{
		{"every field at its cap", worstAtCaps()},
		{"every field past its cap", pastCaps},
	}

	if Ceiling > slots.MaxValue {
		t.Fatalf("the ceiling should stay within what a slot holds, %d is past it", Ceiling)
	}

	key := strings.Repeat("s", maxID)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := slots.Encode(tc.value)
			if err != nil {
				t.Fatalf("the worst case should fit its ceiling: %v", err)
			}

			if len(data) > Ceiling {
				t.Errorf("a fitted record should stay within its ceiling, %d bytes", len(data))
			}

			err = file.Put(ctx, key, data)
			if err != nil {
				t.Fatalf("the slot file should store a fitted worst case: %v", err)
			}

			held, found, err := file.Get(ctx, key)
			if err != nil || !found || !bytes.Equal(held, data) {
				t.Errorf("the stored worst case should read back as written, found %v %v", found, err)
			}
		})
	}
}

func TestTheFitDropsWhatAJumpNeedsLeastAndKeepsTheEvent(t *testing.T) {
	held := worstAtCaps()

	data, err := slots.Encode(held)
	if err != nil {
		t.Fatalf("the worst case should fit its ceiling: %v", err)
	}

	if held.GitCommonDir != "" {
		t.Error("the repository should be the first field dropped")
	}

	if held.Event == "" || held.WaitingFor == "" || held.WaitingAgent == "" || held.TTY == "" || len(data) == 0 {
		t.Errorf("the event, its wait and the terminal should stay while dropping less is enough, got %+v", held)
	}
}

func TestTheMinimalRecordFitsItsCeiling(t *testing.T) {
	held := worstAtCaps()
	for held.Shrink() {
	}

	if size := slots.EncodedLen(held); size > Ceiling {
		t.Errorf("a record with every droppable field dropped should fit its ceiling, %d bytes", size)
	}
}
