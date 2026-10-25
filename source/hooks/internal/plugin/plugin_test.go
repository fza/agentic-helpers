// Package plugin_test drives the plugin's launcher, hooks/run, and the shims in
// bin/, as Claude Code runs them: a fresh process with the plugin's variables
// set and curl faked, never the network or the developer's home.
package plugin_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

const (
	repository = "../../../.."
	pluginRoot = repository + "/plugin"
)

func TestTheOverrideRunsItsOwnDirectory(t *testing.T) {
	plugin := test.NewPlugin(t, pluginRoot, "1.4.0")
	built := filepath.Join(t.TempDir(), "bin")
	plugin.Binaries(t, built, "agentic-scratch")
	plugin.Installed(t, "1.4.0", "agentic-scratch")

	got := plugin.Run(t, "hooks/run", []string{"AGENTIC_HELPERS_BIN_DIR=" + built}, "--session-start", "agentic-scratch", "start")
	if got.Code != 0 || !strings.Contains(got.Stdout, "ran "+filepath.Join(built, "agentic-scratch")+" start") {
		t.Errorf("the override directory's binary should run with the hook's arguments, got: %+v", got)
	}

	if plugin.CurlCalls(t) != nil {
		t.Error("the override should never download")
	}
}

func TestThePluginsVersionRunsWhenPresent(t *testing.T) {
	plugin := test.NewPlugin(t, pluginRoot, "1.4.0")
	plugin.Installed(t, "1.3.0", "agentic-memory")
	plugin.Installed(t, "1.4.0", "agentic-memory")

	got := plugin.Run(t, "hooks/run", nil, "--session-start", "agentic-memory", "session-log", "1")
	if got.Code != 0 || !strings.Contains(got.Stdout, filepath.Join("1.4.0", "agentic-memory")+" session-log 1") {
		t.Errorf("the version the plugin names should run, got: %+v", got)
	}

	if plugin.CurlCalls(t) != nil {
		t.Error("a version already present should never download")
	}
}

func TestSessionStartFetchesTheRelease(t *testing.T) {
	plugin := test.NewPlugin(t, pluginRoot, "1.4.0")
	plugin.Release(t, "1.4.0", "", "agentic-scratch", "agentic-grounding-gate")

	got := plugin.Run(t, "hooks/run", nil, "--session-start", "agentic-scratch", "start")
	if got.Code != 0 || !strings.Contains(got.Stdout, filepath.Join(plugin.Data, "1.4.0", "agentic-scratch")+" start") {
		t.Fatalf("the fetched binary should run, got: %+v", got)
	}

	if len(plugin.CurlCalls(t)) != 2 {
		t.Errorf("the checksums and the archive should be fetched once each, got: %v", plugin.CurlCalls(t))
	}

	if !test.Exists(t, filepath.Join(plugin.Data, "1.4.0", "agentic-grounding-gate")) {
		t.Error("every binary of the release should be unpacked")
	}

	if test.Exists(t, filepath.Join(plugin.Data, ".fetch-1.4.0")) {
		t.Error("the fetch should leave no lock behind")
	}
}

func TestACommandFetchesAMissingRelease(t *testing.T) {
	plugin := test.NewPlugin(t, pluginRoot, "1.4.0")
	plugin.Release(t, "1.4.0", "", "agentic-memory")
	plugin.Installed(t, "1.3.0", "agentic-capture")

	got := plugin.Run(t, "hooks/run", nil, "--command", "agentic-memory", "mcp")
	if got.Code != 0 || !strings.Contains(got.Stdout, filepath.Join(plugin.Data, "1.4.0", "agentic-memory")+" mcp") {
		t.Fatalf("a command should fetch the release it is missing and run its binary, got: %+v", got)
	}

	if strings.Contains(got.Stdout, "agentic-helpers:") {
		t.Errorf("a command's stdout belongs to the binary and carries no notice, got: %+v", got)
	}
}

