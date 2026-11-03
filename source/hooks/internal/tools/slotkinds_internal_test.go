package tools

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
)

// worst is text of size bytes JSON spends the most on: every `<` takes six,
// and so does every control character.
func worst(size int) string {
	return strings.Repeat("<\x01", size/2+1)[:size]
}

func worstList(count int, size int) []string {
	list := make([]string, count)
	for index := range list {
		list[index] = fmt.Sprintf("%d", index) + worst(size)
	}

	return list
}

func worstSession() *sessionState {
	held := &sessionState{Notified: map[string]int{}, Signature: worst(10_000)}

	for index := range 500 {
		var files []fileHash
		for file := range 200 {
			files = append(files, fileHash{Name: fmt.Sprintf("%d", file) + worst(200), Sum: strings.Repeat("f", 64), Size: 1 << 62})
		}

		copyPath := worst(150)
		if index%2 == 0 {
			copyPath = worst(4_000)
		}

		held.Holdings = append(held.Holdings, holding{
			Project: fmt.Sprintf("p%d", index) + worst(30), Tool: fmt.Sprintf("t%d", index) + worst(30),
			Version: 1 << 62, Copy: copyPath, Files: files,
		})
		held.Notified[fmt.Sprintf("%d", index)+worst(500)] = 1 << 62
	}

	for index, line := range worstList(500, 5_000) {
		held.Pending = append(held.Pending, nudge{Key: fmt.Sprintf("%d", index) + worst(5_000), Line: line})
	}

	held.Nudged = worstList(5_000, 2_000)

	return held
}

// TestEveryKindStoresItsWorstCase builds the tool library's session state
// and seen set at their worst, every list past its count and every string
// past its cap in the bytes JSON spends the most on: each fits its ceiling
// and is stored under the longest key it takes.
func TestEveryKindStoresItsWorstCase(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	file := slots.Open(root, filepath.Join(root, "sessions.slots"), 128<<10)

	cases := []struct {
		kind    string
		key     string
		ceiling int
		encode  func() ([]byte, error)
	}{
		{"session", "session/" + strings.Repeat("s", 128), sessionCeiling, func() ([]byte, error) { return worstSession().fit() }},
		{"seen", fmt.Sprintf("seen/%d-%d", 1<<62, 1<<62), seenCeiling, func() ([]byte, error) {
			return slots.Encode(&seenSet{Lines: worstList(20_000, 200), Offered: worstList(20_000, 200)})
		}},
	}

	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			if tc.ceiling > slots.MaxValue {
				t.Fatalf("a kind's ceiling should stay within what a slot holds, %d is past it", tc.ceiling)
			}

			data, err := tc.encode()
			if err != nil {
				t.Fatalf("the worst case should fit its ceiling: %v", err)
			}

			if len(data) > tc.ceiling {
				t.Errorf("a fitted value should stay within its ceiling, %d bytes", len(data))
			}

			err = file.Put(ctx, tc.key, data)
			if err != nil {
				t.Fatalf("the slot file should store a fitted worst case: %v", err)
			}

			held, found, err := file.Get(ctx, tc.key)
			if err != nil || !found || !bytes.Equal(held, data) {
				t.Errorf("the stored worst case should read back as written, found %v %v", found, err)
			}
		})
	}
}

func smallHoldings(count int) []holding {
	var held []holding
	for index := range count {
		held = append(held, holding{Project: "p", Tool: fmt.Sprintf("tool-%d", index), Version: 1, Copy: "/c"})
	}

	return held
}

func smallPending(count int) []nudge {
	var pending []nudge
	for index := range count {
		pending = append(pending, nudge{Key: fmt.Sprintf("k%d", index), Line: "l"})
	}

	return pending
}

