package memory_test

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
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

// storeID is the id of the store the harness's checkout works on.
func (h *harness) storeID() string {
	h.t.Helper()

	return filepath.Base(filepath.Dir(h.store()))
}

func TestASecondCheckoutIsAskedAboutUntilBound(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	id := h.file("Gate reads Bash only today", "finding")
	store := h.storeID()

	other := impostor(h)
	before := test.Tree(t, h.store())

	question := "agentic-memory: no store is bound to the checkout " + other.repo + " yet: " +
		"it is another checkout of a repository a store already holds. Ask the user which store it belongs to, " +
		"then run `agentic-memory bind <store>` or `agentic-memory bind --new` from this checkout.\n" +
		"  " + store + ": " + repoID + "; checkouts " + h.repo

	if got := other.run(unrelatedStart(), "session-start"); got.code != 0 || !strings.Contains(got.out, question) {
		t.Errorf("SessionStart should tell the agent to ask the user, naming the store, got %d %q", got.code, got.out)
	}

	for _, args := range [][]string{{"claim", "drive"}, {"search", "--term", "gate"}, {"show", id}, {"brief"}} {
		got := other.run("planted", args...)
		if got.code != 1 || !strings.Contains(got.err, "no store is bound to the checkout "+other.repo) {
			t.Errorf("%v should be refused with the question while it stands, got %d %q", args, got.code, got.err)
		}
	}

	for _, verb := range []string{"prompt", "tool", "stop", "session-end"} {
		got := other.run(hookPayload("", "", "gate"), verb)
		if got.code != 0 || got.out != "" || got.err != "" {
			t.Errorf("the %s hook should stay silent while the question stands, got %d %q %q", verb, got.code, got.out, got.err)
		}
	}

	if answer := other.mcp(`{"name":"search","arguments":{"term":["gate"]}}`); !strings.Contains(answer, "no store is bound") {
		t.Errorf("the MCP server should refuse with the question, got %s", answer)
	}

	for _, args := range [][]string{{"help"}, {"who"}, {"holder", "main"}} {
		if got := other.run("", args...); strings.Contains(got.err, "no store is bound") {
			t.Errorf("%v reads only and should answer anywhere, got %q", args, got.err)
		}
	}

	if test.Tree(t, h.store()) != before {
		t.Error("the store should be untouched by a checkout bound to no store")
	}

	if bound := other.must("", "bind", store); !strings.Contains(bound, "bound "+other.repo+" to the store "+store+" ("+repoID+")") {
		t.Errorf("bind should name the checkout and the store, got %q", bound)
	}

	if got := other.run(unrelatedStart(), "session-start"); strings.Contains(got.out, "no store is bound") {
		t.Errorf("a bound checkout should hear no question again, got %q", got.out)
	}

	if !strings.Contains(other.must("", "search", "--all-seats", "--term", "Bash"), id) {
		t.Error("a checkout bound to the store should read its entries")
	}
}

func TestBindNewGivesTheCheckoutAStoreOfItsOwn(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	id := h.file("Gate reads Bash only today", "finding")
	store := h.storeID()

	other := impostor(h)

	bound := other.must("", "bind", "--new")
	if !strings.Contains(bound, "moved repo_id "+repoID+" from the store "+store) ||
		!strings.Contains(bound, "`agentic-memory merge "+store+" ") {
		t.Errorf("bind --new should say which identifier moved and offer the merge, got %q", bound)
	}

	other.must("", "claim", "main")

	if strings.Contains(other.must("", "search", "--all-seats", "--term", "Bash"), id) {
		t.Error("a checkout bound to a store of its own should not read the other store's entries")
	}

	if other.storeID() == store {
		t.Error("bind --new should bind the checkout to another store")
	}

	refusals := []struct {
		args []string
		says string
	}{
		{args: []string{"bind"}, says: "bind needs a store id, or --new"},
		{args: []string{"bind", store, "--new"}, says: "bind takes a store or --new, not both"},
		{args: []string{"bind", "not-a-store"}, says: "not-a-store"},
	}

	for _, refusal := range refusals {
		if got := other.run("", refusal.args...); got.code != 1 || !strings.Contains(got.out+got.err, refusal.says) {
			t.Errorf("%v should be refused with its reason, got %d %q", refusal.args, got.code, got.out+got.err)
		}
	}
}

