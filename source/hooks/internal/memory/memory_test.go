package memory_test

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fza/agentic-helpers/source/hooks/internal/promptvector"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/memory"
	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
	"github.com/fza/agentic-helpers/source/hooks/internal/project"
	"github.com/fza/agentic-helpers/source/hooks/internal/projectkey"
	"github.com/fza/agentic-helpers/source/hooks/internal/sddcaps"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const repoID = "github.com/acme/app"

type harness struct {
	t         *testing.T
	repo      string
	home      string
	cache     string
	settings  string
	clock     *test.Clock
	git       *test.Git
	embedder  *test.Embedder
	embedding bool
	processes *test.Processes
	session   string

	waitPoll     time.Duration
	waitDeadline time.Duration
	// compactWindow stands for CLAUDE_CODE_AUTO_COMPACT_WINDOW.
	compactWindow int
	graph         *test.Graph
	sdd           *sddcaps.Probe
	// base is where the session works when it is not the repository's root,
	// and worktrees maps a worktree to its main checkout.
	base      string
	worktrees map[string]string
	// checkout stands in for the common-directory lookup where a test counts
	// it; nil takes one answering from the tree.
	checkout project.CommonDirLookup
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	test.WriteFile(t, filepath.Join(repo, ".sdd", "config.yaml"), "repo_id: "+repoID+"\n")
	test.WriteFile(t, filepath.Join(repo, "internal", "gate", "gate.go"), "package gate\n")
	test.WriteFile(t, filepath.Join(repo, "README.md"), "# app\n")
	test.WriteFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")

	return &harness{
		t: t, repo: repo, home: filepath.Join(root, "home"), cache: filepath.Join(root, "cache"),
		settings: filepath.Join(root, "settings.json"),
		clock:    test.NewClock(time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)),
		git:      &test.Git{Top: repo, Commit: "15af1a8c", Branch: "main"},
		embedder: &test.Embedder{}, embedding: true, processes: test.NewProcesses(), session: test.Session,
		graph: &test.Graph{Err: errors.New("no graph in this test")},
	}
}

// seatID is the id the store files the seat named name under.
func seatID(t *testing.T, store string, name string) string {
	t.Helper()

	registry, found, err := seat.LoadRegistry(store)
	if err != nil || !found {
		t.Fatalf("the store should carry a seat registry: %v", err)
	}

	id, known := registry.IDOf(name)
	if !known {
		t.Fatalf("the registry should name the seat %s", name)
	}

	return id
}

// shelfID is the id of the shelf the seat named name files onto.
func shelfID(t *testing.T, store string, name string) string {
	t.Helper()

	registry, _, err := seat.LoadRegistry(store)
	if err != nil {
		t.Fatalf("the store should carry a seat registry: %v", err)
	}

	return registry.Seats[seatID(t, store, name)].Shelf
}

// seatFile is the path of the seat's file with the given suffix, `.json` for
// its claim.
func (h *harness) seatFile(name string, suffix string) string {
	h.t.Helper()

	return filepath.Join(h.store(), "seats", seatID(h.t, h.store(), name)+suffix)
}

func (h *harness) store() string {
	return filepath.Join(h.home, ".claude", "agentic-memory", filepath.FromSlash(repoID))
}

func (h *harness) env() memory.Env {
	checkout := project.CommonDirLookup(test.GitDirOf{Worktrees: h.worktrees})
	if h.checkout != nil {
		checkout = h.checkout
	}

	return memory.Env{
		Base: cmp.Or(h.base, h.repo), Home: h.home, CacheRoot: h.cache, OwnerName: "claude", SessionID: h.session,
		Settings: []string{h.settings}, Getenv: func(string) string { return "" }, Now: h.clock.Now, Tmp: filepath.Join(h.home, "tmp"),
		Random: test.Random(), Processes: h.processes, Checkout: checkout,
		Git: h.git, Embedders: h.embedder.Factory(h.embedding), Version: "test",
		WaitPoll: h.waitPoll, WaitDeadline: h.waitDeadline, Graph: h.graph, Sdd: h.sdd,
		CompactWindow: h.compactWindow,
	}
}

type ran struct {
	out  string
	err  string
	code int
}

func (h *harness) run(stdin string, args ...string) ran {
	h.t.Helper()

	var captured test.Captured

	code := memory.Main(context.Background(), args, h.env(), captured.Streams(stdin))

	return ran{out: captured.Out.String(), err: captured.Err.String(), code: code}
}

func (h *harness) must(stdin string, args ...string) string {
	h.t.Helper()

	got := h.run(stdin, args...)
	if got.code != 0 {
		h.t.Fatalf("%v should succeed, exit %d: %s%s", args, got.code, got.out, got.err)
	}

	return got.out
}

var idPattern = regexp.MustCompile(`m-[a-z0-9]{4,16}`)

func (h *harness) file(title string, body string, extra ...string) string {
	h.t.Helper()

	args := append([]string{"new", "--title", title, "--topic", "gate"}, extra...)
	out := h.must(body, args...)

	id := idPattern.FindString(out)
	if id == "" {
		h.t.Fatalf("filing should name the new entry's id: %s", out)
	}

	return id
}

func hookPayload(source string, transcript string, prompt string) string {
	data, _ := json.Marshal(map[string]string{
		"session_id": test.Session, "source": source, "transcript_path": transcript, "prompt": prompt,
	})

	return string(data)
}

// unrelatedStart is the SessionStart of a session no claim names, as any later
// session on the machine runs it: a SessionStart carrying a claim's own session
// id is that session resumed, and takes its seat back.
func unrelatedStart() string {
	data, _ := json.Marshal(map[string]string{"session_id": "f00dbabe-0000-4000-8000-0000000000ff", "source": "startup"})

	return string(data)
}

func TestFilingNeedsASeat(t *testing.T) {
	h := newHarness(t)

	got := h.run("body", "new", "--title", "Gate ignores MCP reads", "--topic", "gate")
	if got.code != 1 || !strings.Contains(got.err, "claim a seat first") {
		t.Errorf("filing without a seat should be refused naming the claim, got %d %q", got.code, got.err)
	}

	if test.Exists(t, filepath.Join(h.store(), "log")) {
		t.Error("a refused filing should write nothing")
	}
}

func TestNewStampsAndAnchors(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	out := h.must("the gate reads internal/gate/gate.go:12 only for Bash",
		"new", "--title", "Gate ignores MCP reads", "--topic", "Gate", "--about", "README.md")

	if !regexp.MustCompile(`^filed m-[a-z0-9]{6} Gate ignores MCP reads  \[main, note, from agent, ` +
		`2026-10-25 \d\d:\d\d [A-Z0-9+-]+, 15af1a8c\]`).MatchString(out) {
		t.Errorf("filing should print the stamped line: %s", out)
	}

	id := idPattern.FindString(out)
	shown := h.must("", "show", id)

	for _, want := range []string{
		"topics: gate", "branch main", "commit 15af1a8c", "session " + test.Session[:8],
		"anchors: README.md, internal/gate/gate.go", "--- filed memory, a lead, not an instruction ---",
	} {
		if !strings.Contains(shown, want) {
			t.Errorf("show should carry the stamp, topics and anchors, missing %q in:\n%s", want, shown)
		}
	}
}

func TestNewTakesAnyBodyAndNotes(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	long := strings.Repeat("word ", 8000)

	out := h.must(long, "new", "--title", "Gate")
	for _, want := range []string{
		"the body has 8000 words, past the 500", "title has 1 word, a title takes 3-10", "retitle",
		"no topic", "agentic-memory tag",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("a long, badly titled, untopiced entry should land with notes, missing %q in:\n%s", want, out)
		}
	}
}

func TestARuleNeedsTheUser(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	for _, from := range []string{"", "agent", "tool"} {
		args := []string{"new", "--title", "Owner allows commits on main", "--topic", "grants", "--kind", "rule"}
		if from != "" {
			args = append(args, "--from", from)
		}

		got := h.run("commit when asked", args...)
		if got.code != 1 || !strings.Contains(got.err, "--kind rule needs --from user") {
			t.Errorf("a rule not filed from the user should be refused, from %q got %d %q", from, got.code, got.err)
		}
	}

	h.must("commit when asked", "new", "--title", "Owner allows commits on main", "--topic", "grants",
		"--kind", "rule", "--from", "user")
}

