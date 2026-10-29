package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
)

var ErrNotARepository = errors.New("not a git repository")

type Git struct{}

// Output runs git in dir and returns its stdout. A non-zero exit is an error:
// every caller reads one value and has a fallback for its absence. Git runs
// under the C locale, so its refusal outside a repository reads the same on
// every machine and wraps ErrNotARepository.
func (Git) Output(ctx context.Context, dir string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	command.Env = append(os.Environ(), "LC_ALL=C")

	output, err := command.Output()
	if err != nil && outsideRepository(err) {
		return "", fmt.Errorf("running git %v in %s: %w", args, dir, ErrNotARepository)
	}

	if err != nil {
		return "", fmt.Errorf("running git %v in %s: %w", args, dir, err)
	}

	return string(output), nil
}

func outsideRepository(err error) bool {
	var exited *exec.ExitError

	return errors.As(err, &exited) && bytes.Contains(exited.Stderr, []byte("not a git repository"))
}
