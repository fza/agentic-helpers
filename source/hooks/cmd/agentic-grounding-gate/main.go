// Command agentic-grounding-gate is the Claude Code hook holding a session to
// real reads of a project's sdd decision graph before it asks, drafts or captures.
package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/fza/agentic-helpers/source/hooks/internal/grounding"
	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/process"
	"github.com/fza/agentic-helpers/source/hooks/internal/project"
	"github.com/fza/agentic-helpers/source/hooks/internal/sddcaps"
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

	cacheRoot := os.Getenv("XDG_CACHE_HOME")
	if cacheRoot == "" && home != "" {
		cacheRoot = filepath.Join(home, ".cache")
	}

	owner := os.Getenv("AGENTIC_MEMORY_PROCESS_NAME")
	if owner == "" {
		owner = "claude"
	}

	env := grounding.Env{
		ProjectDir: os.Getenv("CLAUDE_PROJECT_DIR"),
		WorkingDir: working,
		Home:       home,
		Checkout:   project.GitCommonDir{},
		Runner:     process.Runner{},
		Processes:  process.Table{},
		OwnerName:  owner,
		Settings:   pluginconfig.Paths(home, runtime.GOOS, os.Getenv),
		Getenv:     os.Getenv,
		SddCache:   sddcaps.CacheDir(cacheRoot),
		LookPath:   exec.LookPath,
	}
	streams := hookio.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}

	os.Exit(grounding.Main(context.Background(), os.Args[1:], env, streams))
}
