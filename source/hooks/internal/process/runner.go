package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
)

type Result struct {
	Stdout string
	Stderr string
	Code   int
}

type Runner struct{}

// Run runs argv in dir and reports how it ended. A command that exits non-zero
// is a result, not an error; only one that never started is an error.
func (Runner) Run(ctx context.Context, dir string, argv []string) (Result, error) {
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	command.Dir = dir

	var stdout, stderr bytes.Buffer

	command.Stdout = &stdout
	command.Stderr = &stderr

	err := command.Run()

	var exited *exec.ExitError
	if errors.As(err, &exited) {
		return Result{Stdout: stdout.String(), Stderr: stderr.String(), Code: exited.ExitCode()}, nil
	}

	if err != nil {
		return Result{}, fmt.Errorf("running %s: %w", argv[0], err)
	}

	return Result{Stdout: stdout.String(), Stderr: stderr.String()}, nil
}