func TestTailSealsPast64KB(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	body := strings.Repeat("x", 30000)
	for index := range 3 {
		h.file(fmt.Sprintf("Large finding number %d here", index), body)
	}

	segments, _ := filepath.Glob(filepath.Join(h.store(), "log", "*.seg"))
	if len(segments) == 0 {
		t.Error("a tail past 64 KB should seal into a segment")
	}

	if !strings.Contains(h.must("", "search", "--term", "Large finding number 0"), "Large finding number 0") {
		t.Error("a sealed entry should still be found")
	}
}

func TestSearchScopeAndTerms(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("Gate ignores MCP reads always", "Bash only, no mcp tools")
	h.file("Seal writes the manifest", "rename then fsync", "--topic", "storage")

	other := newHarnessSharing(h)
	other.must("", "claim", "drive")
	other.file("Drive seat found the MCP gap", "mcp reads uncounted")

	cases := []struct {
		name string
		args []string
		want []string
		miss []string
	}{
		{
			name: "terms are case-insensitive and all required",
			args: []string{"search", "--term", "MCP", "--term", "BASH"},
			want: []string{"Gate ignores MCP reads"}, miss: []string{"Seal writes"},
		},
		{
			name: "the own seat is the default scope",
			args: []string{"search", "--term", "mcp"},
			want: []string{"Gate ignores"}, miss: []string{"Drive seat"},
		},
		{
			name: "all seats widen the scope",
			args: []string{"search", "--term", "mcp", "--all-seats"},
			want: []string{"Gate ignores", "Drive seat"},
		},
		{
			name: "a named seat narrows to it",
			args: []string{"search", "--term", "mcp", "--seat", "drive"},
			want: []string{"Drive seat"}, miss: []string{"Gate ignores"},
		},
		{
			name: "a topic filters",
			args: []string{"search", "--term", ".", "--topic", "storage"},
			want: []string{"Seal writes"}, miss: []string{"Gate ignores"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := h.must("", tc.args...)

			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("the search should find %q:\n%s", want, out)
				}
			}

			for _, miss := range tc.miss {
				if strings.Contains(out, miss) {
					t.Errorf("the search should leave out %q:\n%s", miss, out)
				}
			}
		})
	}
}

// newHarnessSharing is a second session on the same machine and project: the
// same store, another claude process.
func newHarnessSharing(h *harness) *harness {
	other := *h
	other.processes = h.processes.Other()
	other.session = test.OtherSession

	return &other
}

func TestSemanticSearchFailsClosed(t *testing.T) {
	h := newHarness(t)
	h.embedding = false
	h.must("", "claim", "main")
	h.file("Gate ignores MCP reads always", "only Bash reads count")

	got := h.run("", "search", "--query", "gate blind to tool calls")
	if got.code != 1 || !strings.Contains(got.err, "agentic-memory: semantic search needs an embedder: ") {
		t.Errorf("a semantic search without an embedder should fail closed naming the cause, got %d %q", got.code, got.err)
	}

	got = h.run("", "search", "--term", "gate", "--query", "gate")
	if got.code != 1 {
		t.Error("a hybrid search without an embedder should fail closed too")
	}

	if !strings.Contains(h.must("", "search", "--term", "gate"), "Gate ignores") {
		t.Error("a keyword search should answer without an embedder")
	}
}

func TestEntryFiledWhileEmbedderDownIsEmbeddedLater(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	h.embedder.Err = errors.New("connection refused")
	h.file("Seal writes the manifest atomically", "rename then fsync of the directory")

	h.embedder.Err = nil

	out := h.must("", "search", "--query", "manifest rename fsync directory")
	if !strings.Contains(out, "Seal writes the manifest") {
		t.Errorf("an entry filed while the embedder was down should be found by meaning once it answers:\n%s", out)
	}

	if !strings.Contains(h.must("", "search", "--term", "manifest", "--query", "manifest"), "Seal writes") {
		t.Error("a hybrid search should fuse both arms")
	}
}

func TestStampsCarryTheirZone(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	if !regexp.MustCompile(`\[main, note, from agent, 2026-10-2[45] \d\d:\d\d [A-Za-z0-9+-]+,`).MatchString(
		h.must("body", "new", "--title", "Gate reads Bash only today", "--topic", "gate")) {
		t.Error("a stamp should name its zone, so an hour the clocks repeat reads unambiguously")
	}
}

func TestABroadSearchWarmsOnlyItsTopResults(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	var ids []string
	for index, body := range []string{"needle needle needle", "needle needle", "needle", "needle", "needle"} {
		ids = append(ids, h.file(fmt.Sprintf("Finding number %d about needles", index), body))
	}

	h.must("", "search", "--term", "needle")

	brief := h.must("", "brief")
	if strings.Index(brief, ids[3]) < strings.Index(brief, ids[0]) {
		t.Errorf("a search should warm its top three results only, leaving the rest cooler:\n%s", brief)
	}
}

func TestSupersede(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	old := h.file("Gate reads Bash only today", "old finding about the gate")
	successor := h.file("Gate reads Bash and MCP now", "new finding about the gate")
	wrong := h.file("Gate never reads anything at all", "wrong finding about the gate")

	got := h.run("", "supersede", old)
	if got.code != 1 || !strings.Contains(got.err, "supersede needs --by <id>, --because") {
		t.Errorf("supersede should need a successor, a reason or both, got %d %q", got.code, got.err)
	}

	h.must("", "supersede", old, "--by", successor)
	h.must("", "supersede", wrong, "--because", "misread the ledger")

	plain := h.must("", "search", "--term", "finding about the gate")
	if strings.Contains(plain, old) || strings.Contains(plain, wrong) || !strings.Contains(plain, successor) {
		t.Errorf("superseded entries should leave the results:\n%s", plain)
	}

	widened := h.must("", "search", "--term", "finding about the gate", "--with-superseded")
	if strings.Contains(widened, old) {
		t.Errorf("an entry superseded by a successor should never return:\n%s", widened)
	}

	if !regexp.MustCompile(wrong + `.*superseded 2026-10-25 \d\d:\d\d [A-Z0-9+-]+: misread the ledger`).MatchString(widened) {
		t.Errorf("an entry superseded with a reason should return flagged with its time and reason:\n%s", widened)
	}
}

func TestOtherSeatsRights(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	id := h.file("Gate reads Bash only today", "finding about the gate")

	other := newHarnessSharing(h)
	other.must("", "claim", "drive")

	for _, args := range [][]string{
		{"retire", id}, {"retain", id, "long"}, {"retitle", id, "A new title for it"}, {"tag", id, "x"},
	} {
		got := other.run("", args...)
		if got.code != 1 || !strings.Contains(got.err, id+" is on shelf main") {
			t.Errorf("%s on another seat's entry should be refused, got %d %q", args[0], got.code, got.err)
		}
	}

	retired := h.file("Gate finding retired by its owner", "finding")
	h.must("", "retire", retired)

	if got := other.run("", "revive", retired); got.code != 1 || !strings.Contains(got.err, retired+" is on shelf main") {
		t.Errorf("only the owner should revive its retire, got %d %q", got.code, got.err)
	}

	other.must("", "confirm", id)
	other.must("", "supersede", id, "--because", "fixed in 2d1fc1f8")

	brief := h.must("", "brief")
	if !strings.Contains(brief, "changed by other seats:") || !strings.Contains(brief, "superseded: fixed in 2d1fc1f8 by drive") {
		t.Errorf("the owner's brief should list what other seats changed:\n%s", brief)
	}
}