func TestTheGateSeesSddsMCPTools(t *testing.T) {
	content, err := os.ReadFile(filepath.Join(pluginRoot, "hooks", "hooks.json"))
	if err != nil {
		t.Fatalf("reading hooks.json: %v", err)
	}

	var wiring struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}

	err = json.Unmarshal(content, &wiring)
	if err != nil {
		t.Fatalf("parsing hooks.json: %v", err)
	}

	matched := func(event string, mode string) []string {
		var tools []string

		for _, group := range wiring.Hooks[event] {
			for _, hook := range group.Hooks {
				if strings.HasSuffix(hook.Command, "agentic-grounding-gate "+mode) {
					tools = append(tools, strings.Split(group.Matcher, "|")...)
				}
			}
		}

		return tools
	}

	for _, tool := range []string{"mcp__sdd__show", "mcp__sdd__start_procedure"} {
		if !slices.Contains(matched("PreToolUse", "gate"), tool) {
			t.Errorf("the gate should judge %s before it runs", tool)
		}
	}

	for _, tool := range []string{"mcp__sdd__search", "mcp__sdd__view"} {
		if !slices.Contains(matched("PostToolUse", "record"), tool) {
			t.Errorf("the gate should record %s as a read", tool)
		}
	}
}

func TestTheMCPServerRunsThroughTheCommandLauncher(t *testing.T) {
	content, err := os.ReadFile(filepath.Join(pluginRoot, ".mcp.json"))
	if err != nil {
		t.Fatalf("reading .mcp.json: %v", err)
	}

	var servers struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}

	err = json.Unmarshal(content, &servers)
	if err != nil {
		t.Fatalf("parsing .mcp.json: %v", err)
	}

	for name, server := range servers.MCPServers {
		if server.Command != "${CLAUDE_PLUGIN_ROOT}/hooks/run" || len(server.Args) < 2 || server.Args[0] != "--command" {
			t.Errorf("the %s server should start through the launcher's command mode, which fetches a missing release, got: %+v",
				name, server)

			continue
		}

		if !test.Exists(t, filepath.Join(repository, "source", "hooks", "cmd", server.Args[1])) {
			t.Errorf("the %s server should name a binary the release builds, got: %s", name, server.Args[1])
		}
	}
}

func TestParallelSessionStartsFetchOnce(t *testing.T) {
	plugin := test.NewPlugin(t, pluginRoot, "1.4.0")
	plugin.Release(t, "1.4.0", "", "agentic-scratch")

	var group sync.WaitGroup

	results := make([]test.Ran, 4)
	for index := range results {
		group.Go(func() {
			results[index] = plugin.Run(t, "hooks/run", []string{"FAKE_CURL_DELAY=1"}, "--session-start", "agentic-scratch", "start")
		})
	}

	group.Wait()

	for _, got := range results {
		if got.Code != 0 || !strings.Contains(got.Stdout, filepath.Join("1.4.0", "agentic-scratch")) {
			t.Errorf("every parallel hook should run the fetched binary, got: %+v", got)
		}
	}

	if len(plugin.CurlCalls(t)) != 2 {
		t.Errorf("only one hook should fetch, got: %v", plugin.CurlCalls(t))
	}
}

func TestAStaleLockIsTakenOver(t *testing.T) {
	plugin := test.NewPlugin(t, pluginRoot, "1.4.0")
	plugin.Release(t, "1.4.0", "", "agentic-scratch")

	lock := filepath.Join(plugin.Data, ".fetch-1.4.0")
	test.WriteFile(t, filepath.Join(lock, "checksums.txt"), "left by a fetch that died\n")
	test.Age(t, lock, 10*time.Minute)

	got := plugin.Run(t, "hooks/run", nil, "--session-start", "agentic-scratch", "start")
	if got.Code != 0 || !strings.Contains(got.Stdout, filepath.Join("1.4.0", "agentic-scratch")) {
		t.Errorf("a lock left by a dead fetch should not hold the release back, got: %+v", got)
	}
}

