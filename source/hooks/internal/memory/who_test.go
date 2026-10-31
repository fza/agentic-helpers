package memory_test

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestWhoListsEveryProjectSeatWithItsShelf(t *testing.T) {
	h, other := rowShelf(t)
	h.must("", "release")
	h.must("", "claim", "main")
	h.must("", "claim", "spare", "--session", "f00dbabe-0000-4000-8000-000000000000")

	stale := newHarnessSharing(h)
	stale.must("", "release")
	stale.must("", "claim", "gone")
	h.processes.Kill(test.OtherOwnerPID)

	out := h.must("", "who", "--unused")

	for _, pattern := range []string{
		`(?m)^gone\s+free \(holder a1b2c3d4 gone, freed\)  filed \d+ / used \d+$`,
		`(?m)^impl\s+shelf row-7ah\s+free  filed \d+ / used \d+$`,
		`(?m)^main\s+c3fb65eb pid 200  since \d{4}-\d{2}-\d{2} \d{2}:\d{2} \S+  filed \d+ / used \d+$`,
		`(?m)^rev\s+shelf row-7ah\s+free  filed \d+ / used \d+$`,
		`(?m)^spare\s+reserved f00dbabe  filed \d+ / used \d+$`,
	} {
		if !regexp.MustCompile(pattern).MatchString(out) {
			t.Errorf("who should list every seat of the project with its shelf and state, %s missing, got:\n%s", pattern, out)
		}
	}

	if strings.Index(out, "impl ") > strings.Index(out, "rev ") || strings.Index(out, "main ") > strings.Index(out, "impl ") {
		t.Errorf("who should group the rows by shelf, got:\n%s", out)
	}

	if again := h.must("", "who"); strings.Contains(again, "freed") {
		t.Errorf("who should free a dead holder's claim once, got:\n%s", again)
	}

	h.clock.Advance(11 * time.Minute)

	if out := h.must("", "who"); !regexp.MustCompile(`(?m)^spare\s+free \(holder f00dbabe gone, freed\)  filed \d+ / used \d+$`).MatchString(out) {
		t.Errorf("who should free a reservation nobody took over in 10 minutes, got:\n%s", out)
	}

	_ = other
}

func TestWhoAllAddsEveryOtherProjectReadOnly(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.processes.Other()

	elsewhereID := test.OtherStore(t, h.roots(), "gitlab.com/acme/tool")
	elsewhere := h.roots().Store(elsewhereID, home.Memory)
	test.ClaimOtherSeat(t, elsewhere, "drive", test.OtherSession)
	test.ClaimDeadSeat(t, elsewhere, "left", test.Session)
	test.ClaimSeat(t, filepath.Join(h.store(), "backup", "format-1-20261025T170001.000000000Z"), "ghost", test.Session)

	before := test.Tree(t, elsewhere)

	if out := h.must("", "who"); strings.Contains(out, "drive") {
		t.Errorf("who should list the current project only, got:\n%s", out)
	}

	out := h.must("", "who", "--all")
	if !strings.Contains(out, "gitlab.com/acme/tool ("+elsewhereID+"):\n") || !regexp.MustCompile(`(?m)^  drive\s+a1b2c3d4 pid 300  filed \d+ / used \d+$`).MatchString(out) ||
		strings.Contains(out, "ghost") {
		t.Errorf("who --all should add every other project's seats under its name, backups aside, got:\n%s", out)
	}

	if out := h.must("", "who", "--all", "--unused"); !regexp.MustCompile(`(?m)^  left\s+free  filed \d+ / used \d+$`).MatchString(out) {
		t.Errorf("who --all --unused should add another project's unused seats too, got:\n%s", out)
	}

	if test.Tree(t, elsewhere) != before || test.Exists(t, filepath.Join(elsewhere, "format.json")) {
		t.Error("who --all should neither free nor migrate another project's store")
	}

	answer := h.mcp(`{"name":"who","arguments":{"all":true}}`)
	if !strings.Contains(answer, "gitlab.com/acme/tool") {
		t.Errorf("the MCP who should take the all key, got %s", answer)
	}

	outsideGit(h)

	if out := h.must("", "who", "--all"); !strings.HasPrefix(out, "no seat in this project") || !strings.Contains(out, "gitlab.com/acme/tool") {
		t.Errorf("who outside a project should still list the others with --all, got:\n%s", out)
	}
}

