package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
)

// A seat's ledger keeps itself small by marks calibrated on the seat's own
// history, never on fixed counts: one machine's way of working is not
// another's. Every add and every settle (a tick, a remove, a line ending on
// its own) records the seat's own open-line count. The low mark is the
// median count at settle events, the size the seat holds while work closes;
// the high mark adds the spread of the counts, p75 minus p25, at least 2.
// Rejected: a moving average with high at 1.5 times low, which one burst
// drags for days with a factor again fixed; a hard cap refusing adds, which
// blocks work and invites blind ticking.
const (
	pressureWindow     = 14 * 24 * time.Hour
	pressureEvents     = 256
	pressureCalibrated = 10
	pressureMinSpread  = 2
	pressureListed     = 5
	pressureLineWords  = 12
	// pressureEvery is how many adds apart the nudge fires once its backoff
	// doubled up to it.
	pressureEvery   = 16
	pressureCeiling = 20_000
)

// coldMarks are the marks of a seat with too few settles of its own where
// no other seat of the project has enough either.
var coldMarks = ledgerMarks{low: 6, high: 10}

// pressureEvent is one change of a seat's ledger: when, the seat's own open
// lines after it, and whether it settled a line rather than added one.
type pressureEvent struct {
	At      int64 `json:"at"`
	Open    int   `json:"open"`
	Settled bool  `json:"settled,omitempty"`
}

// ledgerPressure is a seat's record of its ledger changes and where the
// nudge's backoff stands: Fired once the nudge fired since the count last
// stood at the low mark, Further the adds since.
type ledgerPressure struct {
	Events  []pressureEvent `json:"events"`
	Fired   bool            `json:"fired,omitempty"`
	Further int             `json:"further,omitempty"`
}

type ledgerMarks struct {
	low, high float64
}

func (mem *memory) pressureKey(seatKey string) string { return "ledger-pressure/" + seatKey }

// percentile is the p-th percentile of values, interpolated between the two
// nearest ranks; zero for none.
func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}

	sorted := slices.Sorted(slices.Values(values))
	rank := p * float64(len(sorted)-1)
	below := int(math.Floor(rank))
	above := int(math.Ceil(rank))

	return sorted[below] + (sorted[above]-sorted[below])*(rank-float64(below))
}

// windowed is the events within the window before now, the newest
// pressureEvents of them.
func windowed(events []pressureEvent, now time.Time) []pressureEvent {
	floor := now.Add(-pressureWindow).Unix()

	kept := slices.DeleteFunc(slices.Clone(events), func(event pressureEvent) bool { return event.At < floor })

	return kept[max(0, len(kept)-pressureEvents):]
}

// ownMarks are the marks a seat's own events give, and whether they hold
// enough settles to calibrate on.
func ownMarks(events []pressureEvent) (ledgerMarks, bool) {
	var settled, counts []float64

	for _, event := range events {
		counts = append(counts, float64(event.Open))

		if event.Settled {
			settled = append(settled, float64(event.Open))
		}
	}

	if len(settled) < pressureCalibrated {
		return ledgerMarks{}, false
	}

	low := percentile(settled, 0.5)
	spread := max(pressureMinSpread, percentile(counts, 0.75)-percentile(counts, 0.25))

	return ledgerMarks{low: low, high: low + spread}, true
}

// borrowedMarks are the marks of a seat too new to calibrate: the median of
// the other seats' marks that do, else the cold marks.
func borrowedMarks(others []ledgerMarks) ledgerMarks {
	if len(others) == 0 {
		return coldMarks
	}

	var lows, highs []float64

	for _, marks := range others {
		lows = append(lows, marks.low)
		highs = append(highs, marks.high)
	}

	return ledgerMarks{low: percentile(lows, 0.5), high: percentile(highs, 0.5)}
}

// pressureFires is whether the nudge fires on the further-th add since it
// first fired: on the 2nd, 4th, 8th and 16th, then every 16th.
func pressureFires(further int) bool {
	if further <= pressureEvery {
		return further >= 2 && further&(further-1) == 0
	}

	return further%pressureEvery == 0
}