func TestAFetchSignalledMidDownloadLeavesNothingBehind(t *testing.T) {
	for _, signal := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP} {
		t.Run(signal.String(), func(t *testing.T) {
			plugin := test.NewPlugin(t, pluginRoot, "1.4.0")
			plugin.Release(t, "1.4.0", "", "agentic-memory")

			lock := filepath.Join(plugin.Data, ".fetch-1.4.0")
			launcher := plugin.Start(t, "hooks/run", []string{"FAKE_CURL_DELAY=30"}, "--command", "agentic-memory", "mcp")

			deadline := time.Now().Add(10 * time.Second)
			for len(plugin.CurlCalls(t)) == 0 && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}

			err := launcher.Process.Signal(signal)
			if err != nil {
				t.Fatalf("signalling the launcher: %v", err)
			}

			waited := make(chan struct{})
			go func() {
				_ = launcher.Wait()
				close(waited)
			}()

			select {
			case <-waited:
			case <-time.After(10 * time.Second):
				_ = launcher.Process.Kill()
				t.Fatal("a signalled fetch should end without waiting for its download")
			}

			if test.Exists(t, lock) {
				t.Error("a fetch signalled mid-download should take its lock with it")
			}

			if downloadRunning(t, plugin.CurlLog+".pids") {
				t.Error("a fetch signalled mid-download should stop its download too")
			}
		})
	}
}

// downloadRunning reports whether a fake curl the log names still runs, given
// a moment to receive the signal it was sent.
func downloadRunning(t *testing.T, pids string) bool {
	t.Helper()

	content, err := os.ReadFile(pids)
	if err != nil {
		t.Fatalf("reading the fake curl's pids: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		running := false

		for _, field := range strings.Fields(string(content)) {
			pid, _ := strconv.Atoi(field)
			if pid > 0 && syscall.Kill(pid, 0) == nil {
				running = true
			}
		}

		if !running || time.Now().After(deadline) {
			return running
		}

		time.Sleep(50 * time.Millisecond)
	}
}

func TestALockWhoseOwnerIsGoneIsTakenOverAtOnce(t *testing.T) {
	plugin := test.NewPlugin(t, pluginRoot, "1.4.0")
	plugin.Release(t, "1.4.0", "", "agentic-scratch")

	gone := exec.Command("true")

	err := gone.Run()
	if err != nil {
		t.Fatalf("running a process to outlive: %v", err)
	}

	lock := filepath.Join(plugin.Data, ".fetch-1.4.0")
	test.WriteFile(t, filepath.Join(lock, "checksums.txt"), "left by a fetch that was killed\n")
	test.WriteFile(t, filepath.Join(lock, "owner"), strconv.Itoa(gone.Process.Pid)+"\n")

	started := time.Now()

	got := plugin.Run(t, "hooks/run", nil, "--session-start", "agentic-scratch", "start")
	if got.Code != 0 || !strings.Contains(got.Stdout, filepath.Join("1.4.0", "agentic-scratch")) {
		t.Errorf("a lock whose owner is gone should not hold the release back, got: %+v", got)
	}

	if time.Since(started) > 20*time.Second {
		t.Errorf("a dead owner's lock should be taken over at once, not waited out, took %v", time.Since(started))
	}

	if test.Exists(t, lock) || test.Exists(t, lock+".takeover") {
		t.Error("the takeover should leave no lock behind")
	}
}

func TestAFetchKilledUncaughtIsTakenOver(t *testing.T) {
	plugin := test.NewPlugin(t, pluginRoot, "1.4.0")
	plugin.Release(t, "1.4.0", "", "agentic-scratch")

	lock := filepath.Join(plugin.Data, ".fetch-1.4.0")
	first := plugin.Start(t, "hooks/run", []string{"FAKE_CURL_DELAY=30"}, "--session-start", "agentic-scratch", "start")

	deadline := time.Now().Add(10 * time.Second)
	for !test.Exists(t, filepath.Join(lock, "owner")) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}

	err := first.Process.Kill()
	if err != nil {
		t.Fatalf("killing the first fetch: %v", err)
	}

	_ = first.Wait()

	started := time.Now()

	got := plugin.Run(t, "hooks/run", nil, "--session-start", "agentic-scratch", "start")
	if got.Code != 0 || !strings.Contains(got.Stdout, filepath.Join("1.4.0", "agentic-scratch")) {
		t.Errorf("a fetch killed beyond any trap should not hold the release back, got: %+v", got)
	}

	if time.Since(started) > 20*time.Second {
		t.Errorf("the lock of a fetch killed beyond any trap should be taken over at once, took %v", time.Since(started))
	}
}

