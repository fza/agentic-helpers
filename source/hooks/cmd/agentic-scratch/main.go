// Command agentic-scratch is the Claude Code hook sweeping a project's
// `.tmp/claude/<session-id>/` directories: `end` removes the ending session's
// own, and `start` reaps abandoned ones.
package main

import (
	"context"
	"os"
	"runtime"

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/project"
	"github.com/fza/agentic-helpers/source/hooks/internal/scratch"
)

func main() {
	ctx := context.Background()

	base := os.Getenv("CLAUDE_PROJECT_DIR")
	if base == "" {
		working, err := os.Getwd()
		if err != nil {
			os.Exit(0)
		}

		base = working
	}

	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}

	env := scratch.Env{
		Root:     project.Root(ctx, base, project.GitCommonDir{}),
		Settings: pluginconfig.Paths(home, runtime.GOOS, os.Getenv),
		Getenv:   os.Getenv,
	}
	streams := hookio.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}

	os.Exit(scratch.Main(ctx, os.Args[1:], env, streams))
}
