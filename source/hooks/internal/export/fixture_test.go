package export_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/export"
	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/memory"
	"github.com/fza/agentic-helpers/source/hooks/internal/migration"
	"github.com/fza/agentic-helpers/source/hooks/internal/tools"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

// machine is a temporary home whose stores are written by agentic-memory
// and agentic-tools themselves, run as a session would run them.
//
// The builder lives here rather than in the module's test package, where
// shared helpers go: it runs memory.Main and tools.Main, and the test package
// cannot import either, since their in-package tests import it.
type machine struct {
	t         *testing.T
	root      string
	userHome  string
	clock     *test.Clock
	processes *test.Processes
}

func newMachine(t *testing.T) *machine {
	t.Helper()

	root := t.TempDir()

	return &machine{
		t: t, root: root, userHome: filepath.Join(root, "home"),
		clock:     test.NewClock(time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)),
		processes: test.NewProcesses(),
	}
}

func (m *machine) roots() home.Roots { return test.Roots(m.root) }

func (m *machine) env() export.Env {
	return export.Env{
		Roots: m.roots(), Migration: migration.Env{UserHome: m.userHome, Roots: m.roots()},
		Producer: "agentic-helpers test", Now: m.clock.Now,
	}
}

// checkout is a repository declaring repoID, the directory a session works in.
func (m *machine) checkout(name string, repoID string) string {
	m.t.Helper()

	repo := filepath.Join(m.root, name)
	test.WriteFile(m.t, filepath.Join(repo, ".sdd", "config.yaml"), "repo_id: "+repoID+"\n")
	test.WriteFile(m.t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	test.WriteFile(m.t, filepath.Join(repo, "internal", "gate", "gate.go"), "package gate\n")
	test.WriteFile(m.t, filepath.Join(repo, ".sdd", "graph", "2026", "10", "30-142233-d-tac-a9x.md"), "---\ntype: decision\n---\n")

	return repo
}

func (m *machine) memory(repo string, body string, args ...string) string {
	m.t.Helper()

	var captured test.Captured

	env := memory.Env{
		Base: repo, Home: m.userHome, Roots: m.roots(), CacheRoot: filepath.Join(m.root, "cache"), OwnerName: "claude",
		SessionID: test.Session, Getenv: func(string) string { return "" }, Now: m.clock.Now,
		Tmp: filepath.Join(m.root, "tmp"), Random: test.Random(), Processes: m.processes, Checkout: test.GitDirOf{},
		Git: &test.Git{Top: repo, Commit: "15af1a8c", Branch: "main"}, Embedders: (&test.Embedder{}).Factory(false),
		Version: "test", Graph: &test.Graph{Err: errors.New("no sdd here")},
	}

	code := memory.Main(context.Background(), args, env, captured.Streams(body))
	if code != 0 {
		m.t.Fatalf("agentic-memory %v should succeed, exit %d: %s%s", args, code, captured.Out.String(), captured.Err.String())
	}

	m.clock.Advance(time.Minute)

	return captured.Out.String()
}

func (m *machine) tools(repo string, args ...string) string {
	m.t.Helper()

	var captured test.Captured

	env := tools.Env{
		Base: repo, Home: m.userHome, Roots: m.roots(), OwnerName: "claude", SessionID: test.Session,
		Getenv: func(string) string { return "" }, Now: m.clock.Now, Processes: m.processes, Checkout: test.GitDirOf{},
		Git: &test.Git{Top: repo, Commit: "15af1a8c", Branch: "main"}, Embedders: (&test.Embedder{}).Factory(false),
		Version: "test", LockWait: 200 * time.Millisecond,
	}

	code := tools.Main(context.Background(), args, env, captured.Streams(""))
	if code != 0 {
		m.t.Fatalf("agentic-tools %v should succeed, exit %d: %s%s", args, code, captured.Out.String(), captured.Err.String())
	}

	m.clock.Advance(time.Minute)

	return captured.Out.String()
}

var (
	entryID = regexp.MustCompile(`m-[a-z0-9]{4,16}`)
	itemRef = regexp.MustCompile(`#([a-z0-9]{3})\b`)
)

func (m *machine) file(repo string, title string, body string, extra ...string) string {
	m.t.Helper()

	out := m.memory(repo, body, append([]string{"new", "--title", title, "--topic", "gate"}, extra...)...)

	id := entryID.FindString(out)
	if id == "" {
		m.t.Fatalf("filing should name the new entry's id: %s", out)
	}

	return id
}

// itemOf is the id of the todo's item whose text holds text, as show prints it.
func (m *machine) itemOf(repo string, todo string, text string) string {
	m.t.Helper()

	for _, line := range strings.Split(m.memory(repo, "", "show", todo, "--all-items", "--again"), "\n") {
		if strings.Contains(line, text) {
			found := itemRef.FindStringSubmatch(line)
			if found != nil {
				return found[1]
			}
		}
	}

	m.t.Fatalf("show should list the item %q of %s", text, todo)

	return ""
}

// storeDir is the folder of the store the checkout at repo works on.
func (m *machine) storeDir(repo string) string {
	m.t.Helper()

	return filepath.Dir(test.MemoryStore(m.t, m.roots(), repo, test.GitDirOf{}))
}

// populated is what the full fixture holds, for a test to check against.
type populated struct {
	app, other           string
	appStore, otherStore string
	note, todo, retired  string
	first, second, third string
	attached             string
	slice                string
	doneItem, dropped    string
}

// populate writes a store holding every kind of record the archive carries,
// and a second store left unclaimed.
func (m *machine) populate() populated {
	m.t.Helper()

	var held populated

	held.app = m.checkout("app", "github.com/acme/app")
	m.memory(held.app, "", "claim", "main")

	held.note = m.file(held.app, "Gate records reads on the Bash path", "The Bash path records reads.",
		"--about", "internal/gate/gate.go", "--link", "20261030-142233-d-tac-a9x")
	held.todo = m.file(held.app, "Port the waiter to the new clock", "Three steps.", "--kind", "todo",
		"--item", "read the timer", "--item", "swap the clock", "--item", "run the suite")
	held.doneItem = m.itemOf(held.app, held.todo, "read the timer")
	held.dropped = m.itemOf(held.app, held.todo, "swap the clock")
	m.memory(held.app, "", "ack", held.todo+"#"+held.doneItem, "--done")
	m.memory(held.app, "", "ack", held.todo+"#"+held.dropped, "--decline", "the clock stays")

	held.retired = m.file(held.app, "Cache folder moved to the home", "Moved.")
	m.memory(held.app, "", "retire", held.retired, "--because", "settled")

	held.first = m.file(held.app, "Seat names follow the registry", "First take.")
	held.second = m.file(held.app, "Seat names follow the registry ids", "Second take.")
	m.memory(held.app, "", "supersede", held.first, "--by", held.second)
	held.third = m.file(held.app, "Seat names follow registry ids always", "Third take.")
	m.memory(held.app, "", "supersede", held.second, "--by", held.third)

	m.memory(held.app, "", "link", held.note, held.retired)
	m.memory(held.app, "", "tag", held.note, "hooks")

	held.attached = filepath.Join(held.app, "notes.txt")
	test.WriteFile(m.t, held.attached, "notes kept with the entry\n")
	m.memory(held.app, "", "attach", held.note, held.attached)

	m.memory(held.app, "", "ack", "state", "--add", "gate fix half done: the MCP path is next", "--until", "none")

	held.appStore = m.storeDir(held.app)

	registry := filepath.Join(held.appStore, home.Memory, "seats")
	seatFiles, err := filepath.Glob(filepath.Join(registry, "s-*.json"))
	if err != nil || len(seatFiles) == 0 {
		m.t.Fatalf("the claim should leave a claim file: %v", err)
	}

	seatID := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(seatFiles[0]), ".json"), ".last")
	held.slice = "session " + test.Session + "\n# 2026-10-25 UTC\n## 1  09:01\nasked: commit it\ndid: Bash git commit\n"
	test.WriteFile(m.t, filepath.Join(registry, seatID+".slice"), held.slice)

	test.WriteFile(m.t, filepath.Join(held.app, "scan.sh"), "#!/bin/sh\necho scan\n")
	m.tools(held.app, "add", "log-scan", "--from", filepath.Join(held.app, "scan.sh"), "--summary", "Find error bursts in a log",
		"--usage", "log-scan <file>")
	test.WriteFile(m.t, filepath.Join(held.app, "scan.sh"), "#!/bin/sh\necho scan gzip too\n")
	m.tools(held.app, "update", "log-scan", "--from", filepath.Join(held.app, "scan.sh"), "--why", "reads gzip logs too")
	test.WriteFile(m.t, filepath.Join(held.app, "old-scan.sh"), "#!/bin/sh\necho old\n")
	m.tools(held.app, "add", "old-scan", "--from", filepath.Join(held.app, "old-scan.sh"), "--summary",
		"Scan logs the old way for bursts", "--usage", "old-scan <file>")
	m.tools(held.app, "merge", "log-scan", "old-scan", "--from", filepath.Join(held.app, "scan.sh"), "--why", "one scanner")

	held.other = m.checkout("other", "github.com/acme/other")
	m.memory(held.other, "", "claim", "main")
	m.file(held.other, "Other project keeps its own notes", "Kept apart.")

	otherStore := m.storeDir(held.other)
	held.otherStore = filepath.Join(m.roots().Data, "unclaimed", filepath.Base(otherStore))

	err = os.MkdirAll(filepath.Dir(held.otherStore), 0o700)
	if err != nil {
		m.t.Fatalf("creating the unclaimed folder: %v", err)
	}

	err = os.Rename(otherStore, held.otherStore)
	if err != nil {
		m.t.Fatalf("moving the store to unclaimed: %v", err)
	}

	return held
}

