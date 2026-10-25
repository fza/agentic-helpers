// Command agentic-helpers reports on the plugin as a whole: `check` names the
// features off because a piece they rest on is off.
package main

import (
	"context"
	"os"
	"runtime"

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/pieces"
	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
)

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}

	// Claude Code sets CLAUDE_PLUGIN_ROOT for a plugin's hooks and never for a
	// command the Bash tool runs.
	env := pieces.Env{
		Settings: pluginconfig.Paths(home, runtime.GOOS, os.Getenv),
		Getenv:   os.Getenv,
		Hook:     os.Getenv("CLAUDE_PLUGIN_ROOT") != "",
	}
	streams := hookio.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}

	os.Exit(pieces.Main(context.Background(), os.Args[1:], env, streams))
}
