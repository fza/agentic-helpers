package memory_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/sddcaps"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const (
	fdbox          = "gitlab.com/acme/fdbox"
	foreignClosed  = "20261024-120000-d-tac-k2m"
	foreignCloser  = "20261025-090000-s-tac-c1s"
	foreignActive  = "20261024-130000-d-tac-liv"
	foreignInTwo   = "20261024-140000-d-tac-two"
	notConnectedTo = "not in the local graph or any connected repo (sdd repo list)"
)

// withConnectedRepos gives the harness's checkout its local graph and an
// export carrying two connected repos, and sdd's cache of fdbox.
func withConnectedRepos(t *testing.T, h *harness) {
	t.Helper()

	withGraph(t, h)
	h.graph.Out = test.Fixture(t, "memory/sdd-export-connected.json")

	test.WriteFile(t, filepath.Join(h.cache, "sdd", filepath.FromSlash(fdbox), ".sdd", "graph", "2026", "10", "24-120000-d-tac-k2m.md"),
		"---\ntype: decision\n---\n")
}

func TestAConnectedRepoEntryLinksQualified(t *testing.T) {
	qualified := fdbox + ":" + foreignClosed

	cases := []struct {
		name string
		link func(h *harness) string
	}{
		{name: "--link", link: func(h *harness) string {
			return h.file("Gate finding citing fdbox", "body", "--link", qualified)
		}},
		{name: "link", link: func(h *harness) string {
			id := h.file("Gate finding citing fdbox", "body")
			h.must("", "link", id, qualified)

			return id
		}},
		{name: "the text", link: func(h *harness) string {
			return h.file("Gate finding citing fdbox", "follows "+qualified+" there")
		}},
		{name: "a bare id one connected repo holds", link: func(h *harness) string {
			return h.file("Gate finding citing fdbox", "body", "--link", foreignClosed)
		}},
		{name: "link with a bare id", link: func(h *harness) string {
			id := h.file("Gate finding citing fdbox", "body")
			h.must("", "link", id, foreignClosed)

			return id
		}},
		{name: "a bare id in the text", link: func(h *harness) string {
			return h.file("Gate finding citing fdbox", "follows "+foreignClosed)
		}},
	}

	for _, linked := range cases {
		t.Run(linked.name, func(t *testing.T) {
			h := newHarness(t)
			withConnectedRepos(t, h)
			h.must("", "claim", "main")

			id := linked.link(h)

			shown := h.must("", "show", id, "--no-heat")
			if !strings.Contains(shown, "links:\n  → "+qualified+" closed by "+foreignCloser) {
				t.Errorf("show should carry the qualified id and the connected repo's status, got:\n%s", shown)
			}

			first := strings.SplitN(h.must("", "search", "--term", "citing fdbox", "--no-heat", "--again"), "\n", 2)[0]
			if !strings.HasSuffix(first, "→ 1 ended") {
				t.Errorf("a line should fold the connected repo's closed entry into its ended count, got:\n%s", first)
			}
		})
	}
}

func TestAnActiveConnectedRepoEntryCarriesNoFlag(t *testing.T) {
	h := newHarness(t)
	withConnectedRepos(t, h)
	h.must("", "claim", "main")

	id := h.file("Gate finding citing an active one", "body", "--link", fdbox+":"+foreignActive)

	if first := strings.SplitN(h.must("", "show", id, "--no-heat"), "\n", 2)[0]; strings.Contains(first, "→ ") {
		t.Errorf("an active target in a connected repo should carry no flag, got:\n%s", first)
	}
}

