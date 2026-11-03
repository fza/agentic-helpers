package plugin_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

// usedAt is when the version's use mark was last written, zero where it has
// none.
func usedAt(t *testing.T, plugin *test.Plugin, version string) time.Time {
	t.Helper()

	info, err := os.Stat(filepath.Join(plugin.Data, version, "used"))
	if err != nil {
		return time.Time{}
	}

	return info.ModTime()
}

func ageMark(t *testing.T, plugin *test.Plugin, version string) time.Time {
	t.Helper()

	mark := filepath.Join(plugin.Data, version, "used")
	old := time.Now().Add(-30 * 24 * time.Hour).Truncate(time.Second)

	test.WriteFile(t, mark, "\n")

	err := os.Chtimes(mark, old, old)
	if err != nil {
		t.Fatalf("aging the use mark: %v", err)
	}

	return old
}

func TestASessionStartMarksTheVersionItRuns(t *testing.T) {
	cases := []struct {
		name    string
		extra   []string
		args    []string
		prepare func(t *testing.T, plugin *test.Plugin)
		version string
		marked  bool
	}{
		{name: "the named version present", args: []string{"--session-start", "agentic-scratch", "start"},
			prepare: func(t *testing.T, plugin *test.Plugin) { plugin.Installed(t, "1.4.0", "agentic-scratch") },
			version: "1.4.0", marked: true},
		{name: "the named version fetched", args: []string{"--session-start", "agentic-scratch", "start"},
			prepare: func(t *testing.T, plugin *test.Plugin) { plugin.Release(t, "1.4.0", "", "agentic-scratch") },
			version: "1.4.0", marked: true},
		{name: "the newest version standing in", extra: []string{"FAKE_RELEASES=/nonexistent"},
			args:    []string{"--session-start", "agentic-scratch", "start"},
			prepare: func(t *testing.T, plugin *test.Plugin) { plugin.Installed(t, "1.3.0", "agentic-scratch") },
			version: "1.3.0", marked: true},
		{name: "a prompt hook", args: []string{"agentic-scratch", "turn"},
			prepare: func(t *testing.T, plugin *test.Plugin) { plugin.Installed(t, "1.4.0", "agentic-scratch") },
			version: "1.4.0"},
		{name: "a command", args: []string{"--command", "agentic-scratch", "list"},
			prepare: func(t *testing.T, plugin *test.Plugin) { plugin.Installed(t, "1.4.0", "agentic-scratch") },
			version: "1.4.0"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plugin := test.NewPlugin(t, pluginRoot, "1.4.0")
			tc.prepare(t, plugin)

			var old time.Time
			if test.Exists(t, filepath.Join(plugin.Data, tc.version)) {
				old = ageMark(t, plugin, tc.version)
			}

			before := time.Now().Add(-time.Second)

			got := plugin.Run(t, "hooks/run", tc.extra, tc.args...)
			if got.Code != 0 || got.Stderr != "" {
				t.Fatalf("the launcher should run the binary quietly, got: %+v", got)
			}

			marked := usedAt(t, plugin, tc.version)
			if marked.After(before) != tc.marked {
				t.Errorf("only a SessionStart should write a fresh use mark into the version it runs, marked at %s", marked)
			}

			if !tc.marked && !marked.Equal(old) {
				t.Error("a call other than a SessionStart should leave the use mark as it was")
			}
		})
	}
}

func TestAVersionItCannotWriteStillRuns(t *testing.T) {
	plugin := test.NewPlugin(t, pluginRoot, "1.4.0")
	plugin.Installed(t, "1.4.0", "agentic-scratch")

	dir := filepath.Join(plugin.Data, "1.4.0")

	err := os.Chmod(dir, 0o555)
	if err != nil {
		t.Fatalf("making the version read-only: %v", err)
	}

	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	got := plugin.Run(t, "hooks/run", nil, "--session-start", "agentic-scratch", "start")
	if got.Code != 0 || got.Stderr != "" || got.Stdout != "ran "+filepath.Join(dir, "agentic-scratch")+" start\n" {
		t.Errorf("a version the session cannot mark should still run, saying nothing more, got: %+v", got)
	}

	if !usedAt(t, plugin, "1.4.0").IsZero() {
		t.Error("a read-only version should carry no use mark")
	}
}
