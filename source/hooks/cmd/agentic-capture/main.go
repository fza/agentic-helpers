// Command agentic-capture turns a draft file into a captured sdd graph entry:
// the header's fields become `sdd new` flags, the body passes as one argument,
// the project's own lint and the tool's pre-flight run first.
package main

import (
	"context"
	"os"

	"github.com/fza/agentic-helpers/source/hooks/internal/capture"
	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/process"
)

func main() {
	working, err := os.Getwd()
	if err != nil {
		os.Exit(1)
	}

	env := capture.Env{WorkingDir: working, Runner: process.Runner{}}
	streams := hookio.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}

	os.Exit(capture.Main(context.Background(), os.Args[1:], env, streams))
}