func TestALinkNoConnectedRepoHoldsIsRefused(t *testing.T) {
	cases := []struct {
		name    string
		target  string
		refusal string
	}{
		{name: "a repo not connected", target: "gitlab.com/acme/elsewhere:" + foreignClosed,
			refusal: "gitlab.com/acme/elsewhere:" + foreignClosed + ": " + notConnectedTo},
		{name: "an id the connected repo lacks", target: fdbox + ":20261024-120000-d-tac-zzz",
			refusal: fdbox + ":20261024-120000-d-tac-zzz: " + notConnectedTo},
		{name: "a bare id no graph holds", target: "20261024-120000-d-tac-zzz",
			refusal: "20261024-120000-d-tac-zzz: " + notConnectedTo},
		{name: "a bare id only a repo without a repo_id holds", target: "20261024-150000-d-tac-ano",
			refusal: "20261024-150000-d-tac-ano: " + notConnectedTo},
		{name: "a bare id two connected repos hold", target: foreignInTwo,
			refusal: foreignInTwo + ": in several connected repos (gitlab.com/acme/devlab:" + foreignInTwo + ", " +
				fdbox + ":" + foreignInTwo + "); link one of them"},
	}

	for _, refused := range cases {
		t.Run(refused.name, func(t *testing.T) {
			h := newHarness(t)
			withConnectedRepos(t, h)
			h.must("", "claim", "main")
			id := h.file("Gate finding linking nowhere", "body")

			for _, args := range [][]string{
				{"new", "--title", "Gate finding linking elsewhere", "--topic", "gate", "--link", refused.target},
				{"link", id, refused.target},
			} {
				got := h.run("body", args...)
				if got.code != 1 || !strings.Contains(got.err, "agentic-memory: "+refused.refusal) {
					t.Errorf("%s should be refused saying why, got %d %q", args[0], got.code, got.err)
				}
			}

			out := h.must("follows "+refused.target, "new", "--title", "Gate finding naming elsewhere", "--topic", "gate")
			if !strings.Contains(out, "not linked (no such entry): "+refused.target) {
				t.Errorf("an id in the text that resolves to nothing should stay text, got:\n%s", out)
			}
		})
	}
}

func TestALocalEntryTheCheckoutLacksIsNoConnectedRepos(t *testing.T) {
	h := newHarness(t)
	withConnectedRepos(t, h)
	h.must("", "claim", "main")

	err := os.Remove(filepath.Join(h.repo, ".sdd", "graph", "2026", "10", "25-100000-d-tac-act.md"))
	if err != nil {
		t.Fatal(err)
	}

	got := h.run("body", "new", "--title", "Gate finding citing a missing one", "--topic", "gate", "--link", activeDecision)
	if got.code != 1 || !strings.Contains(got.err, activeDecision+": "+notConnectedTo) {
		t.Errorf("a bare id whose file the checkout lacks should be looked up in the connected repos only, got %d %q",
			got.code, got.err)
	}
}

func TestAQualifiedIDOfTheLocalRepoLinksBare(t *testing.T) {
	h := newHarness(t)
	withConnectedRepos(t, h)
	h.must("", "claim", "main")

	id := h.file("Gate finding citing ourselves", "body", "--link", repoID+":"+closedDecision)

	if shown := h.must("", "show", id, "--no-heat"); !strings.Contains(shown, "links:\n  → "+closedDecision+" closed by") {
		t.Errorf("a qualified id naming the local repo should link the local entry, got:\n%s", shown)
	}
}

// The statuses an earlier export cached stay readable, so the refusal is the
// probe's, not the cache's.
func TestWithoutExportAQualifiedIDIsRefused(t *testing.T) {
	h := newHarness(t)
	withConnectedRepos(t, h)
	h.must("", "claim", "main")
	h.file("Gate finding citing fdbox", "body", "--link", fdbox+":"+foreignClosed)

	h.sdd = &sddcaps.Probe{Runner: &test.Runner{Stock: true, Unreleased: true}}

	got := h.run("body", "new", "--title", "Gate finding citing fdbox", "--topic", "gate", "--link", fdbox+":"+foreignActive)
	if got.code != 1 || !strings.Contains(got.err,
		fdbox+":"+foreignActive+": resolving another repository's entry needs sdd export, which this sdd lacks") {
		t.Errorf("a qualified id should be refused where sdd has no export, got %d %q", got.code, got.err)
	}

	got = h.run("body", "new", "--title", "Gate finding citing fdbox", "--topic", "gate", "--link", foreignActive)
	if got.code != 1 || !strings.Contains(got.err, foreignActive+": "+notConnectedTo) {
		t.Errorf("a bare id the local graph lacks should be refused where sdd has no export, got %d %q", got.code, got.err)
	}

	id := h.file("Gate finding citing a local one", "body", "--link", activeDecision)
	if shown := h.must("", "show", id, "--no-heat"); !strings.Contains(shown, "links:\n  → "+activeDecision) {
		t.Errorf("a local link should still resolve by its file, got:\n%s", shown)
	}
}

