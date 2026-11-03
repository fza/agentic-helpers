package main

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/fza/agentic-helpers/source/hooks/internal/capture"
	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/migration"
	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/process"
	"github.com/fza/agentic-helpers/source/hooks/internal/sddcaps"
)

func captureMain() int {
	working, err := os.Getwd()
	if err != nil {
		return 1
	}

	// Every command runs in a process group of its own, out of reach of the
	// terminal's interrupt, so the signal cancels the run and the runner kills
	// the group.
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}

	cacheRoot := os.Getenv("XDG_CACHE_HOME")
	if cacheRoot == "" && home != "" {
		cacheRoot = filepath.Join(home, ".cache")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	migration.Prepare(ctx, migration.Machine(home, os.Getenv), os.Stderr)

	env := capture.Env{
		WorkingDir: working,
		Runner:     process.Runner{},
		Limit:      os.Getenv(capture.LimitVariable),
		Pause:      capture.Sleep,
		Settings:   pluginconfig.Paths(home, runtime.GOOS, os.Getenv),
		Getenv:     os.Getenv,
		SddCache:   sddcaps.CacheDir(cacheRoot),
		LookPath:   exec.LookPath,
		Git:        process.Git{},
		TempRoot:   "/tmp",
	}
	streams := hookio.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}

	return capture.Main(ctx, os.Args[1:], env, streams)
}
