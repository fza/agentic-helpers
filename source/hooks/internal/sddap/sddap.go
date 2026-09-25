// Package sddap registers seats with the sddap tool, so every session and
// screen on the machine asks one place who holds a seat. A machine without the
// tool changes nothing.
package sddap

import (
	"context"
	"os/exec"
	"strconv"
	"time"
)

const callTimeout = 10 * time.Second

type Seats struct {
	Dir string
}

func (seats Seats) Take(ctx context.Context, role string, session string, pid int) {
	seats.run(ctx, "seat", "take", role, "--session", session, "--pid", strconv.Itoa(pid))
}

func (seats Seats) Release(ctx context.Context, role string) {
	seats.run(ctx, "seat", "release", role)
}

// run reports nothing: the seat is recorded in `.memory/roles/` either way,
// and sddap is a second reader of it rather than its owner.
func (seats Seats) run(ctx context.Context, args ...string) {
	bounded, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	command := exec.CommandContext(bounded, "sddap", args...)
	command.Dir = seats.Dir

	_ = command.Run()
}
