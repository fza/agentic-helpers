package process

import (
	"context"
	"fmt"
	"os/exec"
)

type Git struct{}

// Output runs git in dir and returns its stdout. A non-zero exit is an error:
// every caller reads one value and has a fallback for its absence.
func (Git) Output(ctx context.Context, dir string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)

	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("running git %v in %s: %w", args, dir, err)
	}

	return string(output), nil
}