func TestMergeAndRevive(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	id := h.file("Gate reads Bash only today", "finding", "--topic", "authn-gate")

	h.must("", "merge", "authn-gate", "gate-reads")

	if !strings.Contains(h.must("", "show", id), "topics: gate, gate-reads") {
		t.Error("a merged topic should read as its target")
	}

	h.must("", "retire", id)

	if !strings.Contains(h.must("", "search", "--term", "Gate reads"), "retired") {
		t.Error("a retired entry should stay in results, flagged")
	}

	h.must("", "revive", id)

	if strings.Contains(h.must("", "search", "--term", "Gate reads"), "retired") {
		t.Error("a revive should reverse the retire")
	}

	h.must("", "supersede", id, "--because", "wrong")
	h.clock.Advance(15 * 24 * time.Hour)

	got := h.run("", "revive", id)
	if got.code != 1 || !strings.Contains(got.err, id+" body dropped; file it again") {
		t.Errorf("a revive past 14 days should say the body is dropped, got %d %q", got.code, got.err)
	}
}

// A merge drops a retired body only once eight sealed segments pile up, so the
// log here holds what such a merge leaves: the entry's new row without its body.
func TestARetiredBodyDroppedByAMerge(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	log, err := memorylog.Open(filepath.Join(h.store(), "log"))
	if err != nil {
		t.Fatal(err)
	}

	filed := h.clock.Now().Add(-100 * 24 * time.Hour)
	err = log.Append(context.Background(),
		memorylog.Event{
			Kind: memorylog.KindNew, Entry: "m-r3t1", Time: filed, Seat: "main",
			Payload: []byte(`{"title":"Gate finding retired long ago","body":"","topics":["gate"],"retention":"normal","dropped":true}`),
		},
		memorylog.Event{Kind: memorylog.KindRetire, Entry: "m-r3t1", Time: filed.Add(time.Hour), Seat: "main"},
	)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(h.must("", "show", "m-r3t1"), "(body dropped 90 days after the retire)") {
		t.Error("show should say a retired body was dropped, and after how long")
	}

	test.WriteFile(t, h.settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"retired_body_days":120}}}}`)

	if !strings.Contains(h.must("", "show", "m-r3t1"), "(body dropped 120 days after the retire)") {
		t.Error("show should name the days the setting gives")
	}

	got := h.run("", "revive", "m-r3t1")
	if got.code != 1 || !strings.Contains(got.err, "m-r3t1 body dropped; file it again") {
		t.Errorf("a revive of a retired entry without its body should be refused, got %d %q", got.code, got.err)
	}
}

func TestEntryIDLength(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	if id := h.file("Gate finding at the default length", "finding"); len(id) != len("m-")+6 {
		t.Errorf("an entry id should take six characters unless set, got %s", id)
	}

	test.WriteFile(t, h.settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"entry_id_length":8}}}}`)

	if id := h.file("Gate finding at the length set", "finding"); len(id) != len("m-")+8 {
		t.Errorf("an entry id should take the length the setting gives, got %s", id)
	}
}

