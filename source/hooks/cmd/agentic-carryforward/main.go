// Command agentic-carryforward is the Claude Code hook keeping each seat's
// carry-forward file current across a compaction, and the command a session
// runs to claim, release or refresh its seat.
package main

import (
	"context"
	"os"
	"strconv"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/carryforward"
	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/process"
	"github.com/fza/agentic-helpers/source/hooks/internal/project"
	"github.com/fza/agentic-helpers/source/hooks/internal/sddap"
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

	owner := os.Getenv("CARRYFORWARD_PROCESS_NAME")
	if owner == "" {
		owner = "claude"
	}

	window, err := strconv.Atoi(os.Getenv("CLAUDE_CODE_AUTO_COMPACT_WINDOW"))
	if err != nil {
		window = 0
	}

	root := project.Root(ctx, base, project.GitCommonDir{})
	env := carryforward.Env{
		Root:          root,
		Home:          home,
		OwnerName:     owner,
		CompactWindow: window,
		Now:           time.Now,
		Processes:     process.Table{},
		Seats:         sddap.Seats{Dir: root},
	}
	streams := hookio.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}

	os.Exit(carryforward.Main(ctx, os.Args[1:], env, streams))
}
