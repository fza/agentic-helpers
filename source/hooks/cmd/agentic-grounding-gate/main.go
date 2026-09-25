// Command agentic-grounding-gate is the Claude Code hook holding a session to
// real reads of a project's sdd decision graph before it asks, drafts or captures.
package main

import (
	"context"
	"os"

	"github.com/fza/agentic-helpers/source/hooks/internal/grounding"
	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
)

func main() {
	working, err := os.Getwd()
	if err != nil {
		os.Exit(0)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}

	env := grounding.Env{
		ProjectDir: os.Getenv("CLAUDE_PROJECT_DIR"),
		WorkingDir: working,
		Home:       home,
	}
	streams := hookio.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}

	os.Exit(grounding.Main(context.Background(), os.Args[1:], env, streams))
}
