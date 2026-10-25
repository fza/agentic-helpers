// Command agentic-shell-guard is the Claude Code PreToolUse hook on Bash that
// holds an agent's removals to the project and /tmp and bans mktemp.
package main

import (
	"context"
	"os"
	"runtime"

	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/shellguard"
)

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}

	env := shellguard.Env{
		ProjectDir: os.Getenv("CLAUDE_PROJECT_DIR"), Home: home, Tmp: "/tmp",
		Settings: pluginconfig.Paths(home, runtime.GOOS),
	}
	streams := shellguard.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}

	os.Exit(shellguard.Main(context.Background(), env, streams))
}