// sessionRecord writes Claude Code's record of the session running as pid
// under the harness's fake home.
func (h *harness) sessionRecord(pid int, session string, name string) {
	h.t.Helper()

	test.WriteFile(h.t, filepath.Join(h.home, ".claude", "sessions", strconv.Itoa(pid)+".json"),
		fmt.Sprintf(`{"pid": %d, "sessionId": %q, "name": %q, "cwd": "/somewhere"}`, pid, session, name))
}

func TestWhoNamesTheSessionToMessageForEachLiveHolder(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.processes.Other()
	h.sessionRecord(test.OwnerPID, test.Session, "memory\nrework")

	elsewhereID := test.OtherStore(t, h.roots(), "gitlab.com/acme/tool")
	elsewhere := h.roots().Store(elsewhereID, home.Memory)
	test.ClaimOtherSeat(t, elsewhere, "drive", test.OtherSession)
	test.ClaimOtherSeat(t, elsewhere, "ride", test.OtherSession)
	test.ClaimDeadSeat(t, elsewhere, "left", test.Session)
	h.sessionRecord(test.OtherOwnerPID, test.OtherSession, "tool release")

	out := h.must("", "who")
	if !regexp.MustCompile(`(?m)^main\s+c3fb65eb pid 200  since [^\n]+  message: memory rework  filed \d+ / used \d+$`).MatchString(out) {
		t.Errorf("who should name the session a live holder takes messages by, on one line, got:\n%s", out)
	}

	if strings.Contains(out, "drive") || !strings.HasSuffix(out,
		"\n+2 seats in other projects: agentic-memory who --all  (run before concluding a seat is not held)\n") {
		t.Errorf("who should fold other projects' seats into a count naming the command listing them, got:\n%s", out)
	}

	all := h.must("", "who", "--all")
	if !regexp.MustCompile(`(?m)^  drive\s+a1b2c3d4 pid 300  message: tool release  filed \d+ / used \d+$`).MatchString(all) ||
		strings.Contains(all, "in other projects") {
		t.Errorf("who --all should list other projects' seats with the same part and no fold line, got:\n%s", all)
	}

	if answer := mcpMessage(t, h.mcp(`{"name":"who","arguments":{}}`)); !strings.Contains(answer, "message: memory rework") ||
		!strings.Contains(answer, "+2 seats in other projects: agentic-memory who --all") {
		t.Errorf("the MCP who should answer as the command does, got:\n%s", answer)
	}

	if listed := h.must("", "who", "--json"); !strings.Contains(listed, `"message":"memory rework"`) {
		t.Errorf("who --json should carry the holder's session name, got:\n%s", listed)
	}
}

func TestWhoNamesNoSessionWithoutARecordOfTheHolder(t *testing.T) {
	cases := []struct {
		name   string
		record func(h *harness)
	}{
		{name: "no record", record: func(*harness) {}},
		{name: "another session's record", record: func(h *harness) { h.sessionRecord(test.OwnerPID, test.OtherSession, "elsewhere") }},
		{name: "an unreadable record", record: func(h *harness) {
			test.WriteFile(h.t, filepath.Join(h.home, ".claude", "sessions", strconv.Itoa(test.OwnerPID)+".json"), "{not json")
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")
			tc.record(h)

			if out := h.must("", "who"); strings.Contains(out, "message:") || strings.Contains(out, "in other projects") ||
				!regexp.MustCompile(`(?m)^main\s+c3fb65eb pid 200  since [^\n]+  filed \d+ / used \d+$`).MatchString(out) {
				t.Errorf("a holder without its own session record should get no message part, the row unchanged, got:\n%s", out)
			}
		})
	}
}

