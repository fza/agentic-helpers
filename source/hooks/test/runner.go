package test

import (
	"context"
	"slices"
	"sync"

	"github.com/fza/agentic-helpers/source/hooks/internal/process"
)

// Runner answers each command by what it is: the project's lint (run through
// `sh`), the pre-flight (`--dry-run`), or the capture itself.
type Runner struct {
	Lint    process.Result
	DryRun  process.Result
	Capture process.Result

	mutex sync.Mutex
	Calls [][]string
	Dirs  []string
}

func (fake *Runner) Run(_ context.Context, dir string, argv []string) (process.Result, error) {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()

	fake.Calls = append(fake.Calls, slices.Clone(argv))
	fake.Dirs = append(fake.Dirs, dir)

	switch {
	case argv[0] == "sh":
		return fake.Lint, nil
	case slices.Contains(argv, "--dry-run"):
		return fake.DryRun, nil
	default:
		return fake.Capture, nil
	}
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
