package bound

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
)

// childRole names what the test binary does when a bound starts it: the
// command under the bound is this same Go program, so every case also shows a
// Go program, deaf to SIGALRM, ending at the bound.
const childRole = "BOUND_TEST_CHILD"

func TestMain(m *testing.M) {
	role, found := os.LookupEnv(childRole)
	if !found {
		os.Exit(m.Run())
	}

	name, argument, _ := strings.Cut(role, ":")

	switch name {
	case "exit":
		code, _ := strconv.Atoi(argument)
		os.Exit(code)
	case "sleep":
		writePID(argument)
		time.Sleep(10 * time.Minute)
	case "deaf":
		signal.Ignore(syscall.SIGTERM)
		writePID(argument)
		time.Sleep(10 * time.Minute)
	case "parent":
		grandchild := exec.Command(os.Args[0])
		grandchild.Env = append(os.Environ(), childRole+"=sleep:"+argument)

		err := grandchild.Start()
		if err != nil {
			os.Exit(3)
		}

		time.Sleep(10 * time.Minute)
	}

	os.Exit(2)
}

func writePID(path string) {
	if path != "" {
		_ = os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o600)
	}
}

func bounded(t *testing.T, role string, args ...string) (int, string, time.Duration) {
	t.Helper()
	t.Setenv(childRole, role)

	var errOut bytes.Buffer

	streams := hookio.Streams{In: strings.NewReader(""), Out: &bytes.Buffer{}, Err: &errOut}
	started := time.Now()
	finished := make(chan int, 1)

	go func() { finished <- run(context.Background(), append(args, os.Args[0]), streams, 300*time.Millisecond) }()

	select {
	case code := <-finished:
		return code, errOut.String(), time.Since(started)
	case <-time.After(10 * time.Second):
		t.Fatal("the bound should end its command within a grace of the duration")
	}

	return 0, "", 0
}

func waitedPID(t *testing.T, path string) int {
	t.Helper()

	for range 200 {
		data, err := os.ReadFile(path)
		if err == nil && len(data) > 0 {
			pid, _ := strconv.Atoi(string(data))

			return pid
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("the child should have written its pid")

	return 0
}

func gone(pid int) bool {
	for range 200 {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return true
		}

		time.Sleep(10 * time.Millisecond)
	}

	return false
}

func TestABoundEndsAGoProgramAtItsDuration(t *testing.T) {
	code, _, took := bounded(t, "sleep:", "1")
	if code != ExitTimedOut || took > 3*time.Second {
		t.Errorf("a Go program running past the bound should end at it with the timed-out status, got %d after %v", code, took)
	}
}

func TestACommandEndingWithinTheBoundKeepsItsStatus(t *testing.T) {
	code, _, took := bounded(t, "exit:3", "30")
	if code != 3 || took > 3*time.Second {
		t.Errorf("a command ending within the bound should pass its own status through, got %d after %v", code, took)
	}
}

func TestACommandDeafToTermIsKilledAfterTheGrace(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")

	code, _, _ := bounded(t, "deaf:"+pidFile, "0.2")
	if code != ExitTimedOut {
		t.Errorf("a command ignoring SIGTERM should still end with the timed-out status, got %d", code)
	}

	if !gone(waitedPID(t, pidFile)) {
		t.Error("a command ignoring SIGTERM should be killed once the grace runs out")
	}
}

func TestEveryProcessTheCommandStartedEndsWithIt(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")

	code, _, _ := bounded(t, "parent:"+pidFile, "1")
	if code != ExitTimedOut {
		t.Errorf("the bound should run out, got %d", code)
	}

	if !gone(waitedPID(t, pidFile)) {
		t.Error("a process the command started should end with the command's group")
	}
}

func TestATermSentToTheBoundReachesTheCommand(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	t.Setenv(childRole, "sleep:"+pidFile)

	finished := make(chan int, 1)

	go func() {
		streams := hookio.Streams{In: strings.NewReader(""), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}}
		finished <- run(context.Background(), []string{"30", os.Args[0]}, streams, time.Second)
	}()

	waitedPID(t, pidFile)

	err := syscall.Kill(os.Getpid(), syscall.SIGTERM)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case code := <-finished:
		if code != 128+int(syscall.SIGTERM) {
			t.Errorf("the command should end by the SIGTERM the bound passed on, got %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Error("a SIGTERM sent to the bound should reach the command")
	}
}

func TestWhatTheBoundCannotRunIsNamed(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want int
		says string
	}{
		{name: "no command", args: []string{"5"}, want: ExitFailed, says: "usage:"},
		{name: "an option before the duration", args: []string{"-k", "5", "true"}, want: ExitFailed, says: "usage:"},
		{name: "a duration of another form", args: []string{"soon", "true"}, want: ExitFailed, says: `"soon" is no duration`},
		{name: "a command not found", args: []string{"5", "agentic-bound-no-such-command"}, want: ExitNotFound, says: "agentic-bound-no-such-command"},
		{name: "a path that does not exist", args: []string{"5", "/nonexistent/agentic-bound"}, want: ExitNotFound, says: "/nonexistent/agentic-bound"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var errOut bytes.Buffer

			streams := hookio.Streams{In: strings.NewReader(""), Out: &bytes.Buffer{}, Err: &errOut}

			code := run(context.Background(), tc.args, streams, time.Second)
			if code != tc.want || !strings.Contains(errOut.String(), tc.says) {
				t.Errorf("the bound should refuse with its own status and say why, got %d %q", code, errOut.String())
			}
		})
	}
}

func TestADurationReadsAsGNUTimeoutReadsIt(t *testing.T) {
	cases := []struct {
		text  string
		want  time.Duration
		valid bool
	}{
		{text: "300", want: 300 * time.Second, valid: true},
		{text: "1.5", want: 1500 * time.Millisecond, valid: true},
		{text: ".5s", want: 500 * time.Millisecond, valid: true},
		{text: "5m", want: 5 * time.Minute, valid: true},
		{text: "2h", want: 2 * time.Hour, valid: true},
		{text: "1d", want: 24 * time.Hour, valid: true},
		{text: "0", want: 0, valid: true},
		{text: "-5"},
		{text: "1e3"},
		{text: "inf"},
		{text: "0x10"},
		{text: "5 m"},
		{text: "m"},
		{text: ""},
	}

	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			got, err := Parse(tc.text)
			if tc.valid && (err != nil || got != tc.want) {
				t.Errorf("a duration in GNU's form should read, got %v %v", got, err)
			}

			if !tc.valid && !errors.Is(err, ErrDuration) {
				t.Errorf("anything else should be refused as no duration, got %v %v", got, err)
			}
		})
	}
}

func TestAZeroDurationBoundsNothing(t *testing.T) {
	code, _, _ := bounded(t, "exit:0", "0")
	if code != 0 {
		t.Errorf("a zero duration should let the command run to its end, got %d", code)
	}
}
