package main

import (
	"cmp"
	"context"
	"os"
	"runtime"

	"github.com/fza/agentic-helpers/source/hooks/internal/bound"
	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/migration"
	"github.com/fza/agentic-helpers/source/hooks/internal/pieces"
	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/process"
	"github.com/fza/agentic-helpers/source/hooks/internal/tools"
)

func helpersMain() int {
	userHome, err := os.UserHomeDir()
	if err != nil {
		userHome = ""
	}

	cacheRoot := ""
	if userHome != "" || os.Getenv("XDG_CACHE_HOME") != "" {
		cacheRoot = home.Locate(userHome, os.Getenv).Cache
	}

	ctx := context.Background()

	// A bounded run reads no settings and migrates nothing, so it starts at
	// once and its output is the command's alone.
	if len(os.Args) > 1 && os.Args[1] == "bound" {
		return bound.Main(ctx, os.Args[2:], hookio.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr})
	}

	if len(os.Args) > 1 && (os.Args[1] == "record" || os.Args[1] == "sessions") {
		return sessionsMain(ctx, userHome)
	}

	machine := migration.Machine(userHome, os.Getenv)
	migration.Prepare(ctx, machine, os.Stderr)

	// Claude Code sets CLAUDE_PLUGIN_ROOT for a plugin's hooks and never for a
	// command the Bash tool runs.
	env := pieces.Env{
		Settings:         pluginconfig.Paths(userHome, runtime.GOOS, os.Getenv),
		Getenv:           os.Getenv,
		Hook:             os.Getenv("CLAUDE_PLUGIN_ROOT") != "",
		Migration:        machine,
		CacheRoot:        cacheRoot,
		SddConfig:        process.SddConfig{},
		Dir:              cmp.Or(os.Getenv("CLAUDE_PROJECT_DIR"), workingDir()),
		ToolPrune:        tools.Prune,
		InstalledPlugins: pieces.InstalledPlugins(pluginconfig.ConfigDir(userHome, os.Getenv)),
	}
	streams := hookio.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}

	return pieces.Main(ctx, os.Args[1:], env, streams)
}

func workingDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}

	return dir
}
