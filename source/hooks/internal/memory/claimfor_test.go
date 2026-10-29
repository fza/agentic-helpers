package memory_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func (h *harness) holderJSON(args ...string) (map[string]any, int) {
	h.t.Helper()

	got := h.run("", append([]string{"holder"}, args...)...)

	var view map[string]any

	err := json.Unmarshal([]byte(got.out), &view)
	if err != nil {
		h.t.Fatalf("holder --json should print JSON, got %q: %v", got.out, err)
	}

	return view, got.code
}

func TestAClaimForASessionIsAReservationItTakesOver(t *testing.T) {
	board := newHarness(t)
	job := newHarnessSharing(board)

	if out := board.must("", "claim", "impl", "--session", job.session, "--shelf", "row-7ah", "--lifetime", "2d"); !strings.Contains(out, "reserved impl for session a1b2c3d4 (shelf row-7ah)") ||
		!strings.Contains(out, "shelf row-7ah is temporary: purged 2d after its last seat closes") {
		t.Errorf("a claim for another session should reserve the seat, setting its shelf's lifetime, got:\n%s", out)
	}

	view, code := board.holderJSON("impl", "--json")
	if code != 1 || view["reserved"] != true || view["alive"] != nil || view["session_id"] != job.session || view["seat"] != "impl" {
		t.Errorf("a reservation should read as reserved, not alive, exit 1, got %d %v", code, view)
	}

	board.must("", "claim", "rev", "--session", "f00dbabe-0000-4000-8000-000000000000", "--shelf", "row-0", "--lifetime", "0")
	board.must(hookPayload("startup", "", ""), "session-start")

	if _, found := mustShelf(board, "row-0"); !found {
		t.Error("a reservation should keep its temporary shelf in use")
	}

	if got := board.run("", "claim", "impl"); got.code != 1 || !strings.Contains(got.err, "impl is reserved for session a1b2c3d4") {
		t.Errorf("a reserved seat should refuse a claim from another session, got %d %q", got.code, got.err)
	}

	job.must(job.promptHook("start"), "prompt")

	view, code = job.holderJSON("--session", job.session, "--json")
	if code != 0 || view["alive"] != true || view["reserved"] != nil {
		t.Errorf("the session's first hook should take the reservation over, got %d %v", code, view)
	}

	id := job.file("Row finding from the reserved seat", "finding")
	if out := job.must("", "show", id); !strings.Contains(out, "[impl, ") {
		t.Errorf("the session's commands should act as the seat it took over, got:\n%s", out)
	}

	if out := board.must("", "claim", "impl", "--session", job.session); !strings.Contains(out, "session a1b2c3d4 already holds impl") {
		t.Errorf("a claim for a session already holding the seat should change nothing, got %q", out)
	}
}

func TestAReservationNobodyTakesOverFrees(t *testing.T) {
	board := newHarness(t)
	board.must("", "claim", "impl", "--session", test.OtherSession)
	board.clock.Advance(11 * time.Minute)

	if out := board.must("", "claim", "impl"); !strings.HasPrefix(out, "impl: ") {
		t.Errorf("a reservation untaken for 10 minutes should free the seat, got:\n%s", out)
	}
}

func TestAClaimForASessionRefusesASeatInUse(t *testing.T) {
	board := newHarness(t)
	board.must("", "claim", "main")

	job := newHarnessSharing(board)

	if got := job.run("", "claim", "main", "--session", "f00dbabe-0000-4000-8000-000000000000"); got.code != 1 ||
		!strings.Contains(got.err, "main is held by session c3fb65eb") {
		t.Errorf("a reservation should refuse a seat a running session holds, got %d %q", got.code, got.err)
	}

	job.must("", "claim", "rev", "--session", "f00dbabe-0000-4000-8000-000000000000")

	if got := board.run("", "claim", "rev", "--session", test.OtherSession); got.code != 1 ||
		!strings.Contains(got.err, "rev is reserved for session f00dbabe") {
		t.Errorf("a reservation should refuse a seat reserved for another session, got %d %q", got.code, got.err)
	}
}

func TestAClaimForASeatedSessionHandsItOver(t *testing.T) {
	board := newHarness(t)
	job := newHarnessSharing(board)
	job.must("", "claim", "impl-7ah", "--shelf", "row-7ah", "--lifetime", "0")
	job.file("Row finding", "the row's own")

	out := board.must("", "claim", "impl-8bq", "--session", job.session, "--shelf", "row-8bq")
	if !strings.Contains(out, "moved session a1b2c3d4 from impl-7ah to impl-8bq (shelf row-8bq)") {
		t.Errorf("a claim for a seated session should hand it over, got:\n%s", out)
	}

	view, code := board.holderJSON("--session", job.session, "--json")
	if code != 0 || view["seat"] != "impl-8bq" || view["alive"] != true {
		t.Errorf("the handover should take effect at once, the session holding the new seat, got %d %v", code, view)
	}

	if _, found := mustShelf(board, "row-7ah"); found {
		t.Error("the handover should close the old seat, purging its temporary shelf where it was the last in use")
	}

	id := job.file("Finding on the next row", "finding")
	if out := job.must("", "show", id); !strings.Contains(out, "[impl-8bq, ") {
		t.Errorf("the session's next call should act as the new seat, got:\n%s", out)
	}

	prompt := job.must(job.promptHook("next"), "prompt")
	if !strings.Contains(prompt, "agentic-memory: seat moved from impl-7ah to impl-8bq (shelf row-8bq)") ||
		!strings.Contains(prompt, "agentic-memory: seat impl-8bq on shelf row-8bq") {
		t.Errorf("the session's next hook should say it moved and give the new seat's brief, got:\n%s", prompt)
	}

	if again := job.must(job.promptHook("after"), "prompt"); strings.Contains(again, "seat moved") {
		t.Errorf("the move should be told once, got:\n%s", again)
	}

	answer := board.mcp(`{"name":"claim","arguments":{"name":"impl-9zz","session":"` + job.session + `"}}`)
	if !strings.Contains(answer, "moved session a1b2c3d4 from impl-8bq to impl-9zz") {
		t.Errorf("the MCP claim should take the session key, got %s", answer)
	}
}
