package memory_test

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestWhoListsEveryLiveSeatedSessionOnTheMachine(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	test.ClaimDeadSeat(t, h.store(), "gone", test.OtherSession)

	h.processes.Other()

	elsewhere := filepath.Join(h.home, ".claude", "agentic-memory", "gitlab.com", "acme", "tool")
	test.ClaimOtherSeat(t, elsewhere, "drive", test.OtherSession)
	test.ClaimDeadSeat(t, elsewhere, "left", test.Session)

	outsideGit(h)

	root := filepath.Join(h.home, ".claude", "agentic-memory")
	before := test.Tree(t, root) + test.Tree(t, h.cache)

	out := h.must("", "who")

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || !regexp.MustCompile(`^github\.com/acme/app\s+main\s+c3fb65eb  pid 200  since \d{4}-\d{2}-\d{2} \d{2}:\d{2} \S+$`).MatchString(lines[0]) ||
		!regexp.MustCompile(`^gitlab\.com/acme/tool\s+drive\s+a1b2c3d4  pid 300$`).MatchString(lines[1]) {
		t.Errorf("who should list the live seated sessions of every store, sorted, and nothing else, got:\n%s", out)
	}

	if after := test.Tree(t, root) + test.Tree(t, h.cache); after != before {
		t.Errorf("who should change nothing anywhere, a dead holder's claim and an unmigrated store included, got:\n%s\nbefore:\n%s",
			after, before)
	}

	if test.Exists(t, filepath.Join(elsewhere, "format.json")) {
		t.Error("who should read a store of the earlier format without migrating it")
	}

	lines2 := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"who","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"who","arguments":{"all":true}}}`,
	}, "\n") + "\n"

	mcp := h.must(lines2, "mcp")
	if !strings.Contains(mcp, `gitlab.com/acme/tool`) || !strings.Contains(mcp, `who takes no key \"all\"`) {
		t.Errorf("the MCP who should answer the same listing and refuse any key, got:\n%s", mcp)
	}
}

func TestWhoWithNoLiveSessionSaysSo(t *testing.T) {
	h := newHarness(t)

	got := h.run("", "who")
	if got.code != 0 || strings.TrimSpace(got.out) != "no live seated session on this machine" {
		t.Errorf("who with nobody seated should say so and exit 0, got %+v", got)
	}

}
