// Command agentic-shell-guard is the Claude Code PreToolUse hook on Bash that
// holds an agent's removals to the project and /tmp and bans mktemp, and, as
// `agentic-shell-guard permission`, the PermissionRequest hook that ends its
// asks in auto and bypassPermissions mode.
package main

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/shellguard"
)

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}

	cacheRoot := os.Getenv("XDG_CACHE_HOME")
	if cacheRoot == "" && home != "" {
		cacheRoot = filepath.Join(home, ".cache")
	}

	stateDir := ""
	if cacheRoot != "" {
		stateDir = filepath.Join(cacheRoot, "agentic-helpers")
	}

	env := shellguard.Env{
		ProjectDir: os.Getenv("CLAUDE_PROJECT_DIR"), Home: home, Tmp: "/tmp",
		Settings: pluginconfig.Paths(home, runtime.GOOS, os.Getenv), Getenv: os.Getenv,
		StateDir: stateDir, Clock: shellguard.SystemClock{},
	}
	streams := shellguard.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	code := shellguard.Run(ctx, os.Args[1:], env, streams)

	stop()
	os.Exit(code)
}
