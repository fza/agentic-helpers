package memory_test

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// hookLineAge is the age show state prints after a hook line, which moves
// on with every call the test clock counts.
var hookLineAge = regexp.MustCompile(` \([0-9]+[smhdw]\)`)

// seatLedger is a seat main whose ledger holds an open agent line, a ticked
// one, a removed one and fifteen landed lines, the newest ticked, beside a
// seat idea holding none.
func seatLedger(t *testing.T) (*harness, *harness) {
	t.Helper()

	h, other := twoSeats(t)
	h.addLine("gate fix halfway; next: docs")
	ticked := h.addLine("gate tests written")
	h.must("", "ack", "state#"+ticked, "--done")
	removed := h.addLine("a line taken back")
	h.must("", "ack", "state#"+removed, "--remove")
	h.landCommits(0, 14)
	h.tickLanded(14)

	return h, other
}

func TestShowStateReadsAnotherSeatsLedgerAsItsOwnReadsIt(t *testing.T) {
	h, other := seatLedger(t)

	cases := []struct {
		name  string
		flags []string
	}{
		{name: "no flag"},
		{name: "with history", flags: []string{"--with-history"}},
		{name: "all items", flags: []string{"--all-items"}},
		{name: "with removed", flags: []string{"--with-removed"}},
		{name: "every flag", flags: []string{"--with-history", "--all-items", "--with-removed"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			own := h.must("", append([]string{"show", "state"}, tc.flags...)...)

			before := h.logBytes()
			read := other.must("", append([]string{"show", "state", "--seat", "main"}, tc.flags...)...)

			if !h.logUnchanged(before) {
				t.Error("reading another seat's ledger should write nothing")
			}

			if hookLineAge.ReplaceAllString(strings.ReplaceAll(read, " --seat main", ""), "") != hookLineAge.ReplaceAllString(own, "") {
				t.Errorf("another seat's ledger should read as its own seat reads it, got:\n%s\nbeside:\n%s", read, own)
			}
		})
	}

	if read := other.must("", "show", "state", "--seat", "main"); !strings.Contains(read, "hook: landed") ||
		!strings.Contains(read, "(3 older landed and captured: agentic-memory show state --with-history --seat main)") ||
		!strings.Contains(read, "(1 settled: agentic-memory show state --all-items --seat main)") {
		t.Errorf("another seat's ledger should print its hook lines and fold hints naming the seat read, got:\n%s", read)
	}
}

func TestAnotherSeatsOldSlotReadsWithoutTheRequestToSplitIt(t *testing.T) {
	h, other := twoSeats(t)
	writeOldState(t, h, "gate fix halfway; next: docs", false)

	if own := h.must("", "show", "state"); !strings.Contains(own, "state is a ledger now: split this") {
		t.Fatalf("the own seat should read its old slot with the request to split it, got:\n%s", own)
	}

	read := other.must("", "show", "state", "--seat", "main")
	if !strings.Contains(read, "#old (") || !strings.Contains(read, "gate fix halfway; next: docs") ||
		strings.Contains(read, "split this") {
		t.Errorf("another seat's old slot should read without the request to split it, got:\n%s", read)
	}
}

func TestShowStateReadsAnotherSeatsLedgerOverMCP(t *testing.T) {
	h, other := seatLedger(t)

	addresses := func(answers string) []string {
		t.Helper()

		var answer struct {
			State struct {
				Lines []struct {
					ID string `json:"id"`
				} `json:"lines"`
			} `json:"state"`
		}

		err := json.Unmarshal([]byte(mcpText(t, answers)), &answer)
		if err != nil {
			t.Fatalf("MCP show should answer JSON: %v\n%s", err, answers)
		}

		var held []string
		for _, line := range answer.State.Lines {
			held = append(held, line.ID)
		}

		return held
	}

	own := addresses(h.mcp(`{"name":"show","arguments":{"ids":["state"],"all_items":true}}`))
	read := addresses(other.mcp(`{"name":"show","arguments":{"ids":["state"],"seat":"main","all_items":true}}`))

	if len(own) == 0 || !slices.Equal(read, own) {
		t.Errorf("MCP show should carry another seat's lines as its own seat's read does, got %v beside %v", read, own)
	}

	json := other.must("", "show", "state", "--seat", "main", "--all-items", "--json")
	if !strings.Contains(json, `"id":"`+own[0]+`"`) {
		t.Errorf("show --json should carry another seat's ledger, got:\n%s", json)
	}
}

