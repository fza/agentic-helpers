package tools_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/tools"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const (
	ownRepo   = "github.com/fza/app"
	otherRepo = "gitlab.com/format-d/devops/format-d-fdbox"
)

type harness struct {
	t         *testing.T
	repo      string
	home      string
	cache     string
	settings  string
	repoID    string
	session   string
	clock     *test.Clock
	git       *test.Git
	embedder  *test.Embedder
	embedding bool
	processes *test.Processes
	variables map[string]string
	lockWait  time.Duration
	worktrees map[string]string
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	root := t.TempDir()

	h := &harness{
		t: t, home: filepath.Join(root, "home"), cache: filepath.Join(root, "cache"),
		settings: filepath.Join(root, "settings.json"),
		clock:    test.NewClock(time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)),
		embedder: &test.Embedder{}, embedding: true, processes: test.NewProcesses(), session: test.Session,
		variables: map[string]string{}, lockWait: 200 * time.Millisecond, worktrees: map[string]string{},
	}

	h.project(filepath.Join(root, "app"), ownRepo)

	return h
}

func (h *harness) project(repo string, repoID string) {
	h.repo, h.repoID = repo, repoID
	test.WriteFile(h.t, filepath.Join(repo, ".sdd", "config.yaml"), "repo_id: "+repoID+"\n")
	test.WriteFile(h.t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	h.git = &test.Git{Top: repo, Commit: "15af1a8c", Branch: "main"}
}

// elsewhere is a second project on the same machine: its own checkout and
// session, the same home, cache, embedder and clock.
func (h *harness) elsewhere(repoID string) *harness {
	h.t.Helper()

	other := *h
	other.variables = map[string]string{}
	other.session = test.OtherSession
	other.project(filepath.Join(filepath.Dir(h.repo), strings.ReplaceAll(repoID, "/", "_")), repoID)

	return &other
}

func (h *harness) store() string {
	return filepath.Join(h.home, ".claude", "agentic-tools", filepath.FromSlash(h.repoID))
}

func (h *harness) env() tools.Env {
	return tools.Env{
		Base: h.repo, Home: h.home, CacheRoot: h.cache, OwnerName: "claude", SessionID: h.session,
		Settings: []string{h.settings}, Getenv: func(name string) string { return h.variables[name] },
		Now: h.clock.Now, Processes: h.processes, Checkout: test.GitDirOf{Worktrees: h.worktrees},
		Git: h.git, Embedders: h.embedder.Factory(h.embedding), Version: "test", LockWait: h.lockWait,
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

	code := tools.Main(context.Background(), args, h.env(), captured.Streams(stdin))

	return ran{out: captured.Out.String(), err: captured.Err.String(), code: code}
}

func (h *harness) must(args ...string) string {
	h.t.Helper()

	got := h.run("", args...)
	if got.code != 0 {
		h.t.Fatalf("%v should succeed, exit %d: %s%s", args, got.code, got.out, got.err)
	}

	return got.out
}

func (h *harness) refused(says string, args ...string) {
	h.t.Helper()

	got := h.run("", args...)
	if got.code != 1 || !strings.HasPrefix(got.err, "refused: ") || !strings.Contains(got.err, says) {
		h.t.Errorf("%v should be refused saying %q, got exit %d: %s%s", args, says, got.code, got.out, got.err)
	}
}

// hook runs a hook verb with a payload for this session.
func (h *harness) hook(verb string, fields map[string]any) string {
	h.t.Helper()

	payload := map[string]any{"session_id": h.session}
	for key, value := range fields {
		payload[key] = value
	}

	data, err := json.Marshal(payload)
	if err != nil {
		h.t.Fatalf("encoding the payload: %v", err)
	}

	got := h.run(string(data), verb)
	if got.code != 0 || got.err != "" {
		h.t.Fatalf("the %s hook should pass silently on stderr, exit %d: %s", verb, got.code, got.err)
	}

	return got.out
}

// file writes a file into the checkout and returns its path.
func (h *harness) file(relative string, content string) string {
	h.t.Helper()

	at := filepath.Join(h.repo, filepath.FromSlash(relative))
	test.WriteFile(h.t, at, content)

	return at
}

// tool adds a one-file tool with its summary and usage.
func (h *harness) tool(name string, summary string, extra ...string) {
	h.t.Helper()

	from := h.file("scripts/"+name+".py", "# "+name+"\nprint('"+name+"')\n# "+summary+"\n")
	args := append([]string{"add", name, "--from", from, "--summary", summary, "--usage", "python3 " + name + ".py <file>"},
		extra...)
	h.must(args...)
}

func (h *harness) read(relative string) string {
	h.t.Helper()

	data, err := os.ReadFile(filepath.Join(h.store(), filepath.FromSlash(relative)))
	if err != nil {
		h.t.Fatalf("reading %s: %v", relative, err)
	}

	return string(data)
}

func (h *harness) indexed() map[string]any {
	h.t.Helper()

	var held struct {
		Tools map[string]any `json:"tools"`
	}

	err := json.Unmarshal([]byte(h.read("index.json")), &held)
	if err != nil {
		h.t.Fatalf("index.json should decode: %v", err)
	}

	return held.Tools
}

func TestAddRegistersAFileAndAFolder(t *testing.T) {
	h := newHarness(t)

	from := h.file("scripts/word_count.py", "import sys\nprint(len(sys.stdin.read().split()))\n")

	out := h.must("add", "word-count", "--from", from, "--summary", "Count the words of standard input",
		"--usage", "python3 word_count.py < file", "--inputs", "text on stdin")
	if !strings.Contains(out, "added word-count: `agentic-tools take word-count` copies it") {
		t.Errorf("add should print the take line, got %q", out)
	}

	if h.read("word-count/v1/word_count.py") == "" || !strings.Contains(h.read("word-count/v1/tool.json"), `"inputs": "text on stdin"`) {
		t.Error("v1 should hold the file and its tool.json")
	}

	if strings.TrimSpace(h.read("word-count/current")) != "1" {
		t.Error("v1 should be current")
	}

	h.file("tools/split/split.sh", "#!/bin/sh\ntr ' ' '\\n'\n")
	h.file("tools/split/lib/helpers.awk", "{ print }\n")
	h.file("tools/split/tool.json", `{"name":"from-a-copy"}`)
	h.must("add", "word-split", "--from", filepath.Join(h.repo, "tools", "split"), "--summary", "Put each word of a text on its own line",
		"--usage", "sh split.sh < file")

	if h.read("word-split/v1/split.sh") == "" || h.read("word-split/v1/lib/helpers.awk") == "" {
		t.Error("a folder's files should land at the top of v1, its subfolders kept")
	}

	if strings.Contains(h.read("word-split/v1/tool.json"), "from-a-copy") {
		t.Error("a folder's own tool.json should never become the version's")
	}

	index := h.indexed()
	if index["word-count"] == nil || index["word-split"] == nil {
		t.Error("index.json should carry every added tool")
	}
}

func TestAddRefusals(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")

	other := h.elsewhere(otherRepo)
	other.tool("line-count", "Count the lines of a file quickly")

	cases := []struct {
		name string
		args []string
		says string
	}{
		{"a bad name", []string{"add", "Word_Count", "--from", h.file("a.py", "a"), "--summary", "x y z", "--usage", "u"},
			"is no tool name"},
		{"a taken name", []string{"add", "word-count", "--from", h.file("b.py", "b"), "--summary", "Another thing", "--usage", "u"},
			"word-count is taken in this project: `agentic-tools update word-count --why '...'`"},
		{"a two-line summary", []string{"add", "two-lines", "--from", h.file("c.py", "c"), "--summary", "one\ntwo", "--usage", "u"},
			"--summary is one line"},
		{"no usage", []string{"add", "no-usage", "--from", h.file("d.py", "d"), "--summary", "Does a thing"}, "--usage is required"},
		{"a file of a foreign tool", []string{"add", "copied", "--from", h.file("line.py", "# line-count\nprint('line-count')\n# Count the lines of a file quickly\n"),
			"--summary", "Something else entirely here", "--usage", "u"},
			"line.py is line-count@format-d-fdbox v1 byte for byte: `agentic-tools take line-count@format-d-fdbox` copies it"},
		{"a near duplicate of a foreign tool", []string{"add", "lines", "--from", h.file("e.py", "e"),
			"--summary", "Count the lines of a file quickly", "--usage", "python3 line-count.py <file>"},
			"the description is close to line-count@format-d-fdbox"},
		{"outside the reach", []string{"add", "outside", "--from", filepath.Join(h.home, "x.py"), "--summary", "x", "--usage", "u"},
			"lies outside the checkout, AGENTIC_TOOLS_SCRATCH and the taken copies"},
		{"inside .git", []string{"add", "in-git", "--from", h.file(".git/hook.sh", "x"), "--summary", "x", "--usage", "u"},
			"lies outside the checkout"},
		{"inside a submodule's .git", []string{"add", "in-git", "--from", h.file("vendor/lib/.git/hook.sh", "x"), "--summary", "x",
			"--usage", "u"}, "lies outside the checkout"},
		{"inside .git spelled in capitals", []string{"add", "in-git", "--from", filepath.Join(h.repo, ".GIT", "HEAD"), "--summary", "x",
			"--usage", "u"}, "lies outside the checkout"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h.refused(tc.says, tc.args...)
		})
	}

	t.Run("--distinct lets a near duplicate land", func(t *testing.T) {
		h.must("add", "lines", "--from", h.file("f.py", "f"), "--summary", "Count the lines of a file quickly",
			"--usage", "python3 line-count.py <file>", "--distinct", "counts blank lines too")

		if !strings.Contains(h.read("lines/history.jsonl"), "distinct: counts blank lines too") {
			t.Error("the history should keep how the tool differs")
		}
	})
}

func TestAddRefusesWhatAReaderMustNotFollow(t *testing.T) {
	h := newHarness(t)
	target := h.file("real.py", "print(1)\n")

	link := filepath.Join(h.repo, "link.py")
	if os.Symlink(target, link) != nil {
		t.Fatal("the link should be created")
	}

	hard := filepath.Join(h.repo, "hard.py")
	if os.Link(target, hard) != nil {
		t.Fatal("the hard link should be created")
	}

	h.file("folder/ok.py", "x")
	h.file("linked/ok.py", "x")

	if os.Link(target, filepath.Join(h.repo, "linked", "hard.py")) != nil {
		t.Fatal("the hard link inside a folder should be created")
	}

	if os.Symlink(target, filepath.Join(h.repo, "folder", "inner.py")) != nil {
		t.Fatal("the inner link should be created")
	}

	big := h.file("big.py", strings.Repeat("x", 1<<20+1))

	for name, from := range map[string]string{
		"a link": link, "a hard link": hard, "a folder holding a link": filepath.Join(h.repo, "folder"), "an oversized file": big,
		"a folder holding a hard link": filepath.Join(h.repo, "linked"),
	} {
		t.Run(name, func(t *testing.T) {
			got := h.run("", "add", "probe", "--from", from, "--summary", "Probe a reader", "--usage", "u")
			if got.code != 1 {
				t.Errorf("add from %s should be refused, got %d %q", name, got.code, got.out)
			}
		})
	}
}

func TestUpdate(t *testing.T) {
	h := newHarness(t)
	from := h.file("scripts/wc.py", "import sys\n# --lines counts lines\n")
	h.must("add", "word-count", "--from", from, "--summary", "Count the words of standard input",
		"--usage", "python3 wc.py --lines < file", "--inputs", "text on stdin")

	h.refused("nothing changes", "update", "word-count", "--why", "nothing")

	h.must("update", "word-count", "--summary", "Count words of standard input fast", "--why", "faster wording")

	if !strings.Contains(h.read("word-count/v2/tool.json"), `"inputs": "text on stdin"`) || h.read("word-count/v2/wc.py") == "" {
		t.Error("fields and files not given should carry over")
	}

	renamed := h.file("scripts/new/count.py", "import sys\n")
	h.refused("the usage names wc.py, which the new version no longer holds: give --usage",
		"update", "word-count", "--from", renamed, "--why", "renamed")

	h.refused("the usage names --lines", "update", "word-count", "--from", h.file("scripts/wc2/wc.py", "import sys\n"),
		"--why", "dropped the option")

	h.must("update", "word-count", "--from", renamed, "--usage", "python3 count.py < file", "--why", "renamed")

	if strings.TrimSpace(h.read("word-count/current")) != "3" {
		t.Error("each update should make its version current")
	}
}

func TestUpdateForksAForeignTool(t *testing.T) {
	h := newHarness(t)
	other := h.elsewhere(otherRepo)
	other.tool("line-count", "Count the lines of a file quickly")

	origin := other.read("line-count/current")

	out := h.must("update", "line-count", "--summary", "Count the lines of a file, blank ones too", "--why", "blank lines")
	if !strings.Contains(out, "forked line-count from "+otherRepo+" into this project; updated it, now current") {
		t.Errorf("an update of a foreign tool should fork it, got %q", out)
	}

	if !strings.Contains(h.read("line-count/history.jsonl"), "forked from "+otherRepo+" line-count v1") ||
		!strings.Contains(h.read("line-count/v1/tool.json"), `"forked_from"`) {
		t.Error("the fork's v1 should be the origin's current version, saying where it came from")
	}

	if strings.TrimSpace(h.read("line-count/current")) != "2" {
		t.Error("the update should land as v2")
	}

	if other.read("line-count/current") != origin || fmt.Sprint(len(other.indexed())) != "1" {
		t.Error("the origin should stay untouched")
	}
}
