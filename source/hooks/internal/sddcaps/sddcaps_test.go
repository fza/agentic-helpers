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

	"github.com/fza/agentic-helpers/source/hooks/internal/process"
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
			want:   sddcaps.Caps{Runs: true, PreflightTimeout: true, Export: true},
		},
		{
			name:   "the fork",
			runner: &test.Runner{},
			want: sddcaps.Caps{Runs: true, PreflightVerified: true, PreflightTimeout: true, Title: true, ActorKind: true, LocalConfig: true,
				Migrate: true, Export: true, ProjectDir: true, InfoJSON: true},
		},
		{
			name:   "a fork release before info json",
			runner: &test.Runner{NoInfoFormat: true},
			want: sddcaps.Caps{Runs: true, PreflightVerified: true, PreflightTimeout: true, Title: true, ActorKind: true, LocalConfig: true,
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

func TestInfoReadsWhatWritesToTheGraphTake(t *testing.T) {
	cases := []struct {
		name   string
		answer process.Result
		want   *sddcaps.Capabilities
	}{
		{name: "titles", answer: process.Result{Stdout: `{"graph_schema": 2, "capabilities": {"titles": true}}`}, want: &sddcaps.Capabilities{Titles: true}},
		{name: "no titles", answer: process.Result{Stdout: `{"graph_schema": 1, "capabilities": {"titles": false}}`}, want: &sddcaps.Capabilities{}},
		{name: "a graph newer than the binary", answer: process.Result{Stdout: `{"graph_schema": 3, "capabilities": null}`}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sddcaps.Info(context.Background(), &test.Runner{Info: tc.answer}, t.TempDir())
			if err != nil {
				t.Fatalf("a JSON answer should be read: %v", err)
			}

			if (got.Capabilities == nil) != (tc.want == nil) || got.Capabilities != nil && *got.Capabilities != *tc.want {
				t.Errorf("the capabilities should be those sdd printed, got: %+v", got.Capabilities)
			}
		})
	}
}

func TestInfoNamesAFailedRun(t *testing.T) {
	cases := []struct {
		name   string
		answer process.Result
		says   string
	}{
		{name: "a non-zero exit", answer: process.Result{Code: 1, Stderr: "graph not found"}, says: "graph not found"},
		{name: "output that is no JSON", answer: process.Result{Stdout: "Participant: Felix"}, says: "reading `sdd info --format json`"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := sddcaps.Info(context.Background(), &test.Runner{Info: tc.answer}, t.TempDir())
			if err == nil || !strings.Contains(err.Error(), tc.says) {
				t.Errorf("a failed run should say what sdd printed, got: %v", err)
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

	kept, err := json.Marshal(map[string]any{
		"key":  fmt.Sprintf("%s|%d|%d", resolved, info.ModTime().UnixNano(), info.Size()),
		"caps": map[string]bool{"runs": true, "title": true},
	})
	if err != nil {
		t.Fatalf("encoding the kept answer: %v", err)
	}

	test.WriteFile(t, filepath.Join(cache, "sdd-caps.json"), string(kept))

	runner := &test.Runner{}
	got := sddcaps.Probe{Runner: runner, CacheDir: cache, LookPath: lookPath}.Of(context.Background(), t.TempDir())

	if !got.InfoJSON || len(runner.Calls) == 0 {
		t.Errorf("an answer kept by an older probe should not stand for the binary, got: %+v", got)
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
