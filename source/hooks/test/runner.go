package test

import (
	"context"
	"errors"
	"slices"
	"strings"
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
	// HangCapture holds the capture call until its context ends, the way a
	// provider that never answers holds a stock sdd's one call.
	HangCapture bool

	// Help answers `sdd [<command>] --help` from the help texts under
	// fixtures/sdd: the fork's where Stock is false, stock sdd's where it is
	// true. Missing stands for no sdd on the PATH, and Unreleased takes the
	// `export` command out of the help, as no stock release carries it.
	Stock      bool
	Missing    bool
	Unreleased bool

	// Before runs ahead of every call it answers, the way a file changes on
	// disk while the lint or a pre-flight runs.
	Before func(argv []string)

	mutex  sync.Mutex
	Calls  [][]string
	Dirs   []string
	Limits []time.Duration
	tries  int
}

func (fake *Runner) Run(ctx context.Context, dir string, argv []string) (process.Result, error) {
	if fake.Before != nil {
		fake.Before(argv)
	}

	fake.mutex.Lock()

	fake.Calls = append(fake.Calls, slices.Clone(argv))
	fake.Dirs = append(fake.Dirs, dir)

	deadline, bounded := ctx.Deadline()
	if bounded && !isHelp(argv) {
		fake.Limits = append(fake.Limits, time.Until(deadline))
	}

	switch {
	case isHelp(argv):
		fake.mutex.Unlock()

		return fake.help(argv)
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

		if fake.HangCapture {
			<-ctx.Done()

			return process.Result{Code: -1}, nil
		}

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
		help := isHelp(call)
		capture := !lint && !dry && !help

		if kind == "lint" && lint || kind == "dry-run" && dry || kind == "help" && help || kind == "capture" && capture {
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

func isHelp(argv []string) bool {
	return len(argv) >= 2 && len(argv) <= 3 && argv[0] == "sdd" && argv[len(argv)-1] == "--help"
}

func (fake *Runner) help(argv []string) (process.Result, error) {
	if fake.Missing {
		return process.Result{}, errors.New("running sdd: executable file not found in $PATH")
	}

	build := "fork"
	if fake.Stock {
		build = "stock"
	}

	page := "root"
	if len(argv) == 3 {
		page = argv[1]
	}

	text, err := fixtures.ReadFile("fixtures/sdd/" + build + "/" + page + ".txt")
	if err != nil {
		return process.Result{Code: 3, Stderr: "No help topic for '" + page + "'"}, nil
	}

	help := string(text)

	if fake.Unreleased {
		var kept []string

		for line := range strings.SplitSeq(help, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "export ") {
				kept = append(kept, line)
			}
		}

		help = strings.Join(kept, "\n")
	}

	return process.Result{Stdout: help}, nil
}
