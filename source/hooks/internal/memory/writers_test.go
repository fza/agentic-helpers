package memory_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

// impostor is a second checkout declaring the same repo_id: another clone,
// or a repository naming a project it is not.
func impostor(h *harness) *harness {
	h.t.Helper()

	other := newHarnessSharing(h)
	other.repo = filepath.Join(filepath.Dir(h.repo), "impostor")
	test.WriteFile(h.t, filepath.Join(other.repo, ".sdd", "config.yaml"), "repo_id: "+repoID+"\n")
	test.WriteFile(h.t, filepath.Join(other.repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	other.git = &test.Git{Top: other.repo, Commit: "0badc0de", Branch: "main"}

	return other
}

func (h *harness) writersFile() string {
	return filepath.Join(h.home, ".claude", "agentic-helpers", filepath.FromSlash(repoID), "writers.json")
}

func TestTheFirstClaimListsItsCheckout(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	data, _ := os.ReadFile(h.writersFile())
	if strings.TrimSpace(string(data)) != `["`+filepath.Join(h.repo, ".git")+`"]` {
		t.Errorf("the first claim should list its checkout, got %s", data)
	}

	h.file("Gate reads Bash only today", "finding")
}

func TestACheckoutNotListedUsesNoMemory(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	id := h.file("Gate reads Bash only today", "finding")

	other := impostor(h)
	before := test.Tree(t, h.store())

	for _, args := range [][]string{
		{"claim", "drive"}, {"new", "--title", "Planted entry for the seat", "--topic", "gate"}, {"search", "--term", "gate"},
		{"show", id}, {"supersede", id, "--because", "planted"}, {"brief"}, {"topics"},
	} {
		got := other.run("planted", args...)
		if got.code != 1 || !strings.Contains(got.err, "agentic-memory: this memory belongs to other checkouts: "+h.repo+
			"; run `agentic-memory trust "+other.repo+"` from one of them to let this one write") {
			t.Errorf("%v from a checkout not listed should be refused, got %d %q", args, got.code, got.err)
		}
	}

	for _, verb := range []string{"session-start", "prompt", "tool", "stop", "session-end"} {
		got := other.run(hookPayload("", "", "gate"), verb)
		if got.code != 0 || got.out != "" || got.err != "" {
			t.Errorf("the %s hook should stay silent for a checkout not listed, got %d %q %q", verb, got.code, got.out, got.err)
		}
	}

	answer := other.mcp(`{"name":"search","arguments":{"term":["gate"]}}`)
	if !strings.Contains(answer, "this memory belongs to other checkouts") {
		t.Errorf("the MCP server should refuse a checkout not listed, got %s", answer)
	}

	for _, args := range [][]string{{"help"}, {"who"}, {"holder", "main"}} {
		got := other.run("", args...)
		if strings.Contains(got.err, "belongs to other checkouts") {
			t.Errorf("%v reads only and should answer anywhere, got %q", args, got.err)
		}
	}

	if test.Tree(t, h.store()) != before {
		t.Error("a checkout not listed should leave the store byte-identical")
	}
}

func TestTrustAdmitsAnotherCheckout(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	other := impostor(h)

	got := other.run("", "trust", other.repo)
	if got.code != 1 || !strings.Contains(got.err, "this memory belongs to other checkouts") {
		t.Errorf("a checkout not listed should never trust itself, got %d %q", got.code, got.err)
	}

	outside := h.run("", "trust", h.home)
	if outside.code != 1 || !strings.Contains(outside.err, "is no git checkout") {
		t.Errorf("trust of a path outside git should be refused, got %d %q", outside.code, outside.err)
	}

	if !strings.Contains(h.must("", "trust", other.repo), "trusted "+other.repo+": that checkout writes this project's memory and tool library now") {
		t.Error("trust should say which checkout writes now")
	}

	other.must("", "claim", "drive")

	data, _ := os.ReadFile(h.writersFile())
	if !strings.Contains(string(data), filepath.Join(other.repo, ".git")) {
		t.Error("trust should add the checkout to the project's one list")
	}

	answer := h.mcp(`{"name":"trust","arguments":{"checkout":"x"}}`)
	if !strings.Contains(answer, `trust takes no key \"checkout\"; it takes path`) {
		t.Errorf("the MCP trust tool should take exactly path, got %s", answer)
	}
}

func TestAListAnotherPieceWroteAdmitsTheSame(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	other := impostor(h)
	test.WriteFile(t, h.writersFile(), `["`+filepath.Join(h.repo, ".git")+`","`+filepath.Join(other.repo, ".git")+`"]`+"\n")

	other.must("", "claim", "drive")
}

func TestAnUnreadableListAdmitsNoOne(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	test.WriteFile(t, h.writersFile(), "not a list\n")

	got := h.run("finding", "new", "--title", "Gate reads Bash only today", "--topic", "gate")
	if got.code != 1 || !strings.Contains(got.err, "no call lands until "+h.writersFile()+" is mended") {
		t.Errorf("an unreadable list should admit no one, got %d %q", got.code, got.err)
	}

	data, _ := os.ReadFile(h.writersFile())
	if string(data) != "not a list\n" {
		t.Error("an unreadable list should never be claimed over")
	}
}

func TestAProjectWithoutAStoreGetsNoList(t *testing.T) {
	h := newHarness(t)

	h.run(hookPayload("", "", "gate"), "prompt")
	h.run("", "search", "--term", "gate")

	if test.Exists(t, filepath.Join(h.home, ".claude", "agentic-helpers")) {
		t.Error("a call touching no store should make no list")
	}
}

func TestALibrarysListBindsMemoryToo(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	other := impostor(h)
	legacy := filepath.Join(h.home, ".claude", "agentic-tools", filepath.FromSlash(repoID), "writers.json")
	test.WriteFile(t, legacy, `["`+filepath.Join(h.repo, ".git")+`"]`+"\n")
	_ = os.Remove(h.writersFile())

	read := other.run("", "search", "--term", "gate")
	if read.code != 1 || !strings.Contains(read.err, "this memory belongs to other checkouts: "+h.repo) {
		t.Errorf("a library's list should bind memory's reads while the project's list is missing, got %d %q", read.code, read.err)
	}

	got := other.run("", "claim", "drive")
	if got.code != 1 || !strings.Contains(got.err, "this memory belongs to other checkouts: "+h.repo) {
		t.Errorf("a library's list should bind memory while the project's list is missing, got %d %q", got.code, got.err)
	}
}

func TestAReadOfAnUnclaimedStoreClaimsNothing(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	_ = os.Remove(h.writersFile())

	for _, read := range [][]string{{"search", "--term", "gate"}, {"view", "--layout", "active:as-list"}} {
		h.must("", read...)

		if test.Exists(t, h.writersFile()) {
			t.Errorf("a read should claim nothing, %s did", read[0])
		}
	}

	h.file("Gate reads Bash only today", "finding")

	if !test.Exists(t, h.writersFile()) {
		t.Error("a write should claim an unclaimed store")
	}
}
