package sddcaps_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/sddcaps"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestWhatEachBuildOffers(t *testing.T) {
	cases := []struct {
		name   string
		runner *test.Runner
		want   sddcaps.Caps
	}{
		{name: "stock sdd", runner: &test.Runner{Stock: true}, want: sddcaps.Caps{Runs: true, Export: true}},
		{name: "the fork", runner: &test.Runner{}, want: sddcaps.Caps{Runs: true, Export: true, RepoPath: true}},
		{name: "a stock release", runner: &test.Runner{Stock: true, Unreleased: true}, want: sddcaps.Caps{Runs: true}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sddcaps.Probe{Runner: tc.runner}.Of(context.Background(), t.TempDir())
			if got != tc.want {
				t.Errorf("each surface should be read from the build's own help, got: %+v", got)
			}
		})
	}
}

func TestABuildWithoutRepoIsNotAskedForItsSubcommands(t *testing.T) {
	runner := &test.Runner{NoRepo: true}
	got := sddcaps.Probe{Runner: runner}.Of(context.Background(), t.TempDir())

	if got.RepoPath {
		t.Errorf("a build without `repo` should lack `repo path`, got: %+v", got)
	}

	for _, call := range runner.Calls {
		if len(call) > 1 && call[1] == "repo" {
			t.Errorf("a build listing no `repo` command should not be asked for its help, ran: %v", call)
		}
	}
}

func TestNoSddRunsNothing(t *testing.T) {
	got := sddcaps.Probe{Runner: &test.Runner{Missing: true}}.Of(context.Background(), t.TempDir())

	if got.Runs || got.Export || !strings.Contains(got.Printed, "sdd --help") {
		t.Errorf("a missing sdd should offer nothing and say what was tried, got: %+v", got)
	}
}

// binary is a stand-in sdd the probe keys its cache on.
func binary(t *testing.T) (string, func(string) (string, error)) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "sdd")
	test.WriteFile(t, path, "#!/bin/sh\n")

	return path, func(string) (string, error) { return path, nil }
}

func TestTheCacheKeepsTheAnswerPerBinary(t *testing.T) {
	path, lookPath := binary(t)
	cache := t.TempDir()
	runner := &test.Runner{Stock: true, Unreleased: true}
	probe := sddcaps.Probe{Runner: runner, CacheDir: cache, LookPath: lookPath}

	first := probe.Of(context.Background(), t.TempDir())
	probed := len(runner.Calls)

	if probe.Of(context.Background(), t.TempDir()) != first || len(runner.Calls) != probed {
		t.Error("an unchanged binary should be answered from the cache, running no sdd")
	}

	later := time.Now().Add(time.Hour)

	err := os.Chtimes(path, later, later)
	if err != nil {
		t.Fatalf("touching the binary: %v", err)
	}

	runner.Unreleased = false

	if !probe.Of(context.Background(), t.TempDir()).Export || len(runner.Calls) == probed {
		t.Error("a changed binary should be probed again")
	}
}

func TestAnAnswerKeptBeforeASurfaceWasAddedIsProbedAgain(t *testing.T) {
	path, lookPath := binary(t)
	cache := t.TempDir()

	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolving the binary: %v", err)
	}

	info, err := os.Stat(resolved)
	if err != nil {
		t.Fatalf("reading the binary: %v", err)
	}

	cases := []struct {
		name string
		key  string
		caps map[string]bool
	}{
		{
			name: "before export",
			key:  fmt.Sprintf("%s|%d|%d", resolved, info.ModTime().UnixNano(), info.Size()),
			caps: map[string]bool{"runs": true},
		},
		{
			name: "before repo path",
			key:  fmt.Sprintf("3|%s|%d|%d", resolved, info.ModTime().UnixNano(), info.Size()),
			caps: map[string]bool{"runs": true, "export": true},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kept, err := json.Marshal(map[string]any{"key": tc.key, "caps": tc.caps})
			if err != nil {
				t.Fatalf("encoding the kept answer: %v", err)
			}

			test.WriteFile(t, filepath.Join(cache, "sdd-caps.json"), string(kept))

			runner := &test.Runner{}
			got := sddcaps.Probe{Runner: runner, CacheDir: cache, LookPath: lookPath}.Of(context.Background(), t.TempDir())

			if !got.Export || !got.RepoPath || len(runner.Calls) == 0 {
				t.Errorf("an answer kept by an older probe should not stand for the binary, got: %+v", got)
			}
		})
	}
}

func TestAMissingSddIsNeverCached(t *testing.T) {
	_, lookPath := binary(t)
	cache := t.TempDir()
	runner := &test.Runner{Missing: true}
	probe := sddcaps.Probe{Runner: runner, CacheDir: cache, LookPath: lookPath}

	probe.Of(context.Background(), t.TempDir())
	runner.Missing = false

	if !probe.Of(context.Background(), t.TempDir()).Runs {
		t.Error("an sdd that failed to answer should be asked again on the next call")
	}
}
