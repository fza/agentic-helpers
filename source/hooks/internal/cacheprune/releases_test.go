package cacheprune_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/cacheprune"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const named = "2.25.2"

// placeRelease unpacks a fake release of version into dir, its use mark
// touched at used, or none where used is zero.
func placeRelease(t *testing.T, dir string, version string, used time.Time) {
	t.Helper()

	test.WriteFile(t, filepath.Join(dir, version, "agentic-memory"), "#!/bin/sh\n")

	if used.IsZero() {
		return
	}

	mark := filepath.Join(dir, version, cacheprune.UsedName)
	test.WriteFile(t, mark, "\n")

	err := os.Chtimes(mark, used, used)
	if err != nil {
		t.Fatalf("dating the use mark of %s: %v", version, err)
	}
}

func TestARunDeletesOnlyTheReleasesNoSessionMayRun(t *testing.T) {
	roots := newRoots(t)
	dir := filepath.Join(t.TempDir(), "data")
	link := filepath.Join(t.TempDir(), "data-link")

	err := os.MkdirAll(dir, 0o700)
	if err != nil {
		t.Fatalf("creating the data directory: %v", err)
	}

	err = os.Symlink(dir, link)
	if err != nil {
		t.Fatalf("linking the data directory: %v", err)
	}

	cases := []struct {
		name    string
		version string
		used    time.Time
		deleted bool
	}{
		{name: "the named version stays, even unused", version: named},
		{name: "a version a process runs from stays", version: "2.20.0"},
		{name: "a version a process runs from through the resolved directory stays", version: "2.19.0"},
		{name: "a version used within two weeks stays", version: "2.21.0", used: now.Add(-13 * 24 * time.Hour)},
		{name: "a version used over two weeks ago goes", version: "2.22.0", used: now.Add(-15 * 24 * time.Hour),
			deleted: true},
		{name: "a version never marked goes", version: "2.23.0", deleted: true},
	}

	for _, tc := range cases {
		placeRelease(t, dir, tc.version, tc.used)
	}

	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolving the data directory: %v", err)
	}

	processes := test.Executables{Paths: []string{
		filepath.Join(link, "2.20.0", "agentic-memory"),
		filepath.Join(resolved, "2.19.0", "agentic-tools"),
		"/usr/local/bin/agentic-memory",
	}}

	result := cacheprune.Run(context.Background(), roots, retention,
		cacheprune.Releases{Dir: link, Named: named, Processes: processes}, now)
	if result.Error != "" {
		t.Fatalf("the run should succeed: %s", result.Error)
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if test.Exists(t, filepath.Join(dir, tc.version)) == tc.deleted {
				t.Error("a release should go exactly when no session may still run it")
			}

			if slices.Contains(result.Deleted, filepath.Join(link, tc.version)) != tc.deleted {
				t.Error("the run should record exactly the releases it deleted")
			}
		})
	}
}

func TestARunLeavesEverythingButReleaseFolders(t *testing.T) {
	roots := newRoots(t)
	dir := t.TempDir()

	placeRelease(t, dir, "2.22.0", time.Time{})
	test.WriteFile(t, filepath.Join(dir, "notes", "2.22.0"), "kept\n")
	test.WriteFile(t, filepath.Join(dir, "2.23", "agentic-memory"), "#!/bin/sh\n")
	test.WriteFile(t, filepath.Join(dir, "v2.23.0", "agentic-memory"), "#!/bin/sh\n")
	test.WriteFile(t, filepath.Join(dir, ".fetch-2.26.0.4242", "checksums.txt"), "sum\n")
	test.WriteFile(t, filepath.Join(dir, "2.24.0"), "a file named as a version\n")
	test.WriteFile(t, filepath.Join(dir, "README"), "kept by Claude Code\n")

	outside := t.TempDir()
	placeRelease(t, outside, "2.21.0", time.Time{})

	err := os.Symlink(filepath.Join(outside, "2.21.0"), filepath.Join(dir, "2.21.0"))
	if err != nil {
		t.Fatalf("linking a release: %v", err)
	}

	result := cacheprune.Run(context.Background(), roots, retention,
		cacheprune.Releases{Dir: dir, Named: named, Processes: test.Executables{}}, now)
	if result.Error != "" {
		t.Fatalf("the run should succeed: %s", result.Error)
	}

	if test.Exists(t, filepath.Join(dir, "2.22.0")) {
		t.Error("an unused release folder should go")
	}

	for _, kept := range []string{"notes", "2.23", "v2.23.0", ".fetch-2.26.0.4242", "2.24.0", "README", "2.21.0"} {
		_, err := os.Lstat(filepath.Join(dir, kept))
		if err != nil {
			t.Errorf("anything but a folder named as a version should stay, lost %s", kept)
		}
	}

	if !test.Exists(t, filepath.Join(outside, "2.21.0", "agentic-memory")) {
		t.Error("a link named as a version should never be followed")
	}
}

