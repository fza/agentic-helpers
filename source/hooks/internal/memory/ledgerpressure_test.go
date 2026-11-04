package memory_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

const pressureOpens = "open lines, this seat settles work at"

// addLines adds count lines a minute apart and answers their ids and the
// last add's reply.
func (h *harness) addLines(count int, prefix string) ([]string, string) {
	h.t.Helper()

	var (
		ids   []string
		reply string
	)

	for index := range count {
		h.clock.Advance(time.Minute)
		reply = h.must("", "ack", "state", "--add", fmt.Sprintf("%s %d", prefix, index), "--until", "none")
		ids = append(ids, lineAddress.FindStringSubmatch(reply)[1])
	}

	return ids, reply
}

func TestTheNudgeListsTheOldestOpenLinesWithTheirTicks(t *testing.T) {
	h := commitHarness(t)
	h.must(h.stop(), "stop")

	ids, reply := h.addLines(10, "a step of the gate fix that runs past the twelve words a nudge prints")
	if strings.Contains(reply, pressureOpens) {
		t.Fatalf("a ledger at a new seat's cold high mark, its hook lines not counted, should draw no nudge, got %q", reply)
	}

	_, reply = h.addLines(1, "the eleventh")

	want := "ledger holds 11 open lines, this seat settles work at 6; tick each settled one, oldest first:\n" +
		"  #" + ids[0] + " (11m) a step of the gate fix that runs past the twelve words ...: agentic-memory ack state#" + ids[0] + " --done\n"
	if !strings.Contains(reply, want) || strings.Contains(reply, "hook:") || strings.Contains(reply, "landed") {
		t.Fatalf("the first add past the high mark should nudge, naming the count and the low mark, then the agent's oldest lines cut to twelve words, got:\n%s", reply)
	}

	for index, id := range ids {
		listed := strings.Contains(reply, fmt.Sprintf("  #%s (", id)) &&
			strings.Contains(reply, fmt.Sprintf(": agentic-memory ack state#%s --done", id))
		if listed != (index < 5) {
			t.Errorf("the nudge should list the five oldest open lines, each with its tick, line %d listed %v:\n%s", index, listed, reply)
		}
	}

	if _, reply = h.addLines(1, "the twelfth"); strings.Contains(reply, pressureOpens) {
		t.Errorf("the add right after a nudge should draw none, got:\n%s", reply)
	}

	h.clock.Advance(time.Minute)

	answer := h.mcp(`{"name":"ack","arguments":{"ids":["state"],"add":["the thirteenth"],"until":["none"]}}`)
	if !strings.Contains(answer, pressureOpens) || !strings.Contains(answer, `memory ack {\"ids\":[\"state#`+ids[0]+`\"],\"done\":true}`) {
		t.Errorf("the second add after the first nudge should nudge again, in the channel's form, got %s", answer)
	}

	if brief := h.must("", "brief"); !strings.Contains(brief, "ledger holds 13 "+pressureOpens) ||
		!strings.Contains(brief, "agentic-memory ack state#"+ids[0]+" --done") {
		t.Errorf("the brief should carry the nudge while the ledger stands above its high mark, got:\n%s", brief)
	}

	for _, id := range ids[:7] {
		h.must("", "ack", "state#"+id, "--done")
	}

	if brief := h.must("", "brief"); strings.Contains(brief, pressureOpens) {
		t.Errorf("a ledger back at its low mark should leave the brief without the nudge, got:\n%s", brief)
	}

	if _, reply = h.addLines(4, "after"); strings.Contains(reply, pressureOpens) {
		t.Errorf("a ledger back at its high mark should draw no nudge, got:\n%s", reply)
	}

	if _, reply = h.addLines(1, "past it again"); !strings.Contains(reply, "ledger holds 11 "+pressureOpens) {
		t.Errorf("once the count fell to the low mark, the next add past the high one should nudge again, got:\n%s", reply)
	}
}

// settledAt writes a history of ten settles at open lines into the seat's
// slot: enough to calibrate its marks at open low, open plus 2 high.
func (h *harness) settledAt(seatName string, open int) {
	h.t.Helper()

	var events []string

	at := h.clock.Now().Unix()
	for index := range 10 {
		events = append(events, fmt.Sprintf(`{"at":%d,"open":%d,"settled":true}`, at-int64(index)*60, open))
	}

	h.putSlot("ledger-pressure/"+seatID(h.t, h.store(), seatName), `{"events":[`+strings.Join(events, ",")+`]}`)
}

func TestANewSeatBorrowsAnotherSeatsMarks(t *testing.T) {
	h, _ := twoSeats(t)
	h.settledAt("idea", 3)

	if _, reply := h.addLines(5, "step"); strings.Contains(reply, pressureOpens) {
		t.Fatalf("a ledger at the borrowed high mark should draw no nudge, got:\n%s", reply)
	}

	if _, reply := h.addLines(1, "past it"); !strings.Contains(reply, "ledger holds 6 open lines, this seat settles work at 3;") {
		t.Errorf("a seat too new to calibrate should nudge at the marks another seat of the project calibrated, got:\n%s", reply)
	}
}

func TestACalibratedSeatKeepsItsOwnMarks(t *testing.T) {
	h, _ := twoSeats(t)
	h.settledAt("idea", 20)
	h.settledAt("main", 3)

	if _, reply := h.addLines(6, "step"); !strings.Contains(reply, "ledger holds 6 open lines, this seat settles work at 3;") {
		t.Errorf("a seat with ten settles of its own should nudge at its own marks, not the project's, got:\n%s", reply)
	}
}

func TestEveryLedgerChangeIsRecordedWithTheOpenCount(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	removed := h.addLine("removed")
	ticked := h.addLine("ticked")
	h.must("", "ack", "state#"+removed, "--remove")
	h.addLine("ended", "--until", "at:2020-01-01T00:00:00Z")
	h.must("", "brief")
	h.must("", "ack", "state#"+ticked, "--done")
	h.must("", "ack", "state#"+removed, "--undo")
	h.must("", "brief")

	var held struct {
		Events []struct {
			Open    int  `json:"open"`
			Settled bool `json:"settled"`
		} `json:"events"`
	}

	err := json.Unmarshal([]byte(h.slot("ledger-pressure/"+seatID(t, h.store(), "main"))), &held)
	if err != nil {
		t.Fatalf("the seat's ledger changes should be kept: %v", err)
	}

	want := []struct {
		open    int
		settled bool
	}{{1, false}, {2, false}, {1, true}, {1, false}, {1, true}, {0, true}}

	if len(held.Events) != len(want) {
		t.Fatalf("every add, settle and line ended should be one event, an undo and a line named before none, got %+v", held.Events)
	}

	for index, event := range want {
		if held.Events[index].Open != event.open || held.Events[index].Settled != event.settled {
			t.Errorf("event %d should record the own open count after the change and whether it settled, got %+v", index, held.Events[index])
		}
	}
}
