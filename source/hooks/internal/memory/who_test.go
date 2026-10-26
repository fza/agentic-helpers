package memory_test

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

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

	out := h.must("", "who")

	for _, pattern := range []string{
		`(?m)^gone\s+shelf gone\s+free \(holder a1b2c3d4 gone, freed\)  filed \d+ / used \d+ \(14d\)$`,
		`(?m)^impl\s+shelf row-7ah\s+free  filed \d+ / used \d+ \(14d\)$`,
		`(?m)^main\s+shelf main\s+c3fb65eb pid 200  since \d{4}-\d{2}-\d{2} \d{2}:\d{2} \S+  filed \d+ / used \d+ \(14d\)$`,
		`(?m)^rev\s+shelf row-7ah\s+free  filed \d+ / used \d+ \(14d\)$`,
		`(?m)^spare\s+shelf spare\s+reserved f00dbabe  filed \d+ / used \d+ \(14d\)$`,
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

	if out := h.must("", "who"); !regexp.MustCompile(`(?m)^spare\s+shelf spare\s+free \(holder f00dbabe gone, freed\)  filed \d+ / used \d+ \(14d\)$`).MatchString(out) {
		t.Errorf("who should free a reservation nobody took over in 10 minutes, got:\n%s", out)
	}

	_ = other
}

func TestWhoAllAddsEveryOtherProjectReadOnly(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.processes.Other()

	elsewhere := filepath.Join(h.home, ".claude", "agentic-memory", "gitlab.com", "acme", "tool")
	test.ClaimOtherSeat(t, elsewhere, "drive", test.OtherSession)
	test.ClaimDeadSeat(t, elsewhere, "left", test.Session)
	test.ClaimSeat(t, filepath.Join(h.store(), "backup", "format-1-20261025T170001.000000000Z"), "ghost", test.Session)

	before := test.Tree(t, elsewhere)

	if out := h.must("", "who"); strings.Contains(out, "drive") {
		t.Errorf("who should list the current project only, got:\n%s", out)
	}

	out := h.must("", "who", "--all")
	if !strings.Contains(out, "gitlab.com/acme/tool:\n") || !regexp.MustCompile(`(?m)^  drive\s+shelf drive\s+a1b2c3d4 pid 300  filed \d+ / used \d+ \(14d\)$`).MatchString(out) ||
		!regexp.MustCompile(`(?m)^  left\s+shelf left\s+free  filed \d+ / used \d+ \(14d\)$`).MatchString(out) || strings.Contains(out, "ghost") {
		t.Errorf("who --all should add every other project's seats under its name, backups aside, got:\n%s", out)
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
