// Package draftlint runs a project's `draft_lint` command, named in
// `.sdd/grounding.yaml`, on an entry body written to a file: the one rule
// `agentic-capture` and the grounding gate both apply before an entry is
// written.
package draftlint

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/process"
)

var ErrSilent = errors.New("the draft lint reported nothing")

type Runner interface {
	Run(ctx context.Context, dir string, argv []string) (process.Result, error)
}

// Run runs command from dir on the file, the file's path appended as the last
// word. It returns each line the lint printed on a failing run. A run that
// fails and prints nothing never read the body, and an entry is immutable, so
// that is an error wrapping ErrSilent rather than a pass.
func Run(ctx context.Context, runner Runner, dir string, command string, file string) ([]string, error) {
	result, err := runner.Run(ctx, dir, []string{"sh", "-c", command + ` "$@"`, "sh", file})
	if err != nil {
		return nil, fmt.Errorf("the draft lint did not run: %w", err)
	}

	if result.Code == 0 {
		return nil, nil
	}

	var alerts []string

	for line := range strings.SplitSeq(result.Stdout, "\n") {
		if strings.TrimSpace(line) != "" {
			alerts = append(alerts, line)
		}
	}

	if len(alerts) > 0 {
		return alerts, nil
	}

	stderr := strings.TrimSpace(result.Stderr)
	if len(stderr) > 200 {
		stderr = stderr[:200]
	}

	return nil, &silentError{code: result.Code, stderr: stderr}
}

type silentError struct {
	code   int
	stderr string
}

func (silent *silentError) Error() string {
	return fmt.Sprintf("the draft lint exited %d and reported nothing, so the body went unread: %s", silent.code, silent.stderr)
}

func (silent *silentError) Is(target error) bool {
	return target == ErrSilent
}