func TestAnIDFromAShorterLengthStillServes(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	log, err := memorylog.Open(filepath.Join(h.store(), "log"))
	if err != nil {
		t.Fatal(err)
	}

	err = log.Append(context.Background(), memorylog.Event{
		Kind: memorylog.KindNew, Entry: "m-r3t1", Time: h.clock.Now().Add(-time.Hour), Seat: "main",
		Payload: []byte(`{"title":"Gate finding minted with four characters","body":"finding","topics":["gate"],"retention":"normal"}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"show", "m-r3t1"},
		{"retitle", "m-r3t1", "Gate finding still served by its id"},
		{"tag", "m-r3t1", "ids"},
		{"retain", "m-r3t1", "long"},
		{"confirm", "m-r3t1"},
		{"retire", "m-r3t1"},
		{"revive", "m-r3t1"},
		{"supersede", "m-r3t1", "--because", "wrong"},
	} {
		h.must("", args...)
	}

	requests := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"show","arguments":{"ids":["m-r3t1"]}}}` + "\n"

	if !strings.Contains(h.must(requests, "mcp"), "Gate finding still served by its id") {
		t.Error("an MCP tool should serve an id minted at an earlier length")
	}
}

func TestReadsWithoutHeat(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	id := h.file("Gate finding a sweep keeps reading", "see .tmp/drafts")
	heatFile := filepath.Join(h.cache, "agentic-memory", filepath.FromSlash(repoID), "heat.json")

	h.must("", "show", id)

	before, err := os.ReadFile(heatFile)
	if err != nil {
		t.Fatalf("a show should write the heat file: %v", err)
	}

	h.clock.Advance(time.Hour)

	shown := h.must("", "show", id, "--no-heat")
	found := h.must("", "search", "--term", "sweep", "--no-heat")

	requests := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n" +
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"show","arguments":{"ids":["` + id + `"],"no_heat":true}}}` + "\n" +
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"search","arguments":{"term":["sweep"],"no_heat":true}}}` + "\n"
	served := h.must(requests, "mcp")

	after, err := os.ReadFile(heatFile)
	if err != nil {
		t.Fatal(err)
	}

	if string(after) != string(before) {
		t.Error("a read with no_heat should leave the heat file as it was")
	}

	if !strings.Contains(shown, "Gate finding a sweep keeps reading") || !strings.Contains(found, id) {
		t.Errorf("a read with no_heat should answer as one without it:\n%s\n%s", shown, found)
	}

	if strings.Count(served, `"no_heat"`) < 2 || !strings.Contains(served, "Gate finding a sweep keeps reading") {
		t.Errorf("the MCP search and show tools should take no_heat and still answer:\n%s", served)
	}

	h.must("", "show", id)

	warmed, err := os.ReadFile(heatFile)
	if err != nil {
		t.Fatal(err)
	}

	if string(warmed) == string(before) {
		t.Error("a show without the flag should still add heat")
	}

	h.must("", "search", "--term", "sweep")

	searched, err := os.ReadFile(heatFile)
	if err != nil {
		t.Fatal(err)
	}

	if string(searched) == string(warmed) {
		t.Error("a search without the flag should still warm its top hits")
	}
}

func TestMCPToolsTakeExactlyTheirKeys(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	id := h.file("Gate finding read over MCP", "finding")

	calls := []struct {
		name      string
		tool      string
		arguments string
		refusal   string
	}{
		{name: "a wrong-case key", tool: "show", arguments: `{"ids":["` + id + `"],"Force":true}`,
			refusal: `agentic-memory: show takes no key \"Force\"; it takes ids, no_heat`},
		{name: "a key in capitals", tool: "search", arguments: `{"term":["Gate"],"ALL_SEATS":true}`,
			refusal: `agentic-memory: search takes no key \"ALL_SEATS\"`},
		{name: "another tool's key", tool: "show", arguments: `{"ids":["` + id + `"],"force":true}`,
			refusal: `show takes no key \"force\"`},
		{name: "every offending key", tool: "search", arguments: `{"limt":3,"Term":["Gate"]}`,
			refusal: `search takes no key \"Term\", \"limt\"`},
		{name: "a negative skip", tool: "search", arguments: `{"term":["Gate"],"skip":-1}`,
			refusal: `--skip takes a number of 0 or more, not -1`},
		{name: "link's keys", tool: "link", arguments: `{"id":"` + id + `","target":"m-zzzz99","seat":"main"}`,
			refusal: `link takes no key \"seat\"; it takes id, target, no_heat`},
		{name: "untag's keys", tool: "untag", arguments: `{"id":"` + id + `","topic":["gate"]}`,
			refusal: `untag takes no key \"topic\"; it takes id, topics`},
		{name: "claim's removed force", tool: "claim", arguments: `{"name":"main","force":true}`,
			refusal: `claim takes no key \"force\"; it takes name`},
		{name: "a key for a tool taking none", tool: "topics", arguments: `{"all":true}`,
			refusal: `topics takes no key \"all\"; it takes none`},
		{name: "json on a tool without a JSON form", tool: "confirm", arguments: `{"ids":["` + id + `"],"json":true}`,
			refusal: `confirm takes no key \"json\"; it takes ids`},
		{name: "json on a read, unlisted but let through", tool: "show", arguments: `{"ids":["` + id + `"],"json":true}`},
		{name: "the exact keys", tool: "show", arguments: `{"ids":["` + id + `"],"no_heat":true}`},
		{name: "no arguments at all", tool: "topics", arguments: `{}`},
	}

	lines := []string{`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`}
	for index, call := range calls {
		lines = append(lines, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"%s","arguments":%s}}`,
			index+10, call.tool, call.arguments))
	}

	answers := map[int]string{}

	scanner := bufio.NewScanner(strings.NewReader(h.must(strings.Join(lines, "\n")+"\n", "mcp")))
	scanner.Buffer(nil, 1<<20)

	for scanner.Scan() {
		var answer struct {
			ID int `json:"id"`
		}

		_ = json.Unmarshal(scanner.Bytes(), &answer)
		answers[answer.ID] = scanner.Text()
	}

	if strings.Count(answers[2], `"additionalProperties":false`) != strings.Count(answers[2], `"inputSchema"`) {
		t.Error("every served tool schema should take no key beyond its properties")
	}

	for index, call := range calls {
		t.Run(call.name, func(t *testing.T) {
			answer := answers[index+10]
			refused := strings.Contains(answer, `"isError":true`)

			if call.refusal == "" {
				if refused || !strings.Contains(strings.ToLower(answer), "gate") {
					t.Errorf("a call with exactly the listed keys should answer as before: %s", answer)
				}

				return
			}

			if !refused || !strings.Contains(answer, call.refusal) {
				t.Errorf("a call with a key the tool does not list should be refused naming it: %s", answer)
			}
		})
	}
}

func TestStaleness(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	id := h.file("Gate reads Bash only today", "see internal/gate/gate.go and README.md")

	test.WriteFile(t, filepath.Join(h.repo, "internal", "gate", "gate.go"), "package gate // changed\n")

	err := os.Remove(filepath.Join(h.repo, "README.md"))
	if err != nil {
		t.Fatal(err)
	}

	h.git.Branch = "feature"

	out := h.must("", "search", "--term", "Gate reads")
	for _, want := range []string{"drifted: internal/gate/gate.go", "gone: README.md", "other branch: main"} {
		if !strings.Contains(out, want) {
			t.Errorf("a result should carry %q computed from the tree:\n%s", want, out)
		}
	}

	h.must("", "confirm", id)

	if strings.Contains(h.must("", "search", "--term", "Gate reads"), "drifted") {
		t.Error("confirm should re-anchor the entry to the tree")
	}
}

func TestBrief(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	for index := range 40 {
		h.must("grant text", "new", "--title", fmt.Sprintf("Standing grant number %d permitting commits, quoting the owner's instructions verbatim", index),
			"--topic", "grants", "--kind", "rule", "--from", "user")
	}

	retired := h.file("This entry is retired now", "gone")
	h.must("", "retire", retired)
	h.file("Working finding stays in view", "body")

	brief := h.must("", "brief")
	if len(brief) > 9000+200 {
		t.Errorf("the brief should stay within its budget, got %d characters", len(brief))
	}

	rules := strings.Index(brief, "rules (from the user, verify before relying):")
	notes := strings.Index(brief, "notes:")

	if rules < 0 || notes < 0 || rules > notes {
		t.Errorf("rules should come ahead of the notes:\n%s", brief)
	}

	if !strings.Contains(brief, "rules exceed a third of the brief: 40; retire or rekind some") {
		t.Errorf("rules past a third should be named:\n%s", brief)
	}

	if strings.Contains(brief, "This entry is retired") {
		t.Error("a retired entry should leave the brief")
	}

	if !strings.Contains(brief, "Working finding stays in view") {
		t.Error("the notes should follow the rules")
	}
}

var unrelated = []string{
	"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel", "india", "juliet", "kilo",
	"lima", "mike", "november", "oscar", "papa", "quebec", "romeo", "sierra",
}

func TestRecall(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	prompt := hookPayload("", "", "how does the tail seal manifest rename work")
	target := h.file("Tail seal manifest rename", "the tail seal writes the manifest by rename")

	for _, word := range unrelated[:18] {
		h.file("Unrelated finding about "+word, word+" "+word+" "+word)
	}

	if strings.Contains(h.must(prompt, "prompt"), "related") {
		t.Error("recall should stay silent below 20 live entries, even with a clear hit")
	}

	h.file("Unrelated finding about "+unrelated[18], unrelated[18])

	first := h.must(prompt, "prompt")
	if !strings.Contains(first, "agentic-memory: related") || !strings.Contains(first, target) {
		t.Errorf("a prompt touching an entry should recall it:\n%s", first)
	}

	if strings.Contains(h.must(prompt, "prompt"), target) {
		t.Error("an entry shown since the last compaction should not be recalled again")
	}
}

// Three entries sharing the prompt's words among twenty stand about 2.4
// deviations above the mean: past the own seat's bar, short of another seat's.
func TestRecallBarIsHigherForOtherSeats(t *testing.T) {
	cases := []struct {
		name     string
		bySeat   func(h *harness) *harness
		extra    []string
		recalled bool
	}{
		{name: "the own seat's hits clear two deviations", bySeat: func(h *harness) *harness { return h }, recalled: true},
		{name: "another seat's hits need three", bySeat: newHarnessSharing},
		{name: "another seat's hits filed for this seat clear two", bySeat: newHarnessSharing, extra: []string{"--for", "main"}, recalled: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")

			filer := tc.bySeat(h)
			if filer != h {
				filer.must("", "claim", "drive")
			}

			for _, number := range []string{"one", "two", "three"} {
				filer.file("Zebra quokka narwhal sighting "+number, "zebra quokka narwhal", tc.extra...)
			}

			for _, word := range unrelated[:17] {
				h.file("Unrelated finding about "+word, word+" "+word+" "+word)
			}

			got := strings.Contains(h.must(hookPayload("", "", "zebra quokka narwhal"), "prompt"), "Zebra quokka")
			if got != tc.recalled {
				t.Error("recall should hold another seat's entries to a higher bar than the own seat's")
			}
		})
	}
}

func TestClaimRules(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	if !strings.Contains(h.run("", "claim", "drive").err, "already holds main") {
		t.Error("a session should hold one seat at a time")
	}

	other := newHarnessSharing(h)

	got := other.run("", "claim", "main")
	if got.code != 1 || !strings.Contains(got.err, "main is held by session "+test.Session[:8]+" (pid 200), still running") ||
		!strings.Contains(got.err, "agentic-memory release") {
		t.Errorf("a running holder's seat should be refused, naming the holder and how the seat frees, got %d %q", got.code, got.err)
	}

	if got := other.run("", "claim", "main", "--force"); got.code != 1 || !strings.Contains(got.err, "claim takes no --force") {
		t.Errorf("claim should take no --force, got %d %q", got.code, got.err)
	}

	if got := other.run("", "release", "main"); got.code != 1 || !strings.Contains(got.err, "only its holder releases it") {
		t.Errorf("a seat held by another live session should be released only by it, got %d %q", got.code, got.err)
	}

	if h.run("", "holder", "main").code != 0 {
		t.Error("a running holder should keep its seat")
	}

	h.processes.Kill(test.OwnerPID)

	if !strings.Contains(other.must("", "who"), "freed") {
		t.Error("who should free a seat whose holder is gone")
	}

	if other.run("", "holder", "main").code != 1 {
		t.Error("a swept seat should read as free")
	}

	if !strings.Contains(other.must("", "claim", "main"), "main: ") {
		t.Error("a seat whose holder is gone should be claimed without any flag")
	}

	held, err := os.ReadFile(h.seatFile("main", ".json"))
	if err != nil || strings.Contains(string(held), "seized_from") {
		t.Errorf("a claim should record no seized_from, got %s %v", held, err)
	}
}

func TestSeatNamesStayInsideTheStore(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	for _, args := range [][]string{{"release", "../main"}, {"holder", "../main"}, {"claim", "../x"}} {
		got := h.run("", args...)
		if got.code != 1 || !strings.Contains(got.err, "a seat name is a letter") {
			t.Errorf("%v should be refused for its seat name, got %d %q", args, got.code, got.err)
		}
	}
}

func TestHolderJSON(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	var view map[string]any

	err := json.Unmarshal([]byte(h.must("", "holder", "main", "--json")), &view)
	if err != nil || view["session_id"] != test.Session || view["alive"] != true || view["seat"] != "main" {
		t.Errorf("holder --json should describe the live claim: %v %v", view, err)
	}

	err = json.Unmarshal([]byte(h.must("", "holder", "--session", test.Session, "--json")), &view)
	if err != nil || view["seat"] != "main" {
		t.Errorf("holder --session should find the seat a session holds: %v %v", view, err)
	}

	got := h.run("", "holder", "--session", "nobody", "--json")
	if got.code != 1 || !strings.Contains(got.out, `"seat":""`) {
		t.Errorf("a session holding nothing should answer an empty seat with exit 1, got %d %s", got.code, got.out)
	}
}

func TestSessionStart(t *testing.T) {
	h := newHarness(t)

	if got := h.run(hookPayload("startup", "", ""), "session-start"); got.out != "" || got.code != 0 {
		t.Errorf("a project without a store should hear nothing, got %q", got.out)
	}

	h.must("", "claim", "main")
	h.file("Gate reads Bash only today", "finding")

	other := newHarnessSharing(h)

	out := other.must(other.startHook("startup", ""), "session-start")
	if !strings.Contains(out, "agentic-memory: this session holds no seat. Seats: main ("+test.Session[:8]+", 1 live)") {
		t.Errorf("a seatless session should hear the seats where memory exists:\n%s", out)
	}

	if !strings.Contains(h.must(hookPayload("compact", "", ""), "session-start"), "agentic-memory: seat main") {
		t.Error("a compaction should bring back the brief")
	}
}

func (h *harness) transcript() string {
	h.t.Helper()

	path := filepath.Join(h.t.TempDir(), "transcript.jsonl")
	test.WriteFile(h.t, path, test.Fixture(h.t, "memory/transcript.jsonl"))

	return path
}

func TestCompactionReinjectsRecentTurns(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("Gate reads Bash only today", "finding")

	path := h.transcript()

	if h.must(hookPayload("startup", path, ""), "session-log", "1") != "" {
		t.Error("the recent turns should come back only after a compaction")
	}

	out := h.must(hookPayload("compact", path, ""), "session-log", "1")
	if !strings.Contains(out, "Wire the seal into the tail writer") {
		t.Errorf("the turns after the newest entry should come back:\n%s", out)
	}

	if strings.Contains(out, "A prompt before any entry was filed") {
		t.Error("turns before the newest entry should stay out")
	}

	if h.must(hookPayload("compact", path, ""), "session-log", "2") != "" {
		t.Error("a piece past the last should print nothing")
	}
}

func TestSliceSurvivesTheSession(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("Gate reads Bash only today", "finding")

	path := h.transcript()
	h.must(hookPayload("", path, ""), "session-end")

	h.processes.Kill(test.OwnerPID)

	next := newHarnessSharing(h)

	out := next.must("", "claim", "main")
	if !strings.Contains(out, "last turns of the previous session") || !strings.Contains(out, "Wire the seal") {
		t.Errorf("the next claim should print the previous session's slice:\n%s", out)
	}
}

func TestKilledSessionTurnsReadFromTranscript(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("Gate reads Bash only today", "finding")

	path := h.transcript()
	h.must(hookPayload("", path, "a prompt"), "prompt")
	test.WriteFile(t, h.seatFile("main", ".slice"), "session an-older-session\nstale turns of another session")

	h.processes.Kill(test.OwnerPID)

	out := newHarnessSharing(h).must("", "claim", "main")
	if !strings.Contains(out, "Wire the seal") {
		t.Errorf("a killed session's turns should be read from its recorded transcript:\n%s", out)
	}

	if strings.Contains(out, "stale turns") {
		t.Error("a slice another session left should never stand in for the killed session's turns")
	}
}

func (h *harness) sensor(percent float64) {
	h.t.Helper()

	test.WriteFile(h.t, filepath.Join(h.home, ".claude", "agentic-memory", "sessions", h.session+".ctx"),
		fmt.Sprintf(`{"used_percentage":%v,"size":200000,"at":%d}`, percent, time.Date(2026, 10, 25, 9, 0, 0, 0,
			time.UTC).Unix()))
}

func TestNudgeLadder(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("Gate reads Bash only today", "finding")

	payload := hookPayload("", "", "")

	h.sensor(40)

	if strings.Contains(h.must(payload, "prompt"), "Context near compaction") {
		t.Error("no nudge should fire below the threshold")
	}

	h.sensor(70)

	if !strings.Contains(h.must(payload, "prompt"), "Context near compaction") {
		t.Error("a nudge should fire past 65% of the window")
	}

	h.file("Answer to the nudge here", "where work stands")

	if strings.Contains(h.must(payload, "prompt"), "Context near compaction") {
		t.Error("an entry filed after the nudge should answer it")
	}

	h.sensor(90)
	h.must(payload, "prompt")
	h.must(payload, "prompt")

	got := h.run(payload, "stop")
	if got.code != 0 || !strings.Contains(test.AddedContext(got.out, "Stop"), "two nudges unanswered") {
		t.Errorf("two unanswered nudges should hold Stop with the backstop, got %d %q %q", got.code, got.out, got.err)
	}
}

func TestMCPServesEveryCommand(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	requests := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"new","arguments":` +
			`{"title":"Filed over MCP by main","body":"body","topic":["gate"]}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"search","arguments":{"term":["over MCP"]}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"retire","arguments":{"ids":["m-zzzz"]}}}`,
	}, "\n") + "\n"

	out := h.must(requests, "mcp")

	answers := map[int]string{}

	scanner := bufio.NewScanner(strings.NewReader(out))
	scanner.Buffer(nil, 1<<20)

	for scanner.Scan() {
		var answer struct {
			ID int `json:"id"`
		}

		_ = json.Unmarshal(scanner.Bytes(), &answer)
		answers[answer.ID] = scanner.Text()
	}

	for _, name := range []string{"new", "search", "show", "brief", "view", "topics", "confirm", "ack", "state",
		"retire", "supersede", "retitle", "tag", "untag", "link", "who", "claim", "release"} {
		if !strings.Contains(answers[2], `"name":"`+name+`"`) {
			t.Errorf("the MCP server should offer %s as a tool", name)
		}
	}

	if !strings.Contains(answers[3], "filed m-") || !strings.Contains(answers[4], "Filed over MCP by main") {
		t.Errorf("a tool call should answer what the command prints, resolving the same seat:\n%s\n%s",
			answers[3], answers[4])
	}

	if !strings.Contains(answers[5], `"isError":true`) {
		t.Errorf("a refused tool call should come back as an error result: %s", answers[5])
	}
}

