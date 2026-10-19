package test

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/process"
)

// Runner answers each command by what it is: the project's lint (run through
// `sh`), the pre-flight (`--dry-run`), or the capture itself. The first Hangs
// pre-flights run until their context ends, the way a provider that never
// answers holds `sdd`; the rest answer DryRuns in turn, the last repeating,
// or DryRun where DryRuns is empty.
type Runner struct {
	Lint    process.Result
	DryRun  process.Result
	DryRuns []process.Result
	Hangs   int
	Capture process.Result

	mutex  sync.Mutex
	Calls  [][]string
	Dirs   []string
	Limits []time.Duration
	tries  int
}

func (fake *Runner) Run(ctx context.Context, dir string, argv []string) (process.Result, error) {
	fake.mutex.Lock()

	fake.Calls = append(fake.Calls, slices.Clone(argv))
	fake.Dirs = append(fake.Dirs, dir)

	deadline, bounded := ctx.Deadline()
	if bounded {
		fake.Limits = append(fake.Limits, time.Until(deadline))
	}

	switch {
	case argv[0] == "sh":
		fake.mutex.Unlock()

		return fake.Lint, nil
	case slices.Contains(argv, "--dry-run"):
		try := fake.tries
		fake.tries++
		fake.mutex.Unlock()

		return fake.preflight(ctx, try), nil
	default:
		fake.mutex.Unlock()

		return fake.Capture, nil
	}
}

func (fake *Runner) preflight(ctx context.Context, try int) process.Result {
	if try < fake.Hangs {
		<-ctx.Done()

		return process.Result{Code: -1}
	}

	if len(fake.DryRuns) == 0 {
		return fake.DryRun
	}

	return fake.DryRuns[min(try-fake.Hangs, len(fake.DryRuns)-1)]
}

func (fake *Runner) CallsOf(kind string) [][]string {
	var found [][]string

	for _, call := range fake.Calls {
		lint := call[0] == "sh"
		dry := slices.Contains(call, "--dry-run")

		if kind == "lint" && lint || kind == "dry-run" && dry || kind == "capture" && !lint && !dry {
			found = append(found, call)
		}
	}

	return found
}

// Pauses stands in for the wait between pre-flight tries, recording each delay
// and returning at once.
type Pauses struct {
	mutex  sync.Mutex
	Waited []time.Duration
}

func (fake *Pauses) Pause(ctx context.Context, delay time.Duration) error {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()

	fake.Waited = append(fake.Waited, delay)

	return ctx.Err()
}
