package memory_test

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/sddcaps"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

// spawnsOfShow files one entry per link, each linking its own graph entry,
// and counts the subprocesses one `show` asks for: git, the common-directory
// lookup, `sdd export` and the sdd capability probe.
func spawnsOfShow(t *testing.T, links int) int {
	t.Helper()

	h := newHarness(t)
	checkout := &test.CountedCommonDir{Lookup: test.GitDirOf{}}
	h.checkout = checkout
	runner := &test.Runner{}
	h.sdd = &sddcaps.Probe{Runner: runner}
	h.graph.Err, h.graph.Out = nil, test.Fixture(t, "memory/sdd-export.json")
	h.must("", "claim", "main")

	var first string

	for index := range links {
		id := fmt.Sprintf("20261027-0000%02d-s-tac-a%02d", index, index)
		test.WriteFile(t, filepath.Join(h.repo, ".sdd", "graph", "2026", "10", fmt.Sprintf("27-0000%02d-s-tac-a%02d.md", index, index)),
			"---\ntype: signal\n---\n")

		filed := h.file(fmt.Sprintf("Finding number %d citing the graph", index), "see "+id)
		if first == "" {
			first = filed
		}
	}

	before := len(h.git.Calls()) + checkout.Calls() + h.graph.Calls() + len(runner.Calls)

	h.must("", "show", first, "--no-heat")

	return len(h.git.Calls()) + checkout.Calls() + h.graph.Calls() + len(runner.Calls) - before
}

func TestShowSpawnsTheSameWhateverTheLinkCount(t *testing.T) {
	few := spawnsOfShow(t, 2)
	many := spawnsOfShow(t, 24)

	if few != many {
		t.Errorf("one show should spawn as many subprocesses for many links as for few, got %d and %d", few, many)
	}

	if many > 2 {
		t.Errorf("one show should look the main checkout up once and ask git about its own checkout once, got %d spawns",
			many)
	}
}

func TestACheckoutWithoutACommitIsAskedQuestionByQuestion(t *testing.T) {
	h := newHarness(t)
	h.git.Unborn = true
	h.must("", "claim", "main")

	filed := h.must("body", "new", "--title", "Finding filed before any commit", "--topic", "gate")
	out := h.must("", "show", idPattern.FindString(filed))
	if !strings.HasPrefix(filed, "filed m-") || strings.Contains(out, h.git.Commit) {
		t.Errorf("a checkout without a commit should still be a work tree, its entry stamped without a commit, got:\n%s",
			out)
	}
}

func TestTheCommitIsStampedInEightCharacters(t *testing.T) {
	h := newHarness(t)
	h.git.Commit = "15af1a8c0d2e4f6a8b0c2d4e6f8a0b2c4d6e8f0a"
	h.must("", "claim", "main")

	filed := h.must("body", "new", "--title", "Finding stamped with its commit", "--topic", "gate")
	out := h.must("", "show", idPattern.FindString(filed))
	if !strings.Contains(out, ", 15af1a8c]") {
		t.Errorf("the full commit git answers should be stamped cut to eight characters, got:\n%s", out)
	}
}

func TestEveryMCPCallAsksAboutTheCheckoutAgain(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("Finding the search reads back", "body")

	lines := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n"
	for id := 2; id <= 4; id++ {
		lines += fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"search","arguments":{"term":["search"]}}}`, id) + "\n"
	}

	before := combinedAsks(h.git.Calls())

	h.must(lines, "mcp")

	if combinedAsks(h.git.Calls())-before < 3 {
		t.Error("each MCP call should ask git about the checkout anew, since a branch can change while the server runs")
	}
}

func TestTheWaiterAsksAboutTheCheckoutAtEachLook(t *testing.T) {
	h, other := twoSeats(t)
	other.must(hookPayload("", "", ""), "tool")
	h.clock.Advance(time.Minute)

	var group sync.WaitGroup

	var got ran

	group.Go(func() { got = other.waiting(20*time.Millisecond, 10*time.Second).run(other.stop(), "wait") })

	time.Sleep(100 * time.Millisecond)
	h.git.SwitchBranch("feature")
	h.file("Review owes the feature diff", "a handover", "--for", "idea")

	group.Wait()

	if got.code != 2 || strings.Contains(got.err, "other branch") {
		t.Errorf("an arrival filed on the branch the checkout switched to should not read as another branch's, got %+v", got)
	}
}

func combinedAsks(calls [][]string) int {
	asked := 0

	for _, call := range calls {
		if slices.Equal(call, []string{"rev-parse", "--is-inside-work-tree", "--show-toplevel", "HEAD", "--abbrev-ref", "HEAD"}) {
			asked++
		}
	}

	return asked
}
