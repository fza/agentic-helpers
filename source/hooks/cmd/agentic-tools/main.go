// Command agentic-tools is the library of agents' helper tools: the verbs a
// session runs to find, take, add and update tools, the stdio MCP server
// offering the same, and the Claude Code hooks offering tools and nudging a
// session to register what it wrote.
package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
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

	home, err := os.UserHomeDir()
	if err != nil {
		os.Exit(0)
	}

	cacheRoot := os.Getenv("XDG_CACHE_HOME")
	if cacheRoot == "" {
		cacheRoot = filepath.Join(home, ".cache")
	}

	owner := os.Getenv("AGENTIC_MEMORY_PROCESS_NAME")
	if owner == "" {
		owner = "claude"
	}

	env := tools.Env{
		Base:      base,
		Home:      home,
		CacheRoot: cacheRoot,
		OwnerName: owner,
		SessionID: os.Getenv("CLAUDE_CODE_SESSION_ID"),
		Settings:  pluginconfig.Paths(home, runtime.GOOS),
		Getenv:    os.Getenv,
		Now:       time.Now,
		Processes: process.Table{},
		Checkout:  project.GitCommonDir{},
		Git:       process.Git{},
		Embedders: func(config embed.Config) (embed.Embedder, error) {
			return embed.New(config, &http.Client{Timeout: 30 * time.Second})
		},
		Version: version,
	}
	streams := hookio.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}

	os.Exit(tools.Main(ctx, os.Args[1:], env, streams))
}
