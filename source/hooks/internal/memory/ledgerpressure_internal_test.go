package memory

import (
	"testing"
	"time"
)

func TestPercentileInterpolatesBetweenRanks(t *testing.T) {
	cases := []struct {
		name   string
		values []float64
		p      float64
		want   float64
	}{
		{name: "none", values: nil, p: 0.5, want: 0},
		{name: "one", values: []float64{7}, p: 0.75, want: 7},
		{name: "an odd count's median", values: []float64{9, 1, 5}, p: 0.5, want: 5},
		{name: "an even count's median", values: []float64{4, 1, 3, 2}, p: 0.5, want: 2.5},
		{name: "the lower quartile", values: []float64{1, 2, 3, 4, 5}, p: 0.25, want: 2},
		{name: "the upper quartile between ranks", values: []float64{1, 2, 3, 4}, p: 0.75, want: 3.25},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := percentile(tc.values, tc.p); got != tc.want {
				t.Errorf("the percentile should interpolate between the nearest ranks, got %v", got)
			}
		})
	}
}

// history is a seat's events, one an hour: each count added, then the
// counts settled at.
func history(added []int, settled []int) []pressureEvent {
	var events []pressureEvent

	at := int64(1_800_000_000)

	for _, open := range added {
		events = append(events, pressureEvent{At: at, Open: open})
		at += 3600
	}

	for _, open := range settled {
		events = append(events, pressureEvent{At: at, Open: open, Settled: true})
		at += 3600
	}

	return events
}

func TestOwnMarksAreTheMedianAndTheSpread(t *testing.T) {
	cases := []struct {
		name       string
		events     []pressureEvent
		calibrated bool
		marks      ledgerMarks
	}{
		{name: "too few settles", events: history([]int{20, 21}, []int{4, 5, 6, 4, 5, 6, 4, 5, 6}), calibrated: false},
		{
			name:       "the spread above its floor",
			events:     history([]int{12, 14, 16, 18, 20}, []int{4, 4, 5, 5, 6, 6, 6, 7, 8, 9}),
			calibrated: true,
			// settled median 6; every count's p25 5.5, p75 13.
			marks: ledgerMarks{low: 6, high: 13.5},
		},
		{
			name:       "a narrow spread takes the floor",
			events:     history(nil, []int{5, 5, 5, 5, 5, 5, 5, 5, 5, 5}),
			calibrated: true,
			marks:      ledgerMarks{low: 5, high: 7},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			marks, calibrated := ownMarks(tc.events)
			if calibrated != tc.calibrated {
				t.Fatalf("a seat should calibrate on its own events exactly from ten settles, calibrated %v", calibrated)
			}

			if calibrated && marks != tc.marks {
				t.Errorf("the low mark should be the settled median and the high one add the spread, got %+v", marks)
			}
		})
	}
}

func TestANewSeatBorrowsTheProjectsMarks(t *testing.T) {
	cases := []struct {
		name   string
		others []ledgerMarks
		want   ledgerMarks
	}{
		{name: "no seat calibrated", want: coldMarks},
		{name: "one seat", others: []ledgerMarks{{low: 4, high: 7}}, want: ledgerMarks{low: 4, high: 7}},
		{
			name:   "the median of several",
			others: []ledgerMarks{{low: 4, high: 7}, {low: 8, high: 12}, {low: 6, high: 20}},
			want:   ledgerMarks{low: 6, high: 12},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := borrowedMarks(tc.others); got != tc.want {
				t.Errorf("a seat too new to calibrate should take the other seats' median marks, else the cold ones, got %+v", got)
			}
		})
	}
}

func TestTheWindowKeepsTwoWeeksAndTheNewest256(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)

	var events []pressureEvent
	for index := range 300 {
		events = append(events, pressureEvent{At: now.Add(-time.Duration(299-index) * time.Minute).Unix(), Open: index})
	}

	kept := windowed(events, now)
	if len(kept) != pressureEvents || kept[0].Open != 300-pressureEvents || kept[len(kept)-1].Open != 299 {
		t.Errorf("the window should keep the newest events up to its cap, kept %d", len(kept))
	}

	old := []pressureEvent{{At: now.Add(-15 * 24 * time.Hour).Unix(), Open: 1}, {At: now.Add(-13 * 24 * time.Hour).Unix(), Open: 2}}
	if kept := windowed(old, now); len(kept) != 1 || kept[0].Open != 2 {
		t.Errorf("the window should drop events older than two weeks, kept %+v", kept)
	}
}

func TestTheBackoffDoublesToSixteenThenHolds(t *testing.T) {
	var fired []int

	for further := range 70 {
		if pressureFires(further) {
			fired = append(fired, further)
		}
	}

	want := []int{2, 4, 8, 16, 32, 48, 64}
	if len(fired) != len(want) {
		t.Fatalf("the nudge should fire on the 2nd, 4th, 8th and 16th further add, then every 16th, fired on %v", fired)
	}

	for index := range want {
		if fired[index] != want[index] {
			t.Fatalf("the nudge should fire on the 2nd, 4th, 8th and 16th further add, then every 16th, fired on %v", fired)
		}
	}
}

func TestTheNudgeFiresAboveTheHighMarkAndResetsAtTheLow(t *testing.T) {
	marks := ledgerMarks{low: 6, high: 10}

	steps := []struct {
		open    int
		settled bool
		fires   bool
	}{
		{open: 10},
		{open: 11, fires: true},
		{open: 12},
		{open: 13, fires: true},
		{open: 12, settled: true},
		{open: 13},
		{open: 14, fires: true},
		{open: 6, settled: true},
		{open: 7},
		{open: 11, fires: true},
		{open: 12},
	}

	var held ledgerPressure

	for index, step := range steps {
		if got := held.record(pressureEvent{Open: step.open, Settled: step.settled}, marks); got != step.fires {
			t.Errorf("step %d: the nudge should fire on the first add above the high mark, then back off until the count falls to the low one, fired %v",
				index, got)
		}
	}

	if len(held.Events) != len(steps) {
		t.Errorf("every change should be recorded, held %d", len(held.Events))
	}
}