func TestAQualifiedIDInTheTextLinksAsWritten(t *testing.T) {
	h := newHarness(t)
	withConnectedRepos(t, h)
	h.must("", "claim", "main")

	devlab := "gitlab.com/acme/devlab:" + foreignInTwo
	out := h.must("follows "+devlab, "new", "--title", "Gate finding citing devlab", "--topic", "gate")

	if strings.Contains(out, "not linked") {
		t.Errorf("an id qualified in the text should resolve as one id, the bare id within it unread, got:\n%s", out)
	}

	if shown := h.must("", "show", idPattern.FindString(out), "--no-heat"); !strings.Contains(shown, "links:\n  → "+devlab+"\n") {
		t.Errorf("the qualified id should link the entry in the repo it names, got:\n%s", shown)
	}
}

func TestAForeignLinkFlagsWhatTheExportSays(t *testing.T) {
	h := newHarness(t)
	withConnectedRepos(t, h)
	h.must("", "claim", "main")
	id := h.file("Gate finding citing an active one", "body", "--link", fdbox+":"+foreignActive)

	h.graph.Out = test.Fixture(t, "memory/sdd-export.json")
	later := time.Now().Add(time.Hour)

	err := os.Chtimes(filepath.Join(h.repo, ".sdd", "graph", "2026", "10", "25-100000-d-tac-act.md"), later, later)
	if err != nil {
		t.Fatal(err)
	}

	if first := h.must("", "show", id, "--no-heat"); !strings.Contains(first, "links:\n  → "+fdbox+":"+foreignActive+" gone") {
		t.Errorf("a target the export no longer carries should flag as gone, got:\n%s", first)
	}

	h.sdd = &sddcaps.Probe{Runner: &test.Runner{Stock: true, Unreleased: true}}

	err = os.Remove(filepath.Join(h.cacheDir(), "graph-status.json"))
	if err != nil {
		t.Fatal(err)
	}

	if first := strings.SplitN(h.must("", "show", id, "--no-heat"), "\n", 2)[0]; strings.Contains(first, "→ ") {
		t.Errorf("without an export nothing says what became of a foreign target, so no flag, got:\n%s", first)
	}
}

func TestAnExportWithoutLocalEntriesFallsBack(t *testing.T) {
	h := newHarness(t)
	withConnectedRepos(t, h)
	h.graph.Out = test.Fixture(t, "memory/sdd-export-foreign-only.json")
	h.must("", "claim", "main")

	got := h.run("body", "new", "--title", "Gate finding citing a foreign one", "--topic", "gate", "--link", fdbox+":"+foreignActive)
	if !strings.Contains(got.err, "graph status unavailable (sdd export: its output carries no local entries with a status)") {
		t.Errorf("an export carrying no local entry should not be trusted, and the read should say so, got %d %q", got.code, got.err)
	}
}

func TestGraphStatusIsCachedUntilAConnectedRepoChanges(t *testing.T) {
	h := newHarness(t)
	withConnectedRepos(t, h)
	h.must("", "claim", "main")
	h.file("Gate finding citing fdbox", "follows "+fdbox+":"+foreignClosed)

	calls := h.graph.Calls()

	h.must("", "search", "--term", "fdbox", "--no-heat")
	h.must("", "search", "--term", "fdbox", "--no-heat")

	if h.graph.Calls() != calls {
		t.Errorf("sdd export should not run again while neither graph changed, ran %d more times", h.graph.Calls()-calls)
	}

	later := time.Now().Add(time.Hour)
	synced := filepath.Join(h.cache, "sdd", filepath.FromSlash(fdbox), ".sdd", "graph", "2026", "10", "24-120000-d-tac-k2m.md")

	err := os.Chtimes(synced, later, later)
	if err != nil {
		t.Fatal(err)
	}

	h.must("", "search", "--term", "fdbox", "--no-heat")

	if h.graph.Calls() != calls+1 {
		t.Errorf("sdd export should run again once a connected repo's cache changes, ran %d more times", h.graph.Calls()-calls)
	}
}
