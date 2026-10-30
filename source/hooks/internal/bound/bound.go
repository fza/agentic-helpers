// Package bound runs a command for at most a duration, as GNU `timeout` does,
// for a machine without one: shell-guard rewrites a missing `timeout` to
// `agentic-helpers bound`. The command runs in a process group of its own, so
// when the duration runs out every process it started is signalled, and a Go
// program, which ignores the `SIGALRM` a perl alarm sends, ends too.
package bound

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
)

// ExitTimedOut / ExitFailed / ExitCannotRun / ExitNotFound are the exit
// statuses GNU `timeout` gives: the duration ran out, bound itself failed,
// the command could not run, the command was not found.
const (
	ExitTimedOut  = 124
	ExitFailed    = 125
	ExitCannotRun = 126
	ExitNotFound  = 127
)

// Grace is how long the group has between `SIGTERM` and `SIGKILL`: long
// enough for a test run to remove its temporary files.
const Grace = 5 * time.Second

const self = "agentic-helpers bound"

const usage = "usage: " + self + " <duration> <command> [argument...]; the duration in seconds, or with s, m, h or d"

var ErrDuration = errors.New("is no duration")

// decimal is the number GNU `timeout` takes: digits with an optional
// fraction, never a sign, an exponent, a hex form or `inf`, which
// strconv.ParseFloat would all take.
var decimal = regexp.MustCompile(`^([0-9]+(\.[0-9]*)?|\.[0-9]+)$`)

// Main runs `agentic-helpers bound <duration> <command> [argument...]` and
// returns the exit status: the command's own, or one of the Exit statuses.
func Main(ctx context.Context, args []string, streams hookio.Streams) int {
	return run(ctx, args, streams, Grace)
}

func run(ctx context.Context, args []string, streams hookio.Streams, grace time.Duration) int {
	if len(args) < 2 || strings.HasPrefix(args[0], "-") {
		_, _ = fmt.Fprintln(streams.Err, usage)

		return ExitFailed
	}

	limit, err := Parse(args[0])
	if err != nil {
		_, _ = fmt.Fprintf(streams.Err, "%s: %v\n", self, err)

		return ExitFailed
	}

	command := exec.Command(args[1], args[2:]...)
	command.Stdin = streams.In
	command.Stdout = streams.Out
	command.Stderr = streams.Err
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// A process that left the group may hold the output open; the wait for
	// it ends a grace after the command does.
	command.WaitDelay = grace

	received := make(chan os.Signal, 1)
	signal.Notify(received, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	defer signal.Stop(received)

	err = command.Start()
	if err != nil {
		_, _ = fmt.Fprintf(streams.Err, "%s: %v\n", self, err)

		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			return ExitNotFound
		}

		return ExitCannotRun
	}

	group := -command.Process.Pid

	ended := make(chan error, 1)

	go func() { ended <- command.Wait() }()

	var expiry <-chan time.Time

	if limit > 0 {
		timer := time.NewTimer(limit)
		defer timer.Stop()

		expiry = timer.C
	}

	for {
		select {
		case err = <-ended:
			return exitStatus(command, err)
		case forwarded := <-received:
			_ = syscall.Kill(group, forwarded.(syscall.Signal))
		case <-ctx.Done():
			_ = syscall.Kill(group, syscall.SIGTERM)
		case <-expiry:
			stop(group, ended, grace)

			return ExitTimedOut
		}
	}
}

// stop sends the group `SIGTERM`, waits up to grace for the command to end,
// then sends what is left of the group `SIGKILL`, so no process it started
// outlives the bound, and waits for the command's end.
func stop(group int, ended <-chan error, grace time.Duration) {
	_ = syscall.Kill(group, syscall.SIGTERM)

	select {
	case <-ended:
		_ = syscall.Kill(group, syscall.SIGKILL)

		return
	case <-time.After(grace):
	}

	_ = syscall.Kill(group, syscall.SIGKILL)

	<-ended
}

// exitStatus is the command's own exit status, or 128 plus the signal that
// ended it, as a shell reports one.
func exitStatus(command *exec.Cmd, err error) int {
	if err == nil {
		return 0
	}

	state := command.ProcessState
	if state == nil {
		return ExitCannotRun
	}

	status, isUnix := state.Sys().(syscall.WaitStatus)
	if isUnix && status.Signaled() {
		return 128 + int(status.Signal())
	}

	return state.ExitCode()
}

// Parse reads a duration as GNU `timeout` spells one: a number of seconds,
// a fraction allowed, optionally followed by s, m, h or d. Zero bounds
// nothing.
func Parse(text string) (time.Duration, error) {
	unit := time.Second
	number := text

	units := map[string]time.Duration{"s": time.Second, "m": time.Minute, "h": time.Hour, "d": 24 * time.Hour}
	for suffix, value := range units {
		if strings.HasSuffix(text, suffix) {
			unit = value
			number = strings.TrimSuffix(text, suffix)

			break
		}
	}

	if !decimal.MatchString(number) {
		return 0, fmt.Errorf("%q %w", text, ErrDuration)
	}

	value, err := strconv.ParseFloat(number, 64)
	if err != nil {
		return 0, fmt.Errorf("%q %w: %w", text, ErrDuration, err)
	}

	return time.Duration(value * float64(unit)), nil
}
