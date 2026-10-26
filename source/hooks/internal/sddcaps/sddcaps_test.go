package sddcaps_test

import (
	"context"
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
		{
			name:   "stock sdd",
			runner: &test.Runner{Stock: true},
			want:   sddcaps.Caps{Runs: true, Export: true},
		},
		{
			name:   "the fork",
			runner: &test.Runner{},
			want: sddcaps.Caps{Runs: true, PreflightVerified: true, Title: true, ActorKind: true, LocalConfig: true,
				Migrate: true, Export: true, ProjectDir: true},
		},
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
	runner := &test.Runner{Stock: true}
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

	runner.Stock = false

	if !probe.Of(context.Background(), t.TempDir()).Migrate || len(runner.Calls) == probed {
		t.Error("a changed binary should be probed again")
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
