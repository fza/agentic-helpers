// Command agentic-install wires the hook binaries beside it into Claude Code's
// settings and links this checkout's skills, or takes both back out.
package main

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/install"
)

func main() {
	working, err := os.Getwd()
	if err != nil {
		os.Exit(1)
	}

	self, err := os.Executable()
	if err != nil {
		os.Exit(1)
	}

	config := os.Getenv("CLAUDE_CONFIG_DIR")
	if config == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			os.Exit(1)
		}

		config = filepath.Join(home, ".claude")
	}

	resolved, err := filepath.EvalSymlinks(self)
	if err == nil {
		self = resolved
	}

	env := install.Env{ConfigDir: config, BinDir: filepath.Dir(self), WorkingDir: working, Now: time.Now}
	streams := hookio.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}

	os.Exit(install.Main(context.Background(), os.Args[1:], env, streams))
}
