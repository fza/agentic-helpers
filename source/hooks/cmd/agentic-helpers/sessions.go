package main

import (
	"cmp"
	"context"
	"os"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/process"
	"github.com/fza/agentic-helpers/source/hooks/internal/project"
	"github.com/fza/agentic-helpers/source/hooks/internal/sessionrecord"
)

// sessionsMain runs `record` and `sessions`, which read no settings and
// migrate nothing: the record hook runs on every event, and the records live
// in a cache no earlier release laid out.
func sessionsMain(ctx context.Context, userHome string) int {
	dir := ""

	if userHome != "" || os.Getenv("XDG_CACHE_HOME") != "" {
		roots := home.Locate(userHome, os.Getenv)
		roots.ExcludeCacheOnCreate()
		dir = sessionrecord.Dir(roots.Cache)
	}

	env := sessionrecord.Env{
		Dir:       dir,
		Getenv:    os.Getenv,
		Processes: process.Table{},
		Checkout:  project.GitCommonDir{},
		OwnerName: cmp.Or(os.Getenv("AGENTIC_MEMORY_PROCESS_NAME"), "claude"),
		Now:       time.Now,
	}
	streams := hookio.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}

	return sessionrecord.Main(ctx, os.Args[1:], env, streams)
}