// TestASessionPastItsCeilingGivesUpWhatMattersLeastFirst fits sessions each
// past the ceiling by one part: delivered nudges go before stale versions
// told of, those before pending nudges, those before holdings.
func TestASessionPastItsCeilingGivesUpWhatMattersLeastFirst(t *testing.T) {
	cases := []struct {
		name  string
		state func() *sessionState
		check func(held *sessionState) bool
	}{
		{"delivered nudges go first", func() *sessionState {
			return &sessionState{Holdings: smallHoldings(5), Pending: smallPending(5), Nudged: worstList(1_000, 100)}
		}, func(held *sessionState) bool {
			return len(held.Holdings) == 5 && len(held.Pending) == 5 && len(held.Nudged) > 0
		}},
		{"stale versions told of go before pending nudges", func() *sessionState {
			notified := map[string]int{}
			for index, key := range worstList(1_000, 100) {
				notified[key] = index
			}

			return &sessionState{Holdings: smallHoldings(5), Pending: smallPending(5), Notified: notified}
		}, func(held *sessionState) bool {
			return len(held.Holdings) == 5 && len(held.Pending) == 5 && len(held.Notified) == 0
		}},
		{"pending nudges go before holdings", func() *sessionState {
			// Holdings just within the ceiling, which the pending nudges pass.
			holdings := smallHoldings(maxHoldings)
			for size := 1_000; size > 0; size-- {
				for index := range holdings {
					holdings[index].Files = []fileHash{{Name: worst(size), Sum: "s"}}
				}

				if slots.EncodedLen(sessionState{Holdings: holdings}) <= sessionCeiling-200 {
					break
				}
			}

			pending := smallPending(maxPending)
			for index := range pending {
				pending[index].Line = worst(1_000)
			}

			return &sessionState{Holdings: holdings, Pending: pending}
		}, func(held *sessionState) bool {
			return len(held.Holdings) == maxHoldings && len(held.Pending) == 0
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			held := tc.state()

			data, err := held.fit()
			if err != nil || len(data) > sessionCeiling || !tc.check(held) {
				t.Errorf("the session should give up only what matters least: %v, %d holdings, %d pending, %d nudged, %d told",
					err, len(held.Holdings), len(held.Pending), len(held.Nudged), len(held.Notified))
			}
		})
	}
}

// TestASessionPastItsCapsKeepsWhatFits fits a session whose holdings, nudges
// and keys pass their caps while the whole stays within the ceiling.
func TestASessionPastItsCapsKeepsWhatFits(t *testing.T) {
	held := &sessionState{Holdings: append(smallHoldings(60), holding{Project: "p", Tool: "bad", Copy: worst(4_000)})}
	held.Pending = smallPending(30)
	held.Pending[len(held.Pending)-1].Line = worst(1_000)
	held.Pending = append(held.Pending, nudge{Key: worst(1_000), Line: "l"})

	_, err := held.fit()
	if err != nil {
		t.Fatalf("the session should fit: %v", err)
	}

	if len(held.Holdings) != maxHoldings || held.Holdings[len(held.Holdings)-1].Tool != "tool-59" {
		t.Errorf("a session should keep its newest holdings up to their count, kept %d", len(held.Holdings))
	}

	if len(held.Pending) != maxPending || held.Pending[len(held.Pending)-1].Key != "k29" ||
		slots.EncodedLen(held.Pending[len(held.Pending)-1].Line) > pendingBytes {
		t.Errorf("a session should keep its newest pending nudges up to their count, each cut to its bytes")
	}

	for _, holding := range held.Holdings {
		if slots.EncodedLen(holding.Copy) > copyPathBytes {
			t.Errorf("a holding whose copy path passes its cap should be left out")
		}
	}

	for _, pending := range held.Pending {
		if slots.EncodedLen(pending.Key) > pendingKeyBytes {
			t.Errorf("a nudge whose key passes its cap should be left out")
		}
	}
}

// TestASessionWithinItsCapsIsKeptWhole fits a session whose holdings, nudges
// and versions told of stand at their caps: nothing is cut or dropped.
func TestASessionWithinItsCapsIsKeptWhole(t *testing.T) {
	build := func() *sessionState {
		held := &sessionState{Notified: map[string]int{}}

		for index := range maxHoldings {
			held.Holdings = append(held.Holdings, holding{
				Project: "p", Tool: fmt.Sprintf("tool-%d", index), Version: 3, Copy: strings.Repeat("c", copyPathBytes-2),
				Files: []fileHash{{Name: "run.sh", Sum: strings.Repeat("f", 64), Size: 120}},
			})
			held.Notified[holdingKey("p", fmt.Sprintf("tool-%d", index))] = 3
		}

		for index := range maxPending {
			held.Pending = append(held.Pending, nudge{Key: fmt.Sprintf("k%d", index), Line: strings.Repeat("l", pendingBytes-2)})
		}

		return held
	}

	before, _ := build().fit()
	want := build()

	got := build()
	_, err := got.fit()

	if err != nil || len(got.Holdings) != len(want.Holdings) || len(got.Pending) != len(want.Pending) ||
		len(got.Notified) != len(want.Notified) || got.Pending[0].Line != want.Pending[0].Line || len(before) > sessionCeiling {
		t.Errorf("a session within its caps should be kept as it is: %v", err)
	}
}
