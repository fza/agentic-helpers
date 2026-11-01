package process

import (
	"context"
	"fmt"
	"os/exec"
	"syscall"
)

// Detached starts a process in a session of its own, its output discarded,
// and lets it run on once the caller exits.
type Detached struct{}

func (Detached) Start(_ context.Context, path string, args ...string) error {
	command := exec.Command(path, args...)
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	err := command.Start()
	if err != nil {
		return fmt.Errorf("starting %s: %w", path, err)
	}

	return command.Process.Release()
}
