// Command agentic-tools is the library of agents' helper tools: the verbs a
// session runs to find, take, add and update tools, the stdio MCP server
// offering the same, and the Claude Code hooks offering tools and nudging a
// session to register what it wrote.
package main

import (
	"context"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/migration"
	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/process"
	"github.com/fza/agentic-helpers/source/hooks/internal/project"
	"github.com/fza/agentic-helpers/source/hooks/internal/tools"
)

var version = "dev"

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

	userHome, err := os.UserHomeDir()
	if err != nil {
		os.Exit(0)
	}

	owner := os.Getenv("AGENTIC_MEMORY_PROCESS_NAME")
	if owner == "" {
		owner = "claude"
	}

	machine := migration.Machine(userHome, os.Getenv)
	migration.Prepare(ctx, machine, os.Stderr)

	env := tools.Env{
		Base:      base,
		Home:      userHome,
		Roots:     home.Locate(userHome, os.Getenv),
		OwnerName: owner,
		SessionID: os.Getenv("CLAUDE_CODE_SESSION_ID"),
		Settings:  pluginconfig.Paths(userHome, runtime.GOOS, os.Getenv),
		Getenv:    os.Getenv,
		Now:       time.Now,
		Processes: process.Table{},
		Checkout:  project.GitCommonDir{},
		Git:       process.Git{},
		Embedders: func(config embed.Config) (embed.Embedder, error) {
			return embed.New(config, &http.Client{Timeout: 30 * time.Second})
		},
		Version: version,
		Idle:    migration.Idle(machine),
	}
	streams := hookio.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}

	os.Exit(tools.Main(ctx, os.Args[1:], env, streams))
}
