package process_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/process"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestRunReportsHowTheCommandEnded(t *testing.T) {
	got, err := process.Runner{}.Run(context.Background(), t.TempDir(), []string{"sh", "-c", "echo out; echo err >&2; exit 3"})
	if err != nil {
		t.Fatalf("a command that ran should be a result, got: %v", err)
	}

	if got.Code != 3 || got.Stdout != "out\n" || got.Stderr != "err\n" {
		t.Errorf("the exit code and both streams should pass through, got: %+v", got)
	}

	_, err = process.Runner{}.Run(context.Background(), t.TempDir(), []string{"agentic-no-such-command"})
	if err == nil {
		t.Error("a command that never started should be an error")
	}
}

func TestACancelledRunKillsTheWholeGroup(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "hang.sh")
	pidFile := filepath.Join(dir, "child.pid")
	test.WriteFile(t, script, test.Fixture(t, "process/hang.sh"))

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	started := time.Now()

	_, err := process.Runner{}.Run(ctx, dir, []string{"sh", script, pidFile})
	if err != nil {
		t.Fatalf("a killed command should still be a result, got: %v", err)
	}

	if time.Since(started) > 3*time.Second {
		t.Error("the run should end at the cut-off, not wait for a child holding its pipes")
	}

	written, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("reading the child's pid: %v", err)
	}

	child, err := strconv.Atoi(strings.TrimSpace(string(written)))
	if err != nil {
		t.Fatalf("parsing the child's pid: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for !errors.Is(syscall.Kill(child, 0), syscall.ESRCH) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(child, syscall.SIGKILL)

			t.Fatal("the child should die with the command that started it")
		}

		time.Sleep(20 * time.Millisecond)
	}
}

func TestARunStopsReadingPipesAChildOutsideTheGroupHolds(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "escape.sh")
	pidFile := filepath.Join(dir, "child.pid")
	test.WriteFile(t, script, test.Fixture(t, "process/escape.sh"))

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	started := time.Now()

	_, _ = process.Runner{}.Run(ctx, dir, []string{"sh", script, pidFile})

	elapsed := time.Since(started)

	written, _ := os.ReadFile(pidFile)
	child, _ := strconv.Atoi(strings.TrimSpace(string(written)))

	if child > 0 {
		_ = syscall.Kill(child, syscall.SIGKILL)
	}

	if elapsed > 15*time.Second {
		t.Error("the run should give up on pipes a stray child holds open")
	}
}