func TestShowStateReadsAnotherSeatsLedgerWithoutASeat(t *testing.T) {
	_, unseated := seatLedger(t)
	unseated.must("", "release")

	got := unseated.run("", "show", "state", "--seat", "main")
	if got.code != 0 || !strings.Contains(got.out, "state of seat main:") || !strings.Contains(got.out, "gate fix halfway") {
		t.Errorf("a session holding no seat should read another seat's ledger, got %d %q %q", got.code, got.out, got.err)
	}

	if got := unseated.run("", "show", "state"); got.code != 1 || !strings.Contains(got.out+got.err, "state: claim a seat first") {
		t.Errorf("a session holding no seat should still find no ledger of its own, got %d %q %q", got.code, got.out, got.err)
	}
}

func TestShowStateRefusesASeatItCannotRead(t *testing.T) {
	h, other := seatLedger(t)

	cases := []struct {
		name string
		args []string
		says string
	}{
		{
			name: "a seat the project lacks", args: []string{"show", "state", "--seat", "nobody"},
			says: "state: no such seat: nobody; this project's seats: idea, main",
		},
		{
			name: "two seats", args: []string{"show", "state", "--seat", "main", "--seat", "idea"},
			says: "--seat names the one seat whose ledger show state reads",
		},
		{
			name: "no state id", args: []string{"show", "m-aaaaaa", "--seat", "main"},
			says: "--seat names whose ledger show state reads; give state among the ids",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := h.logBytes()

			got := other.run("", tc.args...)
			if got.code != 1 || !strings.Contains(got.out+got.err, tc.says) {
				t.Errorf("show should refuse a seat it cannot read, saying why, got %d %q %q", got.code, got.out, got.err)
			}

			if !h.logUnchanged(before) {
				t.Error("a refused read should write nothing")
			}
		})
	}

	refusal := mcpRefusal(t, other.mcp(`{"name":"show","arguments":{"ids":["state"],"seat":"nobody"}}`))
	if !strings.Contains(refusal, "state: no such seat: nobody; this project's seats: idea, main") {
		t.Errorf("MCP show should refuse a seat the project lacks, naming its seats, got %q", refusal)
	}

	empty := newHarness(t)
	if got := empty.run("", "show", "state", "--seat", "main"); got.code != 1 ||
		!strings.Contains(got.out+got.err, "state: no such seat: main; no seat in this project") {
		t.Errorf("show should refuse a seat in a project holding none, saying so, got %d %q %q", got.code, got.out, got.err)
	}
}

func TestAckTakesNoSeat(t *testing.T) {
	h, other := seatLedger(t)
	id := strings.TrimPrefix(shownLineID.FindString(h.must("", "show", "state")), "  [ ] ")

	before := h.logBytes()

	if got := other.run("", "ack", id, "--done", "--seat", "main"); got.code == 0 {
		t.Errorf("ack should refuse naming another seat's ledger, got %q", got.out)
	}

	refusal := mcpRefusal(t, other.mcp(`{"name":"ack","arguments":{"ids":["`+id+`"],"done":true,"seat":"main"}}`))
	if refusal == "" {
		t.Error("MCP ack should refuse naming another seat's ledger")
	}

	if !h.logUnchanged(before) {
		t.Error("an ack naming another seat should change nothing of its ledger")
	}
}

func mcpRefusal(t *testing.T, answers string) string {
	t.Helper()

	refusal, _ := decoded(t, mcpText(t, answers))["error"].(string)

	return refusal
}
