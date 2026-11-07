package test

import (
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/fza/agentic-helpers/source/hooks/internal/process"
)

// Claude stands in for the `claude` command: it records every call, runs
// OnRun first where set, so a test can look at the tree at that moment, and
// answers Code, or fails as a command never started where Missing is set.
type Claude struct {
	Missing bool
	Code    int
	OnRun   func(argv []string)

	mutex sync.Mutex
	calls [][]string
}

func (fake *Claude) Run(_ context.Context, _ string, argv []string) (process.Result, error) {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()

	if fake.Missing {
		return process.Result{}, errors.New(`exec: "claude": executable file not found in $PATH`)
	}

	fake.calls = append(fake.calls, slices.Clone(argv))

	if fake.OnRun != nil {
		fake.OnRun(argv)
	}

	if fake.Code != 0 {
		return process.Result{Code: fake.Code, Stderr: "failed"}, nil
	}

	return process.Result{}, nil
}

func (fake *Claude) Calls() [][]string {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()

	return slices.Clone(fake.calls)
}