func TestOneCheckoutPerRepositoryNeverHearsAQuestion(t *testing.T) {
	h := newHarness(t)
	other := otherProject(h)

	for _, session := range []*harness{h, other, h, other} {
		got := session.run(unrelatedStart(), "session-start")
		session.must("", "claim", "main")
		session.must("", "release")

		if strings.Contains(got.out+got.err, "store") {
			t.Errorf("a project with one checkout should never hear about its store, got %q %q", got.out, got.err)
		}
	}

	if h.storeID() == other.storeID() {
		t.Error("two repositories should keep two stores")
	}
}

func TestTwoAgentsInOneCheckoutReadOneStore(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	id := h.file("Gate reads Bash only today", "finding")

	second := newHarnessSharing(h)
	second.must("", "claim", "review")

	if !strings.Contains(second.must("", "search", "--all-seats", "--term", "Bash"), id) {
		t.Error("a second agent in the same checkout should read the first one's entries")
	}
}

func TestAnUnclaimedStoreGivesItsDataWithoutAQuestion(t *testing.T) {
	h := newHarness(t)

	donor := otherProject(h)
	donor.must("", "claim", "main")
	id := donor.file("Gate reads Bash only today", "kept by an earlier release")
	donorStore := donor.storeID()

	resolver := home.Resolver{Roots: h.roots()}

	unclaimed, err := resolver.Unclaim(context.Background(), repoID, []string{"/gone/app/.git"}, h.clock.Now())
	if err != nil {
		t.Fatalf("leaving an unclaimed store: %v", err)
	}

	err = os.Rename(h.roots().Store(donorStore, home.Memory), h.roots().UnclaimedStore(unclaimed, home.Memory))
	if err != nil {
		t.Fatalf("moving the data into the unclaimed store: %v", err)
	}

	got := h.run(unrelatedStart(), "session-start")
	if strings.Contains(got.out, "store") {
		t.Errorf("promoting an unclaimed store should ask nothing, got %q", got.out)
	}

	h.must("", "claim", "drive")

	if !strings.Contains(h.must("", "search", "--all-seats", "--term", "Bash"), id) || h.storeID() != unclaimed {
		t.Error("a checkout whose legacy key names an unclaimed store should get that store's data")
	}
}

func TestMergeJoinsTwoStores(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	mine := h.file("Gate reads Bash only today", "finding of the first store")
	into := h.storeID()

	other := otherProject(h)
	other.draws = rand.Reader
	other.must("", "claim", "main")
	theirs := other.file("Capture reads drafts only", "finding of the second store")
	from := other.storeID()
	test.WriteFile(t, filepath.Join(h.roots().Store(from, home.Tools), "word-count", "current"), "1\n")

	joined := h.atUserShell("merge", from, into)
	if joined.code != 0 || !strings.Contains(joined.out, "joined the store "+from+" into "+into) {
		t.Errorf("merge should say which store joined which, got %d %q %q", joined.code, joined.out, joined.err)
	}

	for _, id := range []string{mine, theirs} {
		if !strings.Contains(h.must("", "show", id), id) {
			t.Errorf("the joined store should hold %s under its own id", id)
		}
	}

	who := h.must("", "who", "--unused")
	if !strings.Contains(who, "main-2") {
		t.Errorf("a seat name both stores used should be numbered apart, got:\n%s", who)
	}

	if !test.Exists(t, filepath.Join(h.roots().Store(into, home.Tools), "word-count", "current")) {
		t.Error("the joined store's tool library should move over")
	}

	if other.storeID() != into {
		t.Error("the joined store's checkout should resolve to the surviving store")
	}
}