// record takes one ledger change into the backoff and answers whether the
// nudge fires on it: on the first add above the high mark, then backing off
// while the count stays above the low mark, a settle above it leaving the
// backoff where it stands. A count at or below the low mark starts it over.
func (held *ledgerPressure) record(event pressureEvent, marks ledgerMarks) bool {
	held.Events = append(held.Events, event)

	switch {
	case float64(event.Open) <= marks.low:
		held.Fired, held.Further = false, 0

		return false
	case event.Settled:
		return false
	case !held.Fired:
		held.Fired = float64(event.Open) > marks.high

		return held.Fired
	}

	held.Further++

	return float64(event.Open) > marks.high && pressureFires(held.Further)
}

func (held *ledgerPressure) Ceiling() int { return pressureCeiling }

func (held *ledgerPressure) Limit() {
	held.Events = held.Events[max(0, len(held.Events)-pressureEvents):]
}

// Shrink has nothing to give up: 256 events of the widest numbers stay
// within the ceiling.
func (held *ledgerPressure) Shrink() bool { return false }

// marksFor are the seat's marks: its own where it settled enough lines in
// the window, else borrowed from the project's seats that did: its own slot,
// holding fewer, never counts among them.
func (mem *memory) marksFor(events []pressureEvent) ledgerMarks {
	marks, calibrated := ownMarks(windowed(events, mem.now()))
	if calibrated {
		return marks
	}

	registry, _ := mem.registry()

	var others []ledgerMarks

	for _, other := range slices.Sorted(maps.Keys(registry.Seats)) {
		var held ledgerPressure
		if !mem.readSlot(mem.pressureKey(other), &held) {
			continue
		}

		marks, calibrated := ownMarks(windowed(held.Events, mem.now()))
		if calibrated {
			others = append(others, marks)
		}
	}

	return borrowedMarks(others)
}

// recordLedger keeps one change of the seat's ledger, its own open count
// after it, and answers the seat's marks and whether the nudge fires on it.
func (mem *memory) recordLedger(ctx context.Context, seatName string, open int, settled bool) (ledgerMarks, bool) {
	key := mem.seatKey(seatName)
	if key == "" {
		return coldMarks, false
	}

	change := pressureEvent{At: mem.now().Unix(), Open: open, Settled: settled}

	var held ledgerPressure

	mem.readSlot(mem.pressureKey(key), &held)
	marks := mem.marksFor(append(held.Events, change))

	fires := false

	mem.updateSlot(ctx, mem.pressureKey(key), func(value []byte, found bool) (slots.Fitted, error) {
		var current ledgerPressure

		if found {
			err := json.Unmarshal(value, &current)
			if err != nil {
				current = ledgerPressure{}
			}
		}

		current.Events = windowed(current.Events, mem.now())
		fires = current.record(change, marks)

		return &current, nil
	})

	return marks, fires
}

// ownOpen is the seat's open agent lines, oldest first, as the ledger holds
// them in the order added.
func ownOpen(views []lineView) []lineView {
	var open []lineView

	for _, view := range views {
		if view.open() && view.line.Hook == "" {
			open = append(open, view)
		}
	}

	return open
}

// pressureLines are the nudge: the count against the low mark, then the
// oldest open lines, each with the call that ticks it.
func (mem *memory) pressureLines(at channel, open []lineView, marks ledgerMarks) []string {
	lines := []string{fmt.Sprintf("ledger holds %d open lines, this seat settles work at %d; tick each settled one, oldest first:",
		len(open), int(math.Round(marks.low)))}

	for _, view := range open[:min(len(open), pressureListed)] {
		text := cutWords(strings.Join(strings.Fields(view.text), " "), pressureLineWords)
		if text != strings.Join(strings.Fields(view.text), " ") {
			text += " ..."
		}

		lines = append(lines, fmt.Sprintf("  #%s (%s) %s: %s", view.line.ID, age(mem.now().Sub(view.line.Added)), text,
			settleHint(ledgerID+"#"+view.line.ID, "done").in(at)))
	}

	return lines
}

// briefPressure is the nudge as the brief carries it: whenever the seat's
// own open lines stand above its high mark.
func (mem *memory) briefPressure(ctx context.Context, views []lineView, seatName string) []string {
	open := ownOpen(views)

	key := mem.seatKey(seatName)
	if key == "" || len(open) == 0 {
		return nil
	}

	var held ledgerPressure

	mem.readSlot(mem.pressureKey(key), &held)

	marks := mem.marksFor(held.Events)
	if float64(len(open)) <= marks.high {
		return nil
	}

	return mem.pressureLines(mem.hintChannel(ctx), open, marks)
}
