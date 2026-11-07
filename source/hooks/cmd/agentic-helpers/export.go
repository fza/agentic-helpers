package main

import (
	"cmp"
	"context"
	"os"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/export"
	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/migration"
	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/process"
	"github.com/fza/agentic-helpers/source/hooks/internal/project"
	"github.com/fza/agentic-helpers/source/hooks/internal/sessionrecord"
	"github.com/fza/agentic-helpers/source/hooks/internal/uninstall"
)

// exportMain runs `export` and `uninstall`, which migrate nothing: an export
// reads the home as it stands, and refuses while the old layout waits for
// its migration rather than running it.
func exportMain(ctx context.Context, userHome string) int {
	machine := migration.Machine(userHome, os.Getenv)
	exporting := export.Env{
		Roots: machine.Roots, Migration: machine, Producer: export.Self + " " + version, Now: time.Now,
	}
	streams := hookio.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}

	if os.Args[1] == "export" {
		return export.Main(ctx, os.Args[2:], exporting, streams)
	}

	env := uninstall.Env{
		Export: exporting,
		Sessions: sessionrecord.Env{
			Dir: sessionrecord.Dir(machine.Roots.Cache), Getenv: os.Getenv, Processes: process.Table{},
			Checkout: project.GitCommonDir{}, OwnerName: cmp.Or(os.Getenv("AGENTIC_MEMORY_PROCESS_NAME"), "claude"),
			Now: time.Now,
		},
		SessionID: os.Getenv("CLAUDE_CODE_SESSION_ID"),
		UserHome:  userHome,
		Getenv:    os.Getenv,
		ConfigDir: pluginconfig.ConfigDir(userHome, os.Getenv),
		OldTrees:  machine.OldTrees(),
		Git:       process.Git{},
		Claude:    process.Runner{},
		Now:       time.Now,
	}

	return uninstall.Main(ctx, os.Args[2:], env, streams)
}