func TestMergeRefusesEntryIDsBothStoresHold(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("Gate reads Bash only today", "finding of the first store")
	into := h.storeID()

	other := otherProject(h)
	other.must("", "claim", "main")
	other.file("Capture reads drafts only", "finding of the second store, drawn from the same ids")
	from := other.storeID()

	before := test.Tree(t, h.roots().Stores())

	if got := h.atUserShell("merge", from, into); got.code != 1 || !strings.Contains(got.err, "both stores hold the entry ids m-") {
		t.Errorf("a merge of stores sharing an entry id should be refused naming it, got %d %q", got.code, got.err)
	}

	if test.Tree(t, h.roots().Stores()) != before {
		t.Error("a refused merge should change no store")
	}
}

func TestAReadOrAHookCreatesNoStore(t *testing.T) {
	h := newHarness(t)

	h.run("", "search", "--all-seats", "--term", "gate")
	h.run(unrelatedStart(), "session-start")
	h.run(hookPayload("", "", "gate"), "prompt")

	if test.Exists(t, h.roots().Stores()) {
		t.Error("a read or a hook should create no store")
	}
}

func TestTheMCPServerSeesABindMadeWhileItRuns(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	store := h.storeID()

	other := impostor(h)
	asks := 0
	other.git.Before = func(args []string) {
		if strings.Join(args, " ") != `config --get-regexp ^remote\..*\.url$` {
			return
		}

		asks++
		if asks != 2 {
			return
		}

		checkout, err := home.Inspect(context.Background(), other.repo, test.GitDirOf{}, nil)
		if err == nil {
			_, err = home.Resolver{Roots: h.roots()}.Bind(context.Background(), checkout, store)
		}

		if err != nil {
			t.Errorf("binding while the server runs: %v", err)
		}
	}

	if answer := other.mcp(`{"name":"who","arguments":{}}`); strings.Contains(answer, "no store is bound") ||
		!strings.Contains(answer, "main") {
		t.Errorf("a server started before a bind should work on the bound store from its next call, got %s", answer)
	}
}

func TestSessionStartDeletesUnclaimedStoresPastTheirGrace(t *testing.T) {
	cases := []struct {
		name   string
		config string
		kept   bool
	}{
		{name: "the default grace passed", config: "", kept: false},
		{name: "a longer grace set", config: "orphan_store_grace: 60d\n", kept: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			test.WriteFile(t, h.config, tc.config)

			since := h.clock.Now().Add(-31 * 24 * time.Hour)

			id, err := home.Resolver{Roots: h.roots()}.Unclaim(context.Background(), "github.com/acme/gone", nil, since)
			if err != nil {
				t.Fatalf("leaving an unclaimed store: %v", err)
			}

			h.must(unrelatedStart(), "session-start")

			if test.Exists(t, h.roots().UnclaimedStore(id, "")) != tc.kept {
				t.Error("SessionStart should delete an unclaimed store once orphan_store_grace has passed, and only then")
			}
		})
	}
}

func TestNothingLandsUnderTheClaudeHome(t *testing.T) {
	h := newHarness(t)
	h.must(unrelatedStart(), "session-start")
	h.must("", "claim", "main")
	h.file("Gate reads Bash only today", "finding")
	h.must(hookPayload("", "", "gate"), "prompt")

	if test.Exists(t, filepath.Join(h.home, ".claude")) || test.Exists(t, filepath.Join(h.home, ".cache")) {
		t.Error("memory should keep nothing under ~/.claude or ~/.cache")
	}

	if !test.Exists(t, h.store()) {
		t.Error("memory should keep its store in the home's stores")
	}
}

func TestANewerHomeIsRefused(t *testing.T) {
	h := newHarness(t)
	test.WriteFile(t, filepath.Join(h.roots().Data, "format.json"), `{"format": 9}`)

	if got := h.run("", "claim", "main"); got.code != 1 || !strings.Contains(got.err, "has format 9; this release reads up to 1") {
		t.Errorf("a call on a home of a newer format should be refused naming both formats, got %d %q", got.code, got.err)
	}

	if got := h.run(unrelatedStart(), "session-start"); got.out != "" {
		t.Errorf("a hook on a home of a newer format should stay silent, got %q", got.out)
	}
}