func TestALockOfALiveFetchIsWaitedOn(t *testing.T) {
	cases := []struct {
		name  string
		owner bool
		age   time.Duration
	}{
		{name: "an owner still running, the lock old", owner: true, age: 10 * time.Minute},
		{name: "no owner file, the lock young"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plugin := test.NewPlugin(t, pluginRoot, "1.4.0")
			plugin.Release(t, "1.4.0", "", "agentic-scratch")

			lock := filepath.Join(plugin.Data, ".fetch-1.4.0")
			test.WriteFile(t, filepath.Join(lock, "checksums.txt"), "a fetch in progress\n")

			if tc.owner {
				test.WriteFile(t, filepath.Join(lock, "owner"), strconv.Itoa(os.Getpid())+"\n")
			}

			if tc.age > 0 {
				test.Age(t, lock, tc.age)
			}

			waiter := plugin.Start(t, "hooks/run", nil, "--session-start", "agentic-scratch", "start")
			time.Sleep(2 * time.Second)

			if len(plugin.CurlCalls(t)) != 0 {
				t.Error("a lock held by a live fetch should be waited on, never fetched past")
			}

			plugin.Installed(t, "1.4.0", "agentic-scratch")

			err := os.RemoveAll(lock)
			if err != nil {
				t.Fatalf("finishing the other fetch: %v", err)
			}

			_ = waiter.Wait()

			if len(plugin.CurlCalls(t)) != 0 {
				t.Error("the waiter should run what the other fetch unpacked")
			}
		})
	}
}

func TestAMismatchingChecksumUnpacksNothing(t *testing.T) {
	plugin := test.NewPlugin(t, pluginRoot, "1.4.0")
	plugin.Release(t, "1.4.0", strings.Repeat("0", 64), "agentic-scratch")

	got := plugin.Run(t, "hooks/run", nil, "--session-start", "agentic-scratch", "start")
	if got.Code != 0 || strings.Contains(got.Stdout, "ran ") {
		t.Errorf("an archive failing its checksum should never run, got: %+v", got)
	}

	if !strings.Contains(got.Stdout, "does not match the release's checksums.txt") {
		t.Errorf("the notice should say the checksum failed, got: %+v", got)
	}

	entries, err := os.ReadDir(plugin.Data)
	if err != nil {
		t.Fatalf("reading the data directory: %v", err)
	}

	if len(entries) != 0 {
		t.Errorf("nothing should be left in the data directory, got: %v", entries)
	}
}

func TestAMissingVersionFallsBackToTheNewestPresent(t *testing.T) {
	cases := []struct {
		name  string
		extra []string
		args  []string
	}{
		{name: "a prompt never downloads", args: []string{"agentic-grounding-gate", "turn"}},
		{name: "a session start that cannot download", extra: []string{"FAKE_RELEASES=/nonexistent"},
			args: []string{"--session-start", "agentic-grounding-gate", "start"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plugin := test.NewPlugin(t, pluginRoot, "1.10.1")
			plugin.Installed(t, "1.9.0", "agentic-grounding-gate")
			plugin.Installed(t, "1.10.0", "agentic-grounding-gate")
			plugin.Installed(t, "1.2.0", "agentic-grounding-gate")

			got := plugin.Run(t, "hooks/run", tc.extra, tc.args...)
			if got.Code != 0 || !strings.Contains(got.Stdout, filepath.Join("1.10.0", "agentic-grounding-gate")) {
				t.Errorf("the newest version present should run, by version order, got: %+v", got)
			}

			if strings.Contains(got.Stdout, "agentic-helpers:") {
				t.Errorf("a version standing in should raise no notice, got: %+v", got)
			}
		})
	}

	t.Run("a prompt asks for no download", func(t *testing.T) {
		plugin := test.NewPlugin(t, pluginRoot, "1.10.1")
		plugin.Release(t, "1.10.1", "", "agentic-grounding-gate")
		plugin.Installed(t, "1.9.0", "agentic-grounding-gate")

		plugin.Run(t, "hooks/run", nil, "agentic-grounding-gate", "turn")

		if plugin.CurlCalls(t) != nil {
			t.Error("only a session start should download")
		}
	})
}

