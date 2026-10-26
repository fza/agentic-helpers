// Command agentic-memory is the agent memory store: the subcommands a session
// runs to claim a seat and file, search and retire entries, the stdio MCP
// server offering the same as tools, and the Claude Code hooks replaying what
// a fresh context needs.
package main

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/memory"
	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/process"
	"github.com/fza/agentic-helpers/source/hooks/internal/project"
	"github.com/fza/agentic-helpers/source/hooks/internal/sddcaps"
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

	window, err := strconv.Atoi(os.Getenv("CLAUDE_CODE_AUTO_COMPACT_WINDOW"))
	if err != nil {
		window = 0
	}

	env := memory.Env{
		Base:          base,
		Home:          home,
		CacheRoot:     cacheRoot,
		OwnerName:     owner,
		SessionID:     os.Getenv("CLAUDE_CODE_SESSION_ID"),
		CompactWindow: window,
		Settings:      pluginconfig.Paths(home, runtime.GOOS, os.Getenv),
		Getenv:        os.Getenv,
		Now:           time.Now,
		Processes:     process.Table{},
		Checkout:      project.GitCommonDir{},
		Git:           process.Git{},
		Graph:         process.SddExport{},
		Sdd:           &sddcaps.Probe{Runner: process.Runner{}, CacheDir: sddcaps.CacheDir(cacheRoot), LookPath: exec.LookPath},
		Embedders: func(config embed.Config) (embed.Embedder, error) {
			return embed.New(config, &http.Client{Timeout: 30 * time.Second})
		},
		Version: version,
	}
	streams := hookio.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}

	os.Exit(memory.Main(ctx, os.Args[1:], env, streams))
}
