package memory_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/memory"
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
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	test.WriteFile(t, filepath.Join(repo, ".sdd", "config.yaml"), "repo_id: "+repoID+"\n")
	test.WriteFile(t, filepath.Join(repo, "internal", "gate", "gate.go"), "package gate\n")
	test.WriteFile(t, filepath.Join(repo, "README.md"), "# app\n")

	return &harness{
		t: t, repo: repo, home: filepath.Join(root, "home"), cache: filepath.Join(root, "cache"),
		settings: filepath.Join(root, "settings.json"),
		clock:    test.NewClock(time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)),
		git:      &test.Git{Top: repo, Commit: "15af1a8c", Branch: "main"},
		embedder: &test.Embedder{}, embedding: true, processes: test.NewProcesses(), session: test.Session,
	}
}

func (h *harness) store() string {
	return filepath.Join(h.home, ".claude", "agentic-memory", filepath.FromSlash(repoID))
}

func (h *harness) env() memory.Env {
	return memory.Env{
		Base: h.repo, Home: h.home, CacheRoot: h.cache, OwnerName: "claude", SessionID: h.session,
		Settings: []string{h.settings}, Getenv: func(string) string { return "" }, Now: h.clock.Now,
		Random: test.Random(), Processes: h.processes, Checkout: test.CommonDir{Dir: filepath.Join(h.repo, ".git")},
		Git: h.git, Embedders: h.embedder.Factory(h.embedding), Version: "test",
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

var idPattern = regexp.MustCompile(`m-[a-z0-9]{4}`)

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

	if !regexp.MustCompile(`^filed m-[a-z0-9]{4} Gate ignores MCP reads  \[main, normal, from agent, ` +
		`2026-10-25 \d\d:\d\d, 15af1a8c\]`).MatchString(out) {
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

func TestStandingNeedsASource(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	got := h.run("commit when asked", "new", "--title", "Owner allows commits on main", "--topic", "grants",
		"--retention", "standing")
	if got.code != 1 || !strings.Contains(got.err, "--from") {
		t.Errorf("a standing entry should need --from, got %d %q", got.code, got.err)
	}

	h.must("commit when asked", "new", "--title", "Owner allows commits on main", "--topic", "grants",
		"--retention", "standing", "--from", "user")
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

func TestSupersede(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	old := h.file("Gate reads Bash only today", "old finding about the gate")
	successor := h.file("Gate reads Bash and MCP now", "new finding about the gate")
	wrong := h.file("Gate never reads anything at all", "wrong finding about the gate")

	for _, args := range [][]string{
		{"supersede", old},
		{"supersede", old, "--by", successor, "--because", "both"},
	} {
		got := h.run("", args...)
		if got.code != 1 || !strings.Contains(got.err, "exactly one of --by") {
			t.Errorf("supersede should need exactly one of --by and --because, got %d %q", got.code, got.err)
		}
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

	if !regexp.MustCompile(wrong + `.*superseded 2026-10-25 \d\d:\d\d: misread the ledger`).MatchString(widened) {
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
		if got.code != 1 || !strings.Contains(got.err, id+" belongs to main") {
			t.Errorf("%s on another seat's entry should be refused, got %d %q", args[0], got.code, got.err)
		}
	}

	retired := h.file("Gate finding retired by its owner", "finding")
	h.must("", "retire", retired)

	if got := other.run("", "revive", retired); got.code != 1 || !strings.Contains(got.err, retired+" belongs to main") {
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
		h.must("grant text", "new", "--title", fmt.Sprintf("Standing grant number %d for main", index),
			"--topic", "grants", "--retention", "standing", "--from", "user")
	}

	retired := h.file("This entry is retired now", "gone")
	h.must("", "retire", retired)
	h.file("Working finding stays in view", "body")

	brief := h.must("", "brief")
	if len(brief) > 9000+200 {
		t.Errorf("the brief should stay within its budget, got %d characters", len(brief))
	}

	standing := strings.Index(brief, "standing (user-given grants and instructions, verify before relying):")
	working := strings.Index(brief, "working set:")

	if standing < 0 || working < 0 || standing > working {
		t.Errorf("standing entries should open the brief, ahead of the working set:\n%s", brief)
	}

	if !strings.Contains(brief, "standing entries exceed a third of the brief: 40; demote some") {
		t.Errorf("standing entries past a third should be named:\n%s", brief)
	}

	if strings.Contains(brief, "This entry is retired") {
		t.Error("a retired entry should leave the brief")
	}

	if !strings.Contains(brief, "Working finding stays in view") {
		t.Error("the working set should follow the standing entries")
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
		recalled bool
	}{
		{name: "the own seat's hits clear two deviations", bySeat: func(h *harness) *harness { return h }, recalled: true},
		{name: "another seat's hits need three", bySeat: newHarnessSharing},
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
				filer.file("Zebra quokka narwhal sighting "+number, "zebra quokka narwhal")
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
	if got.code != 1 || !strings.Contains(got.err, "--force") {
		t.Errorf("a live holder should be refused unless forced, got %d %q", got.code, got.err)
	}

	if !strings.Contains(other.must("", "claim", "main", "--force"), "seized from") {
		t.Error("--force should seize a wedged holder's seat")
	}

	if h.run("", "holder", "main").code != 0 {
		t.Error("a live holder should read as held")
	}

	if got := h.run("", "release", "main"); got.code != 1 || !strings.Contains(got.err, "only its holder releases it") {
		t.Errorf("a seat held by another live session should be released only by it, got %d %q", got.code, got.err)
	}

	h.processes.Kill(test.OtherOwnerPID)

	if !strings.Contains(h.must("", "sweep"), "freed") {
		t.Error("sweep should free a seat whose holder is gone")
	}

	if h.run("", "holder", "main").code != 1 {
		t.Error("a swept seat should read as free")
	}
}

func TestSeatNamesStayInsideTheStore(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	for _, args := range [][]string{{"release", "../main"}, {"holder", "../main"}, {"claim", "../x"}} {
		got := h.run("", args...)
		if got.code != 1 || !strings.Contains(got.err, "a seat name matches") {
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

	out := other.must(hookPayload("startup", "", ""), "session-start")
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
	test.WriteFile(t, filepath.Join(h.store(), "seats", "main.slice"), "session an-older-session\nstale turns of another session")

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
	if got.code != 2 || !strings.Contains(got.err, "two nudges unanswered") {
		t.Errorf("two unanswered nudges should make stop exit 2 with the backstop, got %d %q", got.code, got.err)
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

	for _, name := range []string{"new", "search", "show", "brief", "topics", "confirm", "retain", "retire",
		"supersede", "revive", "retitle", "tag", "merge", "holder", "claim", "release", "sweep"} {
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
