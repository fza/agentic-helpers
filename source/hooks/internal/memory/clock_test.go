package memory_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const rollback = -24 * time.Hour

func TestAnEntryFiledAfterTheClockWasSetBackSortsLastAndArrivesAsNew(t *testing.T) {
	h, other := twoSeats(t)
	earlier := h.file("Gate note filed before the rollback", "first")
	other.must("", "brief")

	h.clock.Advance(rollback)
	later := h.file("Gate note filed after the rollback", "second")

	listing := h.must("", "search", "--status", "live")
	if !strings.Contains(listing, later) || strings.Index(listing, later) > strings.Index(listing, earlier) {
		t.Errorf("an entry filed after the rollback should list as the newest, got:\n%s", listing)
	}

	if out := other.must("", "brief"); !strings.Contains(out, "new from other seats:\n  "+later) {
		t.Errorf("another seat's brief should list an entry filed after the rollback as new, got:\n%s", out)
	}
}

func TestARunningServerStampsAfterWhatAnotherSessionStoredSinceItsLastCall(t *testing.T) {
	h, other := twoSeats(t)
	h.file("Gate note filed before the rollback", "first")
	h.clock.Advance(rollback)

	server := h.serve()
	server.call("search", `{"status":["live"]}`)

	elsewhere := other.file("Gate note of the other seat", "second")
	answer := server.call("new", `{"title":"Gate note filed over the server","topic":["gate"],"body":"third"}`)

	served := idPattern.FindString(answer)
	if served == "" {
		t.Fatalf("the server should file the entry: %s", answer)
	}

	var listed struct {
		Entries []struct {
			ID    string    `json:"id"`
			Filed time.Time `json:"filed"`
		} `json:"entries"`
	}

	err := json.Unmarshal([]byte(h.must("", "search", "--all-seats", "--status", "live", "--json")), &listed)
	if err != nil {
		t.Fatal(err)
	}

	filed := map[string]time.Time{}
	for _, one := range listed.Entries {
		filed[one.ID] = one.Filed
	}

	if !filed[served].After(filed[elsewhere]) {
		t.Errorf("the server should stamp after what another session stored since its last call, got %s after %s",
			filed[served], filed[elsewhere])
	}
}

func TestANudgeAnsweredAfterTheClockWasSetBackDoesNotRepeat(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("Gate reads Bash only today", "finding")

	payload := hookPayload("", "", "")

	h.sensor(70)

	if !strings.Contains(h.said(payload, "prompt"), "Context near compaction") {
		t.Fatal("a nudge should fire past 65% of the window")
	}

	h.clock.Advance(rollback)
	h.sensor(70)
	h.file("Answer to the nudge here", "where work stands")

	if out := h.said(payload, "prompt"); strings.Contains(out, "Context near compaction") {
		t.Errorf("an entry filed after the rollback should answer the nudge before it, got:\n%s", out)
	}
}
