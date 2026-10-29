// Command agentic-helpers reports on the plugin as a whole: `check` names the
// features off because a piece they rest on is off, and the command
// substitutions the machine needs.
package main

import (
	"context"
	"os"
	"runtime"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/migration"
	"github.com/fza/agentic-helpers/source/hooks/internal/pieces"
	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
)

func main() {
	userHome, err := os.UserHomeDir()
	if err != nil {
		userHome = ""
	}

	cacheRoot := ""
	if userHome != "" || os.Getenv("XDG_CACHE_HOME") != "" {
		cacheRoot = home.Locate(userHome, os.Getenv).Cache
	}

	ctx := context.Background()
	machine := migration.Machine(userHome, os.Getenv)
	migration.Prepare(ctx, machine, os.Stderr)

	// Claude Code sets CLAUDE_PLUGIN_ROOT for a plugin's hooks and never for a
	// command the Bash tool runs.
	env := pieces.Env{
		Settings:  pluginconfig.Paths(userHome, runtime.GOOS, os.Getenv),
		Getenv:    os.Getenv,
		Hook:      os.Getenv("CLAUDE_PLUGIN_ROOT") != "",
		Migration: machine,
		CacheRoot: cacheRoot,
	}
	streams := hookio.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}

	os.Exit(pieces.Main(ctx, os.Args[1:], env, streams))
}