func TestAClaimRecordsItsHolderStartInRFC3339(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	started := func() string {
		var held map[string]any

		raw, err := os.ReadFile(h.seatFile("main", ".json"))
		if err == nil {
			err = json.Unmarshal(raw, &held)
		}

		if err != nil {
			t.Fatalf("reading the claim: %v", err)
		}

		text, _ := held["started"].(string)

		return text
	}

	if started() != test.OwnerStarted {
		t.Errorf("a claim should record its holder's start as RFC 3339 in UTC, got %q", started())
	}

	test.ClaimSeatStarted(t, h.store(), "main", test.Session, "Do. 25 Sep. 14:00:00 2026")

	if h.run("", "holder", "main").code != 0 {
		t.Error("a claim holding ps text in another locale should read as held while its process runs")
	}

	h.must("", "claim", "main")

	if started() != test.OwnerStarted {
		t.Errorf("claiming the own seat again should rewrite a start held in locale text, got %q", started())
	}
}

func TestARetireCarriesItsReason(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	finished := h.file("Release work for the fetch lock", "owed: a patch for the launcher")
	dropped := h.file("Probe of the old launcher", "a scratch probe")

	h.must("", "retire", finished, "--because", "released as v2.2.2, commit 81df9dd")
	h.must("", "retire", dropped)

	for _, args := range [][]string{{"search", "--term", "launcher"}, {"show", finished}} {
		out := h.must("", args...)
		if !regexp.MustCompile(`retired [^\n]*: released as v2\.2\.2, commit 81df9dd`).MatchString(out) {
			t.Errorf("%v should print the retire's reason after its stamp, got:\n%s", args, out)
		}
	}

	if out := h.must("", "show", dropped); !regexp.MustCompile(`retired \d{4}-\d{2}-\d{2} \d{2}:\d{2} [A-Z+0-9-]+\n`).MatchString(out) ||
		strings.Contains(out, "retired: ") {
		t.Errorf("a retire without a reason should print the stamp alone, got:\n%s", out)
	}

	found := h.must("", "search", "--term", `v2\.2\.2`)
	if !strings.Contains(found, finished) || strings.Contains(found, dropped) {
		t.Errorf("--term should match a word appearing only in a retire's reason, got:\n%s", found)
	}

	h.must("", "revive", finished)

	if out := h.must("", "show", finished); regexp.MustCompile(`(?m)^m-\S+ .*retired `).MatchString(out) {
		t.Errorf("a revived entry should carry no retire reason, got:\n%s", out)
	}
}