func TestWhoFromACheckoutNotListedFreesNothing(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.must("", "release")

	other := newHarnessSharing(h)
	other.must("", "claim", "drive")

	outsider := impostor(h)
	h.processes.Kill(test.OtherOwnerPID)

	before := test.Tree(t, h.store())

	if out := outsider.must("", "who"); strings.Contains(out, "freed") || test.Tree(t, h.store()) != before {
		t.Errorf("who from a checkout not listed should free nothing, got:\n%s", out)
	}
}

func TestSweepIsGone(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	if got := h.run("", "sweep"); got.code != 1 {
		t.Errorf("sweep should be no subcommand any more, got %d %q", got.code, got.out)
	}

	if answer := h.mcp(`{"name":"sweep","arguments":{}}`); !strings.Contains(answer, "unknown tool: sweep") {
		t.Errorf("sweep should be no MCP tool any more, got %s", answer)
	}
}

func TestWhoCountsUnusedFreeSeatsRatherThanListingThem(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "warm")
	warmed := h.file("Gate finding warmed later", "body")
	h.must("", "release")
	h.clock.Advance(15 * 24 * time.Hour)
	h.must("", "claim", "busy")
	h.file("Gate finding filed lately", "body")
	h.must("", "show", warmed)
	h.must("", "release")
	h.must("", "claim", "idle")
	h.must("", "release")
	h.must("", "claim", "main")

	elsewhereID := test.OtherStore(t, h.roots(), "gitlab.com/acme/tool")
	elsewhere := h.roots().Store(elsewhereID, home.Memory)
	test.ClaimDeadSeat(t, elsewhere, "left", test.Session)
	test.ClaimDeadSeat(t, elsewhere, "gone", test.Session)

	out := h.must("", "who")

	for _, listed := range []string{
		`(?m)^main\s+c3fb65eb pid 200  since .+  filed 0 / used 0$`,
		`(?m)^busy\s+free  filed 1 / used \d+$`,
		`(?m)^warm\s+free  filed 0 / used 1$`,
	} {
		if !regexp.MustCompile(listed).MatchString(out) {
			t.Errorf("who should list a held seat and a free seat with any filing or use, %s missing, got:\n%s", listed, out)
		}
	}

	if strings.Contains(out, "idle") {
		t.Errorf("who should leave out a free seat nothing was filed by or used from, got:\n%s", out)
	}

	if !strings.HasSuffix(out, "\n+1 unused seat: agentic-memory who --unused  (run before claiming a new seat)\n") {
		t.Errorf("who should end the project's block counting the seats it left out and naming the flag, got:\n%s", out)
	}

	every := h.must("", "who", "--unused")
	if !regexp.MustCompile(`(?m)^idle\s+free  filed 0 / used 0$`).MatchString(every) || strings.Contains(every, "unused seat") {
		t.Errorf("who --unused should list the unused seats too, with no count, got:\n%s", every)
	}

	all := h.must("", "who", "--all")
	if strings.Contains(all, "idle") || strings.Contains(all, "left") ||
		!strings.Contains(all, "+1 unused seat: agentic-memory who --unused  (run before claiming a new seat)\n\ngitlab.com/acme/tool ("+elsewhereID+"):\n  +2 unused seats: agentic-memory who --all --unused  (run before claiming a new seat)") {
		t.Errorf("who --all should count each project's unused seats at the end of its block, got:\n%s", all)
	}

	if all := h.must("", "who", "--all", "--unused"); !strings.Contains(all, "idle") || !strings.Contains(all, "  left ") {
		t.Errorf("who --all --unused should list every project's unused seats, got:\n%s", all)
	}

	if answer := h.must("", "who", "--json"); !strings.Contains(answer, `"name":"idle"`) {
		t.Errorf("who --json should list every seat, unused ones too, got:\n%s", answer)
	}

	if answer := mcpMessage(t, h.mcp(`{"name":"who","arguments":{"unused":true}}`)); !listsSeat(answer, "idle") {
		t.Errorf("the MCP who with unused should list the unused seats, got:\n%s", answer)
	}
}