// older writes a store as an earlier release left it: memory at format 1
// (test.Format1Store) with a per-session file in its cache, which the
// format-6 step takes into the slot file, and a tool library at format 1,
// its journals JSON lines beside each tool and no format.json. It answers the
// store's folder.
func (m *machine) older() string {
	m.t.Helper()

	repo := m.checkout("old", "github.com/acme/old")
	id := test.StoreOf(m.t, m.roots(), repo, test.GitDirOf{}, nil)
	test.Format1Store(m.t, m.roots().Store(id, home.Memory))
	test.WriteFile(m.t, filepath.Join(m.roots().StoreCache(id, home.Memory), "shown", test.Session+".json"), "{}")

	library := filepath.Join(m.roots().Store(id, home.Tools), "log-scan")
	test.WriteFile(m.t, filepath.Join(library, "current"), "1\n")
	test.WriteFile(m.t, filepath.Join(library, "v1", "tool.json"),
		`{"name":"log-scan","summary":"Find error bursts in a log","usage":"log-scan <file>","files":["scan.sh"]}`)
	test.WriteFile(m.t, filepath.Join(library, "v1", "scan.sh"), "#!/bin/sh\necho scan\n")
	test.WriteFile(m.t, filepath.Join(library, "history.jsonl"),
		`{"v":1,"by":"`+test.Session+`","role":"main","at":"2026-10-20T09:00:00Z","why":"added"}`+"\n")

	return filepath.Dir(m.roots().Store(id, home.Memory))
}
