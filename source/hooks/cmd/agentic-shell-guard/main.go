// Command agentic-shell-guard is the Claude Code PreToolUse hook on Bash that
// holds an agent's removals to the project and /tmp and bans mktemp, and, as
// `agentic-shell-guard permission`, the PermissionRequest hook that ends its
// asks in auto and bypassPermissions mode, and, as `agentic-shell-guard output`,
// the PostToolUse hook on every tool that withholds each output line from
// outside the project looking like a credential.
package main

import (
	"context"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/migration"
	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/shellguard"
)

func main() {
	userHome, err := os.UserHomeDir()
	if err != nil {
		userHome = ""
	}

	stateDir := ""
	if userHome != "" || os.Getenv("XDG_CACHE_HOME") != "" {
		stateDir = home.Locate(userHome, os.Getenv).Cache
	}

	env := shellguard.Env{
		ProjectDir: os.Getenv("CLAUDE_PROJECT_DIR"), Home: userHome, Tmp: "/tmp",
		Settings: pluginconfig.Paths(userHome, runtime.GOOS, os.Getenv), Getenv: os.Getenv,
		StateDir: stateDir, Clock: shellguard.SystemClock{},
	}
	streams := shellguard.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	migration.Prepare(ctx, migration.Machine(userHome, os.Getenv), os.Stderr)

	code := shellguard.Run(ctx, os.Args[1:], env, streams)

	stop()
	os.Exit(code)
}
