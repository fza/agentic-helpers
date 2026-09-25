// Package process reads the process table through `ps`, the one reader present
// on every machine these hooks run on without CGO.
package process

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type Table struct{}

func (Table) Self() int {
	return os.Getpid()
}

func (Table) Parent() int {
	return os.Getppid()
}

// ParentAndName is the parent pid and the executable's base name, or zero and
// an empty name where the process is gone.
func (Table) ParentAndName(ctx context.Context, pid int) (int, string) {
	output, err := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "ppid=,comm=").Output()
	if err != nil {
		return 0, ""
	}

	fields := strings.Fields(strings.TrimSpace(string(output)))
	if len(fields) < 2 {
		return 0, ""
	}

	parent, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, ""
	}

	command := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(output)), fields[0]))

	return parent, filepath.Base(command)
}

// StartTime identifies one run of a pid, so a reused pid never passes for the
// process that held it before.
func (Table) StartTime(ctx context.Context, pid int) string {
	output, err := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "lstart=").Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(output))
}
