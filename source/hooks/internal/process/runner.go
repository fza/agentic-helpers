package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

type Result struct {
	Stdout string
	Stderr string
	Code   int
}

type Runner struct{}

// Run runs argv in dir and reports how it ended. A command that exits non-zero
// is a result, not an error; only one that never started is an error.
//
// The command leads its own process group, and a cancelled ctx kills the whole
// group: `sdd` starts `claude -p` sharing its output pipes, so killing `sdd`
// alone leaves the child running and Run reading its pipes until it exits.
func (Runner) Run(ctx context.Context, dir string, argv []string) (Result, error) {
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	command.Dir = dir
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	command.WaitDelay = 5 * time.Second

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