func TestADataDirectoryItCannotWriteStillRunsWhatIsThere(t *testing.T) {
	plugin := test.NewPlugin(t, pluginRoot, "1.4.0")
	plugin.Installed(t, "1.3.0", "agentic-scratch")

	err := os.Chmod(plugin.Data, 0o555)
	if err != nil {
		t.Fatalf("making the data directory read-only: %v", err)
	}

	t.Cleanup(func() { _ = os.Chmod(plugin.Data, 0o755) })

	got := plugin.Run(t, "hooks/run", nil, "--session-start", "agentic-scratch", "start")
	if got.Code != 0 || !strings.Contains(got.Stdout, filepath.Join("1.3.0", "agentic-scratch")) {
		t.Errorf("the version present should run where nothing can be written, got: %+v", got)
	}

	if plugin.CurlCalls(t) != nil {
		t.Error("nothing should be downloaded where it cannot be kept")
	}

	t.Run("with nothing there, the notice says why", func(t *testing.T) {
		plugin := test.NewPlugin(t, pluginRoot, "1.4.0")
		test.WriteFile(t, filepath.Join(plugin.Data, "README"), "kept by Claude Code\n")

		err := os.Chmod(plugin.Data, 0o555)
		if err != nil {
			t.Fatalf("making the data directory read-only: %v", err)
		}

		t.Cleanup(func() { _ = os.Chmod(plugin.Data, 0o755) })

		got := plugin.Run(t, "hooks/run", nil, "--session-start", "agentic-scratch", "start")
		if got.Code != 0 || !strings.Contains(got.Stdout, "cannot write "+plugin.Data) {
			t.Errorf("the notice should name the directory it cannot write, got: %+v", got)
		}
	})
}

func TestNoBinaryAtAll(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		code   int
		notice bool
	}{
		{name: "the gate refuses", args: []string{"agentic-grounding-gate", "gate"}, code: 2},
		{name: "shell-guard refuses", args: []string{"agentic-shell-guard"}, code: 2},
		{name: "a session start says why once", args: []string{"--session-start", "agentic-scratch", "start"}, notice: true},
		{name: "any other hook stays quiet", args: []string{"agentic-memory", "prompt"}},
		{name: "the gate's own record stays quiet", args: []string{"agentic-grounding-gate", "record"}},
		{name: "a command from bin/ fails loudly", args: []string{"--command", "agentic-memory", "new"}, code: 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plugin := test.NewPlugin(t, pluginRoot, "1.4.0")

			got := plugin.Run(t, "hooks/run", nil, tc.args...)
			if got.Code != tc.code {
				t.Errorf("the hook should exit as decided, got: %+v", got)
			}

			if tc.code != 0 && !strings.Contains(got.Stderr, "start a session with network") {
				t.Errorf("the refusal should name the fix, got: %+v", got)
			}

			notices := strings.Count(got.Stdout, "agentic-helpers:")
			if tc.notice && notices != 1 {
				t.Errorf("the session start should say why once, got: %+v", got)
			}

			if !tc.notice && notices != 0 {
				t.Errorf("the hook should print no notice, got: %+v", got)
			}
		})
	}
}