func TestARetireFromAnEarlierReleaseReadsWithoutAReason(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	id := h.file("An entry retired long ago", "retired by a release writing no payload")

	log, err := memorylog.Open(filepath.Join(h.store(), "log"))
	if err != nil {
		t.Fatalf("opening the log: %v", err)
	}

	err = log.Append(context.Background(), memorylog.Event{Kind: memorylog.KindRetire, Entry: id, Time: h.clock.Now(), Seat: "main"})
	if err != nil {
		t.Fatalf("appending a payload-less retire: %v", err)
	}

	out := h.must("", "show", id)
	if !strings.Contains(out, "retired ") || strings.Contains(out, "retired: ") {
		t.Errorf("a retire with no payload should read as retired without a reason, got:\n%s", out)
	}
}

func (h *harness) heat() string {
	h.t.Helper()

	held, err := os.ReadFile(filepath.Join(h.cache, "agentic-memory", filepath.FromSlash(repoID), "heat.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		h.t.Fatalf("reading the heat: %v", err)
	}

	return string(held)
}

func listed(out string) []string {
	return regexp.MustCompile(`(?m)^m-[a-z0-9]+`).FindAllString(out, -1)
}

func TestSearchByStatus(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	var retired []string

	for index := range 12 {
		id := h.file(fmt.Sprintf("Lock finding number %02d recorded", index), "a finding about the lock")
		h.clock.Advance(time.Minute)

		if index%3 != 2 {
			retired = append(retired, id)
		}
	}

	// Retired newest-filed first, so the most recently retired is the oldest
	// filed and an order by filing time reads backwards.
	for _, id := range slices.Backward(retired) {
		h.must("", "retire", id, "--because", "fixed")
		h.clock.Advance(time.Minute)
	}

	live := h.file("A live entry about the gate", "still open")
	wrong := h.file("A wrong entry about the gate", "turned out false")
	h.must("", "supersede", wrong, "--because", "false")

	other := newHarnessSharing(h)
	other.must("", "claim", "drive")
	other.file("Another seat's retired lock note", "elsewhere")

	before := h.heat()

	out := h.must("", "search", "--status", "retired")
	got := listed(out)

	if len(got) != 8 || got[0] != retired[0] || got[7] != retired[len(retired)-1] {
		t.Errorf("a retired listing should hold the own seat's retired entries, the most recently retired first, at most 10, got:\n%s", out)
	}

	if h.heat() != before {
		t.Error("a status-only listing should warm nothing")
	}

	if got := listed(h.must("", "search", "--status", "live")); slices.Contains(got, retired[0]) || slices.Contains(got, wrong) || !slices.Contains(got, live) {
		t.Errorf("a live listing should exclude retired and superseded entries, got %v", got)
	}

	if got := listed(h.must("", "search", "--status", "superseded")); len(got) != 1 || got[0] != wrong {
		t.Errorf("a superseded listing should find the superseded entry without --with-superseded, got %v", got)
	}

	if got := listed(h.must("", "search", "--status", "retired", "--status", "superseded", "--term", "wrong|number 00")); len(got) != 2 {
		t.Errorf("--status should repeat and narrow a term search to its statuses, got %v", got)
	}

	if got := h.run("", "search", "--status", "done"); got.code != 1 || !strings.Contains(got.err, "--status takes live, retired, superseded") {
		t.Errorf("an unknown status should be refused naming the accepted words, got %d %q", got.code, got.err)
	}

	if got := h.run("", "search"); got.code != 1 || !strings.Contains(got.err, "search needs --term <regex>, --query <phrase> or --status <status>") {
		t.Errorf("a search naming nothing to find should be refused, got %d %q", got.code, got.err)
	}
}

func TestSearchPages(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	for index := range 25 {
		h.file(fmt.Sprintf("Paging finding number %02d recorded", index), "paging finding")
		h.clock.Advance(time.Minute)
	}

	var seen []string

	for _, page := range []struct {
		skip   string
		footer string
	}{
		{skip: "0", footer: "15 more: --skip 10"},
		{skip: "10", footer: "5 more: --skip 20"},
		{skip: "20"},
	} {
		out := h.must("", "search", "--term", "paging", "--skip", page.skip, "--no-heat")
		got := listed(out)

		if len(seen) > 0 && slices.ContainsFunc(got, func(id string) bool { return slices.Contains(seen, id) }) {
			t.Errorf("pages should hold disjoint results, page --skip %s repeats one of %v", page.skip, seen)
		}

		seen = append(seen, got...)

		if page.footer == "" && strings.Contains(out, "more: --skip") {
			t.Errorf("the last page should end without a footer, got:\n%s", out)
		}

		if page.footer != "" && !strings.HasSuffix(strings.TrimSpace(out), page.footer) {
			t.Errorf("a page with results past it should end naming how many and the next flag, got:\n%s", out)
		}
	}

	if len(seen) != 25 {
		t.Errorf("the pages together should cover every match, got %d", len(seen))
	}

	whole := listed(h.must("", "search", "--term", "paging", "--query", "paging finding", "--limit", "20", "--no-heat"))
	second := listed(h.must("", "search", "--term", "paging", "--query", "paging finding", "--skip", "10", "--no-heat"))

	if !slices.Equal(second, whole[10:20]) {
		t.Errorf("a hybrid search's second page should be results 11 to 20 of the same search with --limit 20, got %v against %v", second, whole[10:20])
	}

	if out := h.must("", "search", "--query", "paging finding", "--no-heat"); !regexp.MustCompile(`\nmore: --skip 10$`).MatchString(strings.TrimSpace(out)) {
		t.Errorf("a search by meaning should end a non-final page with the next flag alone, got:\n%s", out)
	}

	before := h.heat()
	h.must("", "search", "--term", "paging", "--skip", "10")

	if h.heat() != before {
		t.Error("a page past the first should warm nothing")
	}

	if got := h.run("", "search", "--term", "paging", "--skip", "-1"); got.code != 1 || !strings.Contains(got.err, "--skip takes a number of 0 or more") {
		t.Errorf("a negative --skip should be refused naming the flag, got %d %q", got.code, got.err)
	}
}

func TestAJapaneseTitleOfThreeToTenCharactersDrawsNoNote(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	out := h.must("出力が止まる", "new", "--title", "納品書の出力", "--topic", "gate")
	if strings.Contains(out, "note: title has") {
		t.Errorf("a six-character Japanese title should count as six words, got:\n%s", out)
	}

	id := idPattern.FindString(out)
	if retitled := h.must("", "retitle", id, "納品書が失敗"); strings.Contains(retitled, "note: title has") {
		t.Errorf("retitle should count a Japanese title by character too, got:\n%s", retitled)
	}
}

func TestFileNamesInAnyScriptAnchorAnEntry(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	for _, name := range []string{"docs/Übersicht.md", "src/納品書.go", "src/Größe.go"} {
		test.WriteFile(t, filepath.Join(h.repo, filepath.FromSlash(name)), "content\n")
	}

	id := h.file("Paths written in other scripts", "see docs/Übersicht.md, src/納品書.go and src/Größe.go:12, z.B. not docs/Fehlt.md")

	out := h.must("", "show", id)

	anchors := regexp.MustCompile(`(?m)^anchors: (.*)$`).FindStringSubmatch(out)
	if anchors == nil {
		t.Fatalf("show should list the entry's anchors, got:\n%s", out)
	}

	for _, want := range []string{"docs/Übersicht.md", "src/納品書.go", "src/Größe.go"} {
		if !strings.Contains(anchors[1], want) {
			t.Errorf("a file named in another script should anchor the entry, %s missing from %q", want, anchors[1])
		}
	}

	if strings.Contains(anchors[1], "Fehlt") || strings.Contains(anchors[1], "z.B") || strings.Contains(anchors[1], ":12") {
		t.Errorf("only existing files, without a line suffix, should anchor, got %q", anchors[1])
	}
}

func outsideGit(h *harness) {
	h.git.OutsideWorkTree = true
}

func TestOutsideAGitWorkTreeMemoryRefusesAndCreatesNothing(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("A finding in the project", "the project's own store exists beside the root")
	h.must("", "release")

	root := filepath.Join(h.home, ".claude", "agentic-memory")
	before := test.Tree(t, root)

	outsideGit(h)

	for _, args := range [][]string{{"claim", "main"}, {"new", "--title", "A finding outside git", "--topic", "gate"}, {"search", "--term", "x"}} {
		got := h.run("a body", args...)
		if got.code != 1 || !strings.Contains(got.err, "agentic-memory: "+h.repo+" is not inside a git work tree; memory works only in one") {
			t.Errorf("%v outside a work tree should be refused naming the directory, got %d %q", args, got.code, got.err)
		}
	}

	for _, event := range []string{"session-start", "prompt", "stop", "session-end"} {
		got := h.run(hookPayload("startup", "", "hello"), event)
		if got.code != 0 || got.out != "" || got.err != "" {
			t.Errorf("the %s hook outside a work tree should stay silent, got %d %q %q", event, got.code, got.out, got.err)
		}
	}

	if after := test.Tree(t, root); after != before {
		t.Errorf("nothing should change under the store root outside a work tree, got:\n%s\nbefore:\n%s", after, before)
	}

	if got := h.run("", "help"); got.code != 0 {
		t.Errorf("help should answer anywhere, got %d %q", got.code, got.err)
	}

	lines := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"claim","arguments":{"name":"main"}}}`,
	}, "\n") + "\n"

	out := h.must(lines, "mcp")
	if !strings.Contains(out, `"id":1,"result"`) || !strings.Contains(out, `is not inside a git work tree; memory works only in one`) ||
		!strings.Contains(out, `"isError":true`) {
		t.Errorf("the MCP server should start and answer every tool with the refusal as an error, got:\n%s", out)
	}
}

func TestTheGitLimitLiftsThroughTheOption(t *testing.T) {
	h := newHarness(t)
	outsideGit(h)
	test.WriteFile(t, h.settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"memory_require_git":false}}}}`)

	h.must("", "claim", "main")
	id := h.file("A finding outside git", "kept in a store keyed by the directory")

	if !strings.Contains(h.must("", "search", "--term", "outside"), id) {
		t.Error("with the limit lifted, a session outside git should file and find")
	}

	store := filepath.Join(h.home, ".claude", "agentic-memory", filepath.FromSlash(projectkey.DirectoryKey(h.repo)))
	if !test.Exists(t, filepath.Join(store, "seats", seatID(t, store, "main")+".json")) {
		t.Errorf("the store should be keyed by the directory's own hash, nothing under %s", store)
	}
}

