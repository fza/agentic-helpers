package uninstall_test

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/export"
	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/memory"
	"github.com/fza/agentic-helpers/source/hooks/internal/migration"
	"github.com/fza/agentic-helpers/source/hooks/internal/sessionrecord"
	"github.com/fza/agentic-helpers/source/hooks/internal/uninstall"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

// machine is a temporary home with one store a seat claim and a filing wrote,
// the plugin's data folder beside another plugin's, the old layout's writers
// folder, and git's global excludes file.
type machine struct {
	t          *testing.T
	root       string
	userHome   string
	roots      home.Roots
	clock      *test.Clock
	git        *test.Git
	claude     *test.Claude
	repo       string
	excludes   string
	pluginData string
	otherData  string
	oldTree    string
	variables  map[string]string
	// processes is the table every session record is read against, and
	// sessionID the calling session's id where one calls.
	processes *test.Processes
	sessionID string
}

func newMachine(t *testing.T) *machine {
	t.Helper()

	root := t.TempDir()
	m := &machine{
		t: t, root: root, userHome: filepath.Join(root, "home"), roots: test.Roots(root),
		clock:     test.NewClock(time.Date(2026, 11, 7, 14, 2, 11, 0, time.UTC)),
		claude:    &test.Claude{},
		repo:      filepath.Join(root, "app"),
		variables: map[string]string{},
		processes: test.NewProcesses(),
	}

	m.excludes = filepath.Join(m.userHome, ".config", "git", "ignore")
	test.WriteFile(t, m.excludes, "*.swp\n")
	m.git = &test.Git{Top: m.repo, Commit: "15af1a8c", Branch: "main", ExcludesFile: m.excludes}

	test.WriteFile(t, filepath.Join(m.repo, ".sdd", "config.yaml"), "repo_id: github.com/acme/app\n")
	test.WriteFile(t, filepath.Join(m.repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	test.WriteFile(t, filepath.Join(m.repo, ".tmp", "seats", "main", "draft.md"), "draft\n")

	m.memory("", "claim", "main")
	m.memory("Kept until the export.", "new", "--title", "Uninstall keeps every entry first", "--topic", "release")

	m.pluginData = filepath.Join(m.userHome, ".claude", "plugins", "data", "agentic-helpers-agentic-helpers")
	test.WriteFile(t, filepath.Join(m.pluginData, "2.28.0", "agentic-helpers"), "binary\n")
	m.otherData = filepath.Join(m.userHome, ".claude", "plugins", "data", "context7-context7-marketplace")
	test.WriteFile(t, filepath.Join(m.otherData, "keep"), "theirs\n")
	m.oldTree = filepath.Join(m.userHome, ".claude", "agentic-helpers")
	test.WriteFile(t, filepath.Join(m.oldTree, "writers.json"), "{}\n")
	test.WriteFile(t, filepath.Join(m.roots.Cache, "stores", "x", "vectors.seg"), "cache\n")
	m.clock.Hold()

	return m
}

func (m *machine) memory(body string, args ...string) {
	m.t.Helper()

	var captured test.Captured

	env := memory.Env{
		Base: m.repo, Home: m.userHome, Roots: m.roots, CacheRoot: filepath.Join(m.root, "cache"), OwnerName: "claude",
		SessionID: test.Session, Getenv: func(string) string { return "" }, Now: m.clock.Now,
		Tmp: filepath.Join(m.root, "tmp"), Random: test.Random(), Processes: test.NewProcesses(), Checkout: test.GitDirOf{},
		Git: m.git, Embedders: (&test.Embedder{}).Factory(false), Version: "test",
		Graph: &test.Graph{Err: errors.New("no sdd here")},
	}

	code := memory.Main(context.Background(), args, env, captured.Streams(body))
	if code != 0 {
		m.t.Fatalf("agentic-memory %v should succeed, exit %d: %s%s", args, code, captured.Out.String(), captured.Err.String())
	}
}

func (m *machine) env() uninstall.Env {
	migrating := migration.Env{UserHome: m.userHome, Roots: m.roots}
	env := uninstall.Env{
		Export: export.Env{
			Roots: m.roots, Migration: migrating, Producer: "agentic-helpers test", Now: m.clock.Now,
		},
		Sessions: sessionrecord.Env{
			Dir: sessionrecord.Dir(m.roots.Cache), Getenv: func(string) string { return "" }, Processes: m.processes,
			Checkout: test.CommonDir{Dir: filepath.Join(m.repo, ".git")}, OwnerName: "claude", Now: m.clock.Now,
		},
		SessionID: m.sessionID,
		UserHome:  m.userHome, Getenv: func(key string) string { return m.variables[key] },
		ConfigDir: filepath.Join(m.userHome, ".claude"), OldTrees: migrating.OldTrees(), Git: m.git, Now: m.clock.Now,
	}

	if m.claude != nil {
		env.Claude = m.claude
	}

	return env
}

func (m *machine) run(args ...string) (int, string, string) {
	m.t.Helper()

	var captured test.Captured

	code := uninstall.Main(context.Background(), args, m.env(), captured.Streams(""))

	return code, captured.Out.String(), captured.Err.String()
}

func (m *machine) defaultExport() string {
	return filepath.Join(m.userHome, "agentic-helpers-export-"+m.clock.Now().UTC().Format(home.BackupStamp)+".tar.gz")
}

func gone(t *testing.T, path string) bool {
	t.Helper()

	_, err := os.Lstat(path)

	return errors.Is(err, os.ErrNotExist)
}

// shared is every trace the uninstall must leave: the excludes file, the
// checkout's scratch folder and its local settings, another plugin's data.
func (m *machine) shared() map[string]string {
	state := test.TreeState(m.t, m.repo)
	maps.Copy(state, test.TreeState(m.t, m.otherData))
	maps.Copy(state, test.TreeState(m.t, filepath.Dir(m.excludes)))

	return state
}

func TestUninstallExportsThenRemovesOnlyItsOwnFolders(t *testing.T) {
	m := newMachine(t)

	excludes := test.ReadFile(t, m.excludes)
	if !strings.Contains(excludes, ".tmp/") || !strings.Contains(excludes, "**/.claude/settings.local.json") {
		t.Fatalf("the claim should have added its lines to the excludes file: %q", excludes)
	}

	before := m.shared()

	code, out, errOut := m.run()
	if code != 0 {
		t.Fatalf("the uninstall should succeed: %s%s", out, errOut)
	}

	manifest, err := export.Verify(m.defaultExport())
	if err != nil || len(manifest.Stores) != 1 || manifest.Stores[0].Counts["entries"] != 1 {
		t.Fatalf("the export should land at the default path and read back whole: %+v %v", manifest, err)
	}

	for _, removed := range []string{m.roots.Data, m.roots.Cache, m.pluginData, m.oldTree} {
		if !gone(t, removed) {
			t.Errorf("%s should be removed", removed)
		}
	}

	if !maps.Equal(before, m.shared()) {
		t.Errorf("shared traces and another plugin's data should stay untouched")
	}

	for _, listed := range []string{
		"grep -vxF '.tmp/' '" + m.excludes + "' > '" + m.excludes + ".new'; [ $? -le 1 ] && mv '" + m.excludes + ".new' '" +
			m.excludes + "'",
		"grep -vxF '**/.claude/settings.local.json' '" + m.excludes + "' > '" + m.excludes + ".new';",
		"rm -rf '" + filepath.Join(m.repo, ".tmp") + "'",
		`remove the key "autoMemoryEnabled" from ` + filepath.Join(m.repo, ".claude", "settings.local.json"),
	} {
		if !strings.Contains(out, listed) {
			t.Errorf("each shared trace should be listed with the command removing it: %q missing from %s", listed, out)
		}
	}

	want := [][]string{
		{"claude", "plugin", "uninstall", "agentic-helpers@agentic-helpers"},
		{"claude", "plugin", "marketplace", "remove", "agentic-helpers"},
	}
	if !slices.EqualFunc(m.claude.Calls(), want, slices.Equal) {
		t.Errorf("claude should be asked to drop the plugin, then its marketplace: %v", m.claude.Calls())
	}

	if !strings.Contains(out, "restart this Claude Code session") {
		t.Errorf("the uninstall should end saying the calling session needs a restart: %s", out)
	}

	if strings.Contains(out, ".bak") || strings.Contains(out, "jq ") {
		t.Errorf("no hand-removal command should leave a backup file or need jq: %s", out)
	}
}

func TestUninstallDropsThePluginBeforeRemovingAnyFolder(t *testing.T) {
	m := newMachine(t)

	var standing []bool

	m.claude.OnRun = func([]string) {
		standing = append(standing, !gone(t, m.roots.Data) && !gone(t, m.roots.Cache) && !gone(t, m.pluginData) &&
			!gone(t, m.oldTree))
	}

	code, out, errOut := m.run()
	if code != 0 {
		t.Fatalf("the uninstall should succeed: %s%s", out, errOut)
	}

	if !slices.Equal(standing, []bool{true, true}) || !gone(t, m.roots.Data) {
		t.Errorf("both claude commands should run while every folder still stands, the folders going after: %v", standing)
	}

	if strings.Index(out, "ran claude plugin marketplace remove") > strings.Index(out, "removed ") {
		t.Errorf("the output should report the plugin dropped before any removal: %s", out)
	}
}

// record writes the SessionStart record of session as its Claude Code
// process, the one processes runs the hook under.
func (m *machine) record(processes *test.Processes, session string) {
	m.t.Helper()

	payload := `{"session_id":"` + session + `","hook_event_name":"SessionStart","cwd":"` + m.repo + `"}`
	env := m.env().Sessions
	env.Processes = processes

	var captured test.Captured

	code := sessionrecord.Main(context.Background(), []string{"record"}, env, captured.Streams(payload))
	if code != 0 || captured.Err.Len() != 0 {
		m.t.Fatalf("recording the session should succeed: %s", captured.Err.String())
	}
}

func TestUninstallRefusesWhileAnotherSessionRuns(t *testing.T) {
	m := newMachine(t)
	m.record(m.processes.Other(), test.OtherSession)
	before := test.TreeState(t, m.root)

	code, out, errOut := m.run()
	if code != 1 || !strings.Contains(errOut, "other Claude Code sessions run") || !strings.Contains(errOut, test.OtherSession) ||
		!strings.Contains(errOut, "--ignore-sessions") {
		t.Errorf("a live other session should refuse the uninstall, named, with the way past it: %d %s%s", code, out, errOut)
	}

	if !maps.Equal(before, test.TreeState(t, m.root)) || len(m.claude.Calls()) != 0 {
		t.Errorf("a refused uninstall should change nothing and run no claude command")
	}

	code, out, errOut = m.run("--ignore-sessions")
	if code != 0 || !gone(t, m.roots.Data) {
		t.Errorf("--ignore-sessions should pass over the record: %d %s%s", code, out, errOut)
	}
}

func TestUninstallCountsNoSessionThatIsNotAnotherLiveOne(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(m *machine)
	}{
		{"its own session by its Claude Code process", func(m *machine) {
			m.record(m.processes, test.Session)
		}},
		{"its own session by the session id", func(m *machine) {
			m.record(m.processes.Other(), test.OtherSession)
			m.sessionID = test.OtherSession
		}},
		{"a session whose process is gone", func(m *machine) {
			m.record(m.processes.Other(), test.OtherSession)
			m.processes.Kill(test.OtherOwnerPID)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine(t)
			tc.setup(m)

			code, out, errOut := m.run()
			if code != 0 || !gone(t, m.roots.Data) {
				t.Errorf("the uninstall should go ahead: %d %s%s", code, out, errOut)
			}
		})
	}
}

func TestUninstallRemovesNothingWhenTheExportFails(t *testing.T) {
	m := newMachine(t)
	stores, err := m.roots.StoreDirs()
	if err != nil || len(stores) != 1 {
		t.Fatalf("the machine should hold one store: %v", err)
	}

	test.WriteFile(t, filepath.Join(stores[0].Dir, home.Memory, "format.json"), `{"format": 99}`)

	data, cache := test.TreeState(t, m.roots.Data), test.TreeState(t, m.roots.Cache)
	plugin, old := test.TreeState(t, m.pluginData), test.TreeState(t, m.oldTree)

	code, out, errOut := m.run()
	if code != 1 || !strings.Contains(errOut, "export failed") || !strings.Contains(errOut, "nothing removed") {
		t.Errorf("a failed export should stop the uninstall, saying nothing was removed: %d %s%s", code, out, errOut)
	}

	if !maps.Equal(data, test.TreeState(t, m.roots.Data)) || !maps.Equal(cache, test.TreeState(t, m.roots.Cache)) ||
		!maps.Equal(plugin, test.TreeState(t, m.pluginData)) || !maps.Equal(old, test.TreeState(t, m.oldTree)) {
		t.Errorf("a failed export should leave every folder as it was")
	}

	if len(m.claude.Calls()) != 0 || !gone(t, m.defaultExport()) {
		t.Errorf("a failed export should run no claude command and leave no archive")
	}
}

func TestUninstallWithDiscardDataWarnsThenDeletes(t *testing.T) {
	m := newMachine(t)

	code, out, errOut := m.run("--discard-data")
	if code != 0 {
		t.Fatalf("the uninstall should succeed: %s%s", out, errOut)
	}

	warning := strings.Index(out, "memory stores, the tool library and attachments are deleted with no export")
	removed := strings.Index(out, "removed ")

	if warning < 0 || removed < warning {
		t.Errorf("the warning should come before anything is removed: %s", out)
	}

	listed, _ := filepath.Glob(filepath.Join(m.userHome, "agentic-helpers-export-*"))
	if len(listed) != 0 || !gone(t, m.roots.Data) {
		t.Errorf("--discard-data should write no archive and remove the data root: %v", listed)
	}
}

func TestUninstallDryRunChangesNothing(t *testing.T) {
	m := newMachine(t)
	before := test.TreeState(t, m.root)

	code, out, errOut := m.run("--dry-run")
	if code != 0 || !strings.Contains(out, "would remove "+m.roots.Data) ||
		!strings.Contains(out, "would export every store to "+m.defaultExport()) ||
		!strings.Contains(out, "would run claude plugin uninstall agentic-helpers@agentic-helpers") {
		t.Errorf("a dry run should say what it would do: %d %s%s", code, out, errOut)
	}

	if !maps.Equal(before, test.TreeState(t, m.root)) || len(m.claude.Calls()) != 0 {
		t.Errorf("a dry run should change nothing and run no claude command")
	}

	if strings.Contains(out, "a real run is refused") {
		t.Errorf("a dry run with no other session should name no refusal: %s", out)
	}
}

func TestUninstallDryRunNamesTheSessionsARealRunIsRefusedOver(t *testing.T) {
	m := newMachine(t)
	m.record(m.processes.Other(), test.OtherSession)
	before := test.TreeState(t, m.root)

	code, out, errOut := m.run("--dry-run")
	if code != 0 || !strings.Contains(out, "would remove "+m.roots.Data) ||
		!strings.Contains(out, "would export every store to "+m.defaultExport()) {
		t.Errorf("a dry run should say what a real run would do while another session runs: %d %s%s", code, out, errOut)
	}

	refused := strings.Index(out, "a real run is refused while these sessions run")
	named := strings.Index(out, "session "+test.OtherSession)

	if refused < 0 || named < refused || !strings.Contains(out, "--ignore-sessions") {
		t.Errorf("a dry run should say a real run is refused, and name the session it is refused over: %s", out)
	}

	if !maps.Equal(before, test.TreeState(t, m.root)) || len(m.claude.Calls()) != 0 {
		t.Errorf("a dry run should change nothing and run no claude command")
	}
}

func TestUninstallFollowsNoLinkPlantedInTheDataRoot(t *testing.T) {
	m := newMachine(t)
	outside := test.Outside(t)

	err := os.Symlink(outside, filepath.Join(m.roots.Data, "planted"))
	if err != nil {
		t.Fatalf("planting the link: %v", err)
	}

	err = os.Symlink(filepath.Join(outside, "keep.txt"), filepath.Join(m.roots.Data, "stores", "planted.txt"))
	if err != nil {
		t.Fatalf("planting the link: %v", err)
	}

	code, out, errOut := m.run()
	if code != 0 || !gone(t, m.roots.Data) {
		t.Fatalf("the uninstall should remove the data root: %d %s%s", code, out, errOut)
	}

	if !test.Untouched(t, outside) {
		t.Errorf("a link planted in the data root should go without what it points at")
	}
}

func TestUninstallPrintsTheClaudeCommandsWhereClaudeCannotRun(t *testing.T) {
	for _, tc := range []struct {
		name   string
		claude *test.Claude
	}{
		{"no claude command", &test.Claude{Missing: true}},
		{"no runner at all", nil},
		{"claude failing", &test.Claude{Code: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine(t)
			m.claude = tc.claude

			code, out, errOut := m.run()
			if code != 0 {
				t.Fatalf("a claude command that cannot run should not fail the uninstall: %s%s", out, errOut)
			}

			for _, command := range []string{"claude plugin uninstall agentic-helpers@agentic-helpers",
				"claude plugin marketplace remove agentic-helpers"} {
				if !strings.Contains(out, command) || !strings.Contains(out, "run it yourself") {
					t.Errorf("the command should be printed for the user to run: %s", out)
				}
			}

			if !gone(t, m.roots.Data) || !gone(t, m.pluginData) {
				t.Errorf("the rest of the uninstall should be done")
			}
		})
	}
}

func TestUninstallRefusesAnExportInsideAFolderItRemoves(t *testing.T) {
	m := newMachine(t)
	before := test.TreeState(t, m.root)

	code, out, errOut := m.run("--export-to", filepath.Join(m.roots.Data, "export.tar.gz"))
	if code != 1 || !strings.Contains(errOut, "inside a folder the uninstall removes") {
		t.Errorf("an export the removal would delete should be refused: %d %s%s", code, out, errOut)
	}

	if !maps.Equal(before, test.TreeState(t, m.root)) {
		t.Errorf("a refused uninstall should change nothing")
	}
}

func TestUninstallRefusesARootHoldingTheUserHome(t *testing.T) {
	m := newMachine(t)
	m.roots.Data = m.root
	before := test.TreeState(t, m.root)

	code, out, errOut := m.run("--discard-data")
	if code != 1 || !strings.Contains(errOut, "no folder of agentic-helpers' own") {
		t.Errorf("a data root holding the user's home should be refused: %d %s%s", code, out, errOut)
	}

	if !maps.Equal(before, test.TreeState(t, m.root)) {
		t.Errorf("a refused uninstall should change nothing")
	}
}

func TestUninstallTakesThePluginDataClaudeCodeNames(t *testing.T) {
	m := newMachine(t)
	named := filepath.Join(m.root, "elsewhere", "agentic-helpers-inline")
	test.WriteFile(t, filepath.Join(named, "2.28.0", "agentic-helpers"), "binary\n")
	m.variables["CLAUDE_PLUGIN_DATA"] = named

	code, out, errOut := m.run("--discard-data")
	if code != 0 || !gone(t, named) {
		t.Fatalf("the plugin data folder Claude Code names should be removed: %s%s", out, errOut)
	}

	if gone(t, m.pluginData) {
		t.Errorf("only the named plugin data folder should go where Claude Code names one")
	}
}

func TestUninstallLeavesAnotherPluginsDataClaudeCodeNames(t *testing.T) {
	m := newMachine(t)
	m.variables["CLAUDE_PLUGIN_DATA"] = m.otherData

	code, out, errOut := m.run("--discard-data")
	if code != 0 || !gone(t, m.pluginData) {
		t.Fatalf("agentic-helpers' own plugin data should go: %s%s", out, errOut)
	}

	if gone(t, m.otherData) {
		t.Errorf("a data folder of another plugin should stay, whatever names it")
	}
}

func TestUninstallRefusesAMisspeltCall(t *testing.T) {
	for _, args := range [][]string{{"--discard"}, {"--export-to"}, {"--discard-data", "--export-to", "x.tar.gz"}} {
		m := newMachine(t)
		before := test.TreeState(t, m.root)

		code, _, errOut := m.run(args...)
		if code != 1 || !strings.HasPrefix(errOut, "usage: agentic-helpers uninstall") {
			t.Errorf("%v should print the usage and exit 1: %d %s", args, code, errOut)
		}

		if !maps.Equal(before, test.TreeState(t, m.root)) {
			t.Errorf("a misspelt call should change nothing")
		}
	}
}