func TestARunRemovesAReleaseACrashLeftMovedAside(t *testing.T) {
	roots := newRoots(t)
	dir := t.TempDir()

	test.WriteFile(t, filepath.Join(dir, ".pruned-2.10.0", "agentic-memory"), "#!/bin/sh\n")
	test.WriteFile(t, filepath.Join(dir, ".pruned-notes", "kept"), "kept\n")

	result := cacheprune.Run(context.Background(), roots, retention,
		cacheprune.Releases{Dir: dir, Named: named, Processes: test.Executables{}}, now)
	if result.Error != "" {
		t.Fatalf("the run should succeed: %s", result.Error)
	}

	if test.Exists(t, filepath.Join(dir, ".pruned-2.10.0")) || slices.Contains(result.Deleted, filepath.Join(dir, ".pruned-2.10.0")) {
		t.Error("a release a crash left moved aside should go with the next run, never counted as a release")
	}

	if !test.Exists(t, filepath.Join(dir, ".pruned-notes")) {
		t.Error("a moved-aside folder not named after a version should stay")
	}
}

func TestARunThatCannotTellWhatRunsKeepsEveryRelease(t *testing.T) {
	cases := []struct {
		name      string
		releases  func(dir string) cacheprune.Releases
		denied    bool
		withError bool
	}{
		{name: "no named version, as a run started by hand", releases: func(dir string) cacheprune.Releases {
			return cacheprune.Releases{Dir: dir, Processes: test.Executables{}}
		}},
		{name: "no process table", releases: func(dir string) cacheprune.Releases {
			return cacheprune.Releases{Dir: dir, Named: named}
		}},
		{name: "a process table denied, as in a sandbox", denied: true, releases: func(dir string) cacheprune.Releases {
			return cacheprune.Releases{Dir: dir, Named: named, Processes: test.Executables{Err: os.ErrPermission}}
		}},
		{name: "a process table that fails", withError: true, releases: func(dir string) cacheprune.Releases {
			return cacheprune.Releases{Dir: dir, Named: named, Processes: test.Executables{Err: os.ErrInvalid}}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			roots := newRoots(t)
			dir := t.TempDir()
			placeRelease(t, dir, "2.22.0", time.Time{})

			result := cacheprune.Run(context.Background(), roots, retention, tc.releases(dir), now)

			if !test.Exists(t, filepath.Join(dir, "2.22.0")) || len(result.Deleted) > 0 {
				t.Error("a run that cannot tell which releases a session runs should keep every one")
			}

			if (result.Denied > 0) != tc.denied || (result.Error != "") != tc.withError {
				t.Errorf("a denied process table should count as denied and a failing one as an error, got %+v", result)
			}
		})
	}
}

func TestARunPastItsLimitDeletesNoMoreReleases(t *testing.T) {
	roots := newRoots(t)
	dir := t.TempDir()
	placeRelease(t, dir, "2.22.0", time.Time{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := cacheprune.Run(ctx, roots, retention, cacheprune.Releases{Dir: dir, Named: named, Processes: test.Executables{}}, now)

	if !test.Exists(t, filepath.Join(dir, "2.22.0")) || result.Error == "" {
		t.Error("a run stopped by its limit should delete no further release and record why it stopped")
	}
}

func TestThePluginsOwnVariablesNameItsReleases(t *testing.T) {
	root := t.TempDir()
	data := t.TempDir()
	test.WriteFile(t, filepath.Join(root, ".claude-plugin", "plugin.json"),
		"{\n  \"name\": \"agentic-helpers\",\n  \"version\": \""+named+"\"\n}\n")

	processes := test.Executables{}

	cases := []struct {
		name      string
		variables map[string]string
		want      cacheprune.Releases
	}{
		{name: "a hook's or an MCP server's environment",
			variables: map[string]string{"CLAUDE_PLUGIN_ROOT": root, "CLAUDE_PLUGIN_DATA": data},
			want:      cacheprune.Releases{Dir: data, Named: named, Processes: processes}},
		{name: "a command run by hand", variables: map[string]string{}},
		{name: "no plugin root", variables: map[string]string{"CLAUDE_PLUGIN_DATA": data}},
		{name: "a relative data directory", variables: map[string]string{"CLAUDE_PLUGIN_ROOT": root, "CLAUDE_PLUGIN_DATA": "data"}},
		{name: "a plugin root without a manifest",
			variables: map[string]string{"CLAUDE_PLUGIN_ROOT": t.TempDir(), "CLAUDE_PLUGIN_DATA": data},
			want:      cacheprune.Releases{Dir: data, Processes: processes}},
	}

	if cacheprune.PluginReleases(nil, processes).Dir != "" {
		t.Error("no environment should name no releases")
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cacheprune.PluginReleases(func(key string) string { return tc.variables[key] }, processes)
			if got.Dir != tc.want.Dir || got.Named != tc.want.Named {
				t.Errorf("the releases should come from the plugin's own variables and manifest alone, got %+v", got)
			}
		})
	}
}