func TestInsideGitTheOptionChangesNothing(t *testing.T) {
	h := newHarness(t)
	test.WriteFile(t, h.settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"memory_require_git":false}}}}`)

	h.must("", "claim", "main")

	if !test.Exists(t, h.seatFile("main", ".json")) {
		t.Error("inside a work tree the store should keep the project's key whatever the option says")
	}
}

func TestTheBriefNamesTheEntryLanguage(t *testing.T) {
	cases := []struct {
		name   string
		graph  string
		option string
		want   string
	}{
		{name: "the graph's language", graph: "language: de\n", option: "fr", want: "de"},
		{name: "a graph with an empty language takes the option", graph: "language: \"\"\n", option: "fr", want: "fr"},
		{name: "a graph naming none takes the option", option: "ja", want: "ja"},
		{name: "neither is English", want: "en"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			test.WriteFile(t, filepath.Join(h.repo, ".sdd", "config.yaml"), "repo_id: "+repoID+"\n"+tc.graph)

			if tc.option != "" {
				test.WriteFile(t, h.settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"memory_language":"`+tc.option+`"}}}}`)
			}

			header := regexp.MustCompile(`(?m)^agentic-memory: seat main, \d+ live, \d+ retired, entries in (\S+)$`)

			claimed := header.FindStringSubmatch(h.must("", "claim", "main"))
			if claimed == nil || claimed[1] != tc.want {
				t.Errorf("the brief at claim should name the resolved entry language, got %v", claimed)
			}

			compacted := header.FindStringSubmatch(h.must(hookPayload("compact", h.transcript(), ""), "session-start"))
			if compacted == nil || compacted[1] != tc.want {
				t.Errorf("the brief after a compaction should name it too, got %v", compacted)
			}
		})
	}
}

func TestStoredTimesAreUTC(t *testing.T) {
	h := newHarness(t)
	h.clock = test.NewClock(time.Date(2026, 10, 25, 16, 0, 0, 0, time.FixedZone("CET", 60*60)))
	h.must("", "claim", "main")
	id := h.file("Gate reads Bash only today", "finding")
	h.must("", "show", id)
	h.sensor(70)
	h.must(hookPayload("", "", ""), "prompt")

	offset := regexp.MustCompile(`\d{2}:\d{2}:\d{2}(?:\.\d+)?[+-]\d{2}:\d{2}`)

	for _, path := range []string{
		h.seatFile("main", ".json"),
		filepath.Join(h.store(), "log", "tail.jsonl"),
		filepath.Join(h.cache, "agentic-memory", filepath.FromSlash(repoID), "heat.json"),
		filepath.Join(h.cache, "agentic-memory", filepath.FromSlash(repoID), "briefs", seatID(t, h.store(), "main")+".json"),
		filepath.Join(h.cache, "agentic-memory", filepath.FromSlash(repoID), "ladder", h.session+".json"),
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}

		if offset.Match(raw) || !regexp.MustCompile(`\d{2}:\d{2}:\d{2}(?:\.\d+)?Z`).Match(raw) {
			t.Errorf("%s should hold its times in UTC, ending in Z, got %s", filepath.Base(path), raw)
		}
	}

	claim, _ := os.ReadFile(h.seatFile("main", ".json"))
	if !regexp.MustCompile(`"claimed": ?"2026-10-25T15:00:\d{2}(?:\.\d+)?Z"`).Match(claim) {
		t.Errorf("a claim should record when it was claimed in RFC 3339 UTC, got %s", claim)
	}
}

func TestAContextReadingIsReadInEitherForm(t *testing.T) {
	for _, at := range []string{`"2026-10-25T09:00:00Z"`, fmt.Sprint(time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC).Unix())} {
		t.Run(at, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")
			h.file("Gate reads Bash only today", "finding")
			test.WriteFile(t, filepath.Join(h.home, ".claude", "agentic-memory", "sessions", h.session+".ctx"),
				`{"used_percentage":70,"size":200000,"at":`+at+`}`)

			if !strings.Contains(h.must(hookPayload("", "", ""), "prompt"), "Context near compaction") {
				t.Error("a reading written as RFC 3339 or as Unix seconds should count")
			}
		})
	}
}

