// Package plugin_test drives the plugin's launcher, hooks/run, and the shims in
// bin/, as Claude Code runs them: a fresh process with the plugin's variables
// set and curl faked, never the network or the developer's home.
package plugin_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

const repository = "../../../.."

func TestTheOverrideRunsItsOwnDirectory(t *testing.T) {
	plugin := test.NewPlugin(t, repository, "1.4.0")
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
	plugin := test.NewPlugin(t, repository, "1.4.0")
	plugin.Installed(t, "1.3.0", "agentic-carryforward")
	plugin.Installed(t, "1.4.0", "agentic-carryforward")

	got := plugin.Run(t, "hooks/run", nil, "--session-start", "agentic-carryforward", "session-log", "1")
	if got.Code != 0 || !strings.Contains(got.Stdout, filepath.Join("1.4.0", "agentic-carryforward")+" session-log 1") {
		t.Errorf("the version the plugin names should run, got: %+v", got)
	}

	if plugin.CurlCalls(t) != nil {
		t.Error("a version already present should never download")
	}
}

func TestSessionStartFetchesTheRelease(t *testing.T) {
	plugin := test.NewPlugin(t, repository, "1.4.0")
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

func TestParallelSessionStartsFetchOnce(t *testing.T) {
	plugin := test.NewPlugin(t, repository, "1.4.0")
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
	plugin := test.NewPlugin(t, repository, "1.4.0")
	plugin.Release(t, "1.4.0", "", "agentic-scratch")

	lock := filepath.Join(plugin.Data, ".fetch-1.4.0")
	test.WriteFile(t, filepath.Join(lock, "checksums.txt"), "left by a fetch that died\n")
	test.Age(t, lock, 10*time.Minute)

	got := plugin.Run(t, "hooks/run", nil, "--session-start", "agentic-scratch", "start")
	if got.Code != 0 || !strings.Contains(got.Stdout, filepath.Join("1.4.0", "agentic-scratch")) {
		t.Errorf("a lock left by a dead fetch should not hold the release back, got: %+v", got)
	}
}

func TestAMismatchingChecksumUnpacksNothing(t *testing.T) {
	plugin := test.NewPlugin(t, repository, "1.4.0")
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
			plugin := test.NewPlugin(t, repository, "1.10.1")
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
		plugin := test.NewPlugin(t, repository, "1.10.1")
		plugin.Release(t, "1.10.1", "", "agentic-grounding-gate")
		plugin.Installed(t, "1.9.0", "agentic-grounding-gate")

		plugin.Run(t, "hooks/run", nil, "agentic-grounding-gate", "turn")

		if plugin.CurlCalls(t) != nil {
			t.Error("only a session start should download")
		}
	})
}

func TestADataDirectoryItCannotWriteStillRunsWhatIsThere(t *testing.T) {
	plugin := test.NewPlugin(t, repository, "1.4.0")
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
		plugin := test.NewPlugin(t, repository, "1.4.0")
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
		{name: "a session start says why once", args: []string{"--session-start", "agentic-scratch", "start"}, notice: true},
		{name: "any other hook stays quiet", args: []string{"agentic-carryforward", "prompt"}},
		{name: "the gate's own record stays quiet", args: []string{"agentic-grounding-gate", "record"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plugin := test.NewPlugin(t, repository, "1.4.0")

			got := plugin.Run(t, "hooks/run", nil, tc.args...)
			if got.Code != tc.code {
				t.Errorf("the hook should exit as decided, got: %+v", got)
			}

			if tc.code == 2 && !strings.Contains(got.Stderr, "start a session with network") {
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
	plugin := test.NewPlugin(t, repository, "1.4.0")
	data := filepath.Join(plugin.Config, "plugins", "data", "agentic-helpers-agentic-helpers")
	plugin.Binaries(t, filepath.Join(data, "1.4.0"), "agentic-capture", "agentic-carryforward")

	bashTool := []string{"CLAUDE_PLUGIN_ROOT", "CLAUDE_PLUGIN_DATA", "CLAUDE_CONFIG_DIR=" + plugin.Config}

	cases := []struct {
		shim string
		args []string
	}{
		{shim: "bin/agentic-capture", args: []string{"--help"}},
		{shim: "bin/agentic-carryforward", args: []string{"claim", "main"}},
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
	content, err := os.ReadFile(filepath.Join(repository, "hooks", "hooks.json"))
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
