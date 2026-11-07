package export

import (
	"context"
	"fmt"

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
)

const usage = "usage: " + Self + " export [--force] <file>"

// Main runs `agentic-helpers export [--force] <file>`: it writes the archive,
// prints where and what it holds, and exits 1 on any refusal, having written
// nothing at the target.
func Main(ctx context.Context, args []string, env Env, streams hookio.Streams) int {
	force := false
	target := ""

	for _, arg := range args {
		switch {
		case arg == "--force":
			force = true
		case target == "" && arg != "" && arg[0] != '-':
			target = arg
		default:
			_, _ = fmt.Fprintln(streams.Err, usage)

			return 1
		}
	}

	if target == "" {
		_, _ = fmt.Fprintln(streams.Err, usage)

		return 1
	}

	result, err := Write(ctx, env, target, force)
	if err != nil {
		_, _ = fmt.Fprintf(streams.Err, "%s export: %v; nothing written\n", Self, err)

		return 1
	}

	for _, line := range Summary(result) {
		_, _ = fmt.Fprintln(streams.Out, line)
	}

	return 0
}