func TestTheReadingIsMeasuredAgainstTheWindowClaudeCodeCompactsAt(t *testing.T) {
	cases := []struct {
		name          string
		reading       string
		compactWindow int
		nudged        bool
	}{
		{name: "the plugin's window", reading: `,"compact_window":400000`, nudged: true},
		{name: "the plugin's window over the environment's", reading: `,"compact_window":400000`, compactWindow: 900000, nudged: true},
		{name: "the plugin's window as large as the model's", reading: `,"compact_window":1000000`, compactWindow: 400000},
		{name: "the environment's window without the plugin's", compactWindow: 400000, nudged: true},
		{name: "the model's window without either"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.compactWindow = tc.compactWindow
			h.must("", "claim", "main")
			h.file("Gate reads Bash only today", "finding")
			test.WriteFile(t, filepath.Join(h.home, ".claude", "agentic-memory", "sessions", h.session+".ctx"),
				`{"used_percentage":30,"size":1000000`+tc.reading+`,"at":"2026-10-25T09:00:00Z"}`)

			nudged := strings.Contains(h.must(hookPayload("", "", ""), "prompt"), "Context near compaction")
			if nudged != tc.nudged {
				t.Errorf("a nudge should fire only once the reading passes the threshold of the window compacted at")
			}
		})
	}
}

func TestASeatNameInAnyScriptIsOneSeat(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "café")

	if got := h.run("", "holder", "café"); got.code != 0 {
		t.Errorf("a seat claimed decomposed should be held under its precomposed spelling, got %d %q", got.code, got.err)
	}

	if got := h.run("", "holder", "CAFÉ"); got.code != 0 {
		t.Errorf("a seat name should be found whatever its case, got %d %q", got.code, got.err)
	}

	files, err := filepath.Glob(filepath.Join(h.store(), "seats", "*.json"))
	if err != nil || len(files) != 1 || filepath.Base(files[0]) != seatID(t, h.store(), "café")+".json" {
		t.Errorf("one seat should keep one file, under the id of its normal spelling, got %v %v", files, err)
	}
}

func TestAnMCPClaimNormalisesTheSeatName(t *testing.T) {
	h := newHarness(t)

	lines := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"claim","arguments":{"name":"Café"}}}`,
	}, "\n") + "\n"

	if out := h.must(lines, "mcp"); strings.Contains(out, `"isError":true`) {
		t.Errorf("an MCP claim should normalise the seat name before checking it, got:\n%s", out)
	}

	if got := h.run("", "holder", "café"); got.code != 0 {
		t.Errorf("the seat claimed over MCP should be held under its normal spelling, got %d %q", got.code, got.err)
	}
}

func TestRecallLeavesThePromptVectorForAgenticTools(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	for _, word := range unrelated[:19] {
		h.file("Unrelated finding about "+word, word+" "+word+" "+word)
	}

	h.file("Tail seal manifest rename", "the tail seal writes the manifest by rename")

	question := "how does the tail seal manifest rename work"
	h.must(hookPayload("", "", question), "prompt")

	_, held := promptvector.Load(promptvector.Dir(h.cache), h.session, question, h.embedder.Fingerprint())
	if !held {
		t.Error("recall should leave the prompt's vector in the session's slot")
	}

	h.must(hookPayload("", "", ""), "session-end")

	_, held = promptvector.Load(promptvector.Dir(h.cache), h.session, question, h.embedder.Fingerprint())
	if held {
		t.Error("the session's end should empty its slot")
	}
}

func (h *harness) ladderFile() string {
	return filepath.Join(h.cache, "agentic-memory", filepath.FromSlash(repoID), "ladder", test.Session+".json")
}

func TestTheReplayNudgeFiresOnceTheTurnsOutgrowTheReplay(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("Gate reads Bash only today", "finding")

	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	test.AppendTurns(t, path, h.clock.Now().Add(time.Hour), 10, 1000)

	prompt := hookPayload("", path, "")

	if strings.Contains(h.must(prompt, "prompt"), "no longer fit the post-compaction replay") {
		t.Error("turns that fit the replay should never be nudged")
	}

	var checked struct {
		Size int64 `json:"replay_checked_size"`
	}

	data, _ := os.ReadFile(h.ladderFile())
	_ = json.Unmarshal(data, &checked)
	info, _ := os.Stat(path)

	if checked.Size != info.Size() {
		t.Error("a check should record the transcript's size, so the next waits for a piece of growth")
	}

	test.AppendTurns(t, path, h.clock.Now().Add(2*time.Hour), 40, 1000)

	first := h.must(prompt, "prompt")
	if !strings.Contains(first, "Turns since your newest entry or state write no longer fit the post-compaction replay. "+
		"Write where work stands first: agentic-memory state < body. "+
		"File an entry only for a finding, an open question or a judgment call: "+
		"agentic-memory new --kind <kind> --title '<3-10 words>' --topic <name>... < body") {
		t.Errorf("turns outgrowing the replay should nudge the seat to file:\n%s", first)
	}

	test.AppendTurns(t, path, h.clock.Now().Add(3*time.Hour), 20, 1000)

	if strings.Contains(h.must(prompt, "prompt"), "no longer fit the post-compaction replay") {
		t.Error("the replay nudge should fire once until the seat files")
	}

	var steps struct {
		Nudges int `json:"nudges"`
	}

	data, _ = os.ReadFile(h.ladderFile())
	_ = json.Unmarshal(data, &steps)

	if steps.Nudges != 0 {
		t.Error("the replay nudge should leave the compaction nudge's count untouched")
	}

	got := h.run(prompt, "stop")
	if got.code != 0 || got.out != "" {
		t.Errorf("the replay nudge should never arm the Stop backstop, got %d %q", got.code, got.err)
	}

	h.clock.Advance(4 * time.Hour)
	h.file("Where the gate work stands now", "filed")
	test.AppendTurns(t, path, h.clock.Now().Add(time.Minute), 60, 1000)

	if !strings.Contains(h.must(prompt, "prompt"), "no longer fit the post-compaction replay") {
		t.Error("filing an entry should re-arm the replay nudge")
	}
}

func TestTheReplayCheckWaitsForThePieceOfGrowth(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("Gate reads Bash only today", "finding")

	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	test.AppendTurns(t, path, h.clock.Now().Add(time.Hour), 60, 1000)

	info, _ := os.Stat(path)
	test.WriteFile(t, h.ladderFile(), fmt.Sprintf(`{"rung":65,"replay_checked_size":%d}`, info.Size()-100))

	prompt := hookPayload("", path, "")

	if strings.Contains(h.must(prompt, "prompt"), "no longer fit the post-compaction replay") {
		t.Error("below a piece of growth since the last check the hook should not read the transcript")
	}

	test.AppendTurns(t, path, h.clock.Now().Add(3*time.Hour), 5, 1000)

	if !strings.Contains(h.must(prompt, "prompt"), "no longer fit the post-compaction replay") {
		t.Error("a piece of growth should let the check run")
	}
}

func TestACutReplaySaysWhatItDropped(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("Gate reads Bash only today", "finding")

	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	test.AppendTurns(t, path, h.clock.Now().Add(time.Hour), 60, 1000)

	out := h.must(hookPayload("compact", path, ""), "session-log", "1")
	if !regexp.MustCompile(`^agentic-memory: the turns since seat main's newest entry, in 5 pieces, newest last; \d+ older pieces cut, the full transcript at ` +
		regexp.QuoteMeta(path) + ":\n").MatchString(out) {
		t.Errorf("a cut replay should name the count and the transcript in its first piece:\n%.300s", out)
	}

	if strings.Contains(h.must(hookPayload("compact", path, ""), "session-log", "2"), "cut") {
		t.Error("only the first piece should carry the header")
	}

	short := h.transcript()

	whole := h.must(hookPayload("compact", short, ""), "session-log", "1")
	if !strings.HasPrefix(whole, "agentic-memory: the turns since seat main's newest entry, in 1 pieces, newest last:\n") {
		t.Errorf("a replay cutting nothing should keep its header:\n%.200s", whole)
	}
}
