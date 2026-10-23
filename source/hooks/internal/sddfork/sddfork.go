// Package sddfork tells whether the sdd on the PATH is the build agentic-helpers
// works with: the fork at github.com/fza/sdd, whose every version, release or
// local, carries "fza" and which upstream versions never do. The number after it
// grows with every build and is never parsed.
package sddfork

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/process"
)

const (
	Source  = "github.com/fza/sdd"
	Install = "brew install fza/tap/sdd"
)

const limit = 5 * time.Second

type Runner interface {
	Run(ctx context.Context, dir string, argv []string) (process.Result, error)
}

// Check runs `sdd --version` in dir and returns what it printed, for the
// message naming it, and whether that is the fork.
func Check(ctx context.Context, runner Runner, dir string) (string, bool) {
	bounded, cancel := context.WithTimeout(ctx, limit)
	defer cancel()

	result, err := runner.Run(bounded, dir, []string{"sdd", "--version"})
	if err != nil {
		return fmt.Sprintf("nothing (%v)", err), false
	}

	printed := strings.TrimSpace(result.Stdout + result.Stderr)
	if result.Code != 0 {
		return fmt.Sprintf("%q and exited %d", printed, result.Code), false
	}

	version, found := strings.CutPrefix(printed, "sdd version ")

	return fmt.Sprintf("%q", printed), found && strings.Contains(version, "fza")
}