func TestTheShimsRunTheRealBinary(t *testing.T) {
	plugin := test.NewPlugin(t, pluginRoot, "1.4.0")
	data := filepath.Join(plugin.Config, "plugins", "data", "agentic-helpers-agentic-helpers")
	plugin.Binaries(t, filepath.Join(data, "1.4.0"), "agentic-capture", "agentic-memory")

	bashTool := []string{"CLAUDE_PLUGIN_ROOT", "CLAUDE_PLUGIN_DATA", "CLAUDE_CONFIG_DIR=" + plugin.Config}

	cases := []struct {
		shim string
		args []string
	}{
		{shim: "bin/agentic-capture", args: []string{"--help"}},
		{shim: "bin/agentic-memory", args: []string{"claim", "main"}},
	}

	for _, tc := range cases {
		t.Run(tc.shim, func(t *testing.T) {
			got := plugin.Run(t, tc.shim, bashTool, tc.args...)

			want := "ran " + filepath.Join(data, "1.4.0", filepath.Base(tc.shim)) + " " + strings.Join(tc.args, " ")
			if got.Code != 0 || !strings.Contains(got.Stdout, want) {
				t.Errorf("the shim should hand its arguments to the real binary of the plugin's version, got: %+v", got)
			}
		})
	}
}

// Claude Code loads every component it finds at a plugin's root, an MCP
// server from a .mcp.json among them, so the plugin's directory holds its own
// components and nothing else.
func TestThePluginShipsOnlyItsOwnComponents(t *testing.T) {
	entries, err := os.ReadDir(pluginRoot)
	if err != nil {
		t.Fatalf("reading the plugin directory: %v", err)
	}

	own := []string{".claude-plugin", ".mcp.json", "bin", "hooks", "skills"}

	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	if !slices.Equal(names, own) {
		t.Errorf("the plugin directory should hold its manifest, shims, hooks and skills alone, got: %v", names)
	}

	manifests, err := os.ReadDir(filepath.Join(pluginRoot, ".claude-plugin"))
	if err != nil {
		t.Fatalf("reading the plugin manifest directory: %v", err)
	}

	if len(manifests) != 1 || manifests[0].Name() != "plugin.json" {
		t.Errorf("the plugin manifest directory should hold plugin.json alone, got: %v", manifests)
	}
}

func TestTheReleaseBuildsEveryBinary(t *testing.T) {
	config, err := os.ReadFile(filepath.Join(repository, ".goreleaser.yaml"))
	if err != nil {
		t.Fatalf("reading .goreleaser.yaml: %v", err)
	}

	commands, err := os.ReadDir(filepath.Join(repository, "source", "hooks", "cmd"))
	if err != nil {
		t.Fatalf("reading cmd/: %v", err)
	}

	for _, command := range commands {
		if !strings.Contains(string(config), "main: ./cmd/"+command.Name()+"\n") ||
			!strings.Contains(string(config), "binary: "+command.Name()+"\n") {
			t.Errorf("every binary under cmd/ should be built by the release, got none for %s", command.Name())
		}
	}
}

func TestEveryHookRunsThroughTheLauncher(t *testing.T) {
	content, err := os.ReadFile(filepath.Join(pluginRoot, "hooks", "hooks.json"))
	if err != nil {
		t.Fatalf("reading hooks.json: %v", err)
	}

	var wiring struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}

	err = json.Unmarshal(content, &wiring)
	if err != nil {
		t.Fatalf("parsing hooks.json: %v", err)
	}

	launcher := `"${CLAUDE_PLUGIN_ROOT}/hooks/run" `
	seen := 0

	for event, groups := range wiring.Hooks {
		for _, group := range groups {
			for _, hook := range group.Hooks {
				seen++

				arguments, found := strings.CutPrefix(hook.Command, launcher)
				if !found || hook.Type != "command" || hook.Timeout == 0 {
					t.Errorf("every %s hook should be a command through the launcher with a timeout, got: %+v", event, hook)

					continue
				}

				fetches := strings.HasPrefix(arguments, "--session-start ")
				if fetches != (event == "SessionStart") {
					t.Errorf("only a SessionStart hook should be allowed to fetch, got: %s %s", event, hook.Command)
				}

				binary := strings.Fields(strings.TrimPrefix(arguments, "--session-start "))[0]
				if !test.Exists(t, filepath.Join(repository, "source", "hooks", "cmd", binary)) {
					t.Errorf("every hook should name a binary the release builds, got: %s", binary)
				}
			}
		}
	}

	if seen == 0 {
		t.Error("hooks.json should wire hooks")
	}
}
