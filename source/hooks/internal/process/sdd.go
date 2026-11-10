package process

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// exportLimit bounds one `sdd export`, so a read never hangs on it.
const exportLimit = 10 * time.Second

type SddExport struct{}

// Export runs `sdd export` for the graph in dir and every connected repo it
// depends on, whole, and returns the document it prints.
func (SddExport) Export(ctx context.Context, dir string) ([]byte, error) {
	bounded, cancel := context.WithTimeout(ctx, exportLimit)
	defer cancel()

	command := exec.CommandContext(bounded, "sdd", "export", "--dependencies", "all")
	command.Dir = dir

	output, err := command.Output()
	if err != nil {
		var said string

		var exited *exec.ExitError
		if errors.As(err, &exited) {
			said = strings.TrimSpace(string(exited.Stderr))
		}

		return nil, fmt.Errorf("%w %s", err, said)
	}

	return output, nil
}

// repoPathLimit bounds one `sdd repo path`, which only reads sdd's settings.
const repoPathLimit = 5 * time.Second

// RepoPath is where sdd keeps its local copy of the connected repo repoID, as
// `sdd repo path` prints it in dir.
func (SddExport) RepoPath(ctx context.Context, dir string, repoID string) (string, error) {
	bounded, cancel := context.WithTimeout(ctx, repoPathLimit)
	defer cancel()

	command := exec.CommandContext(bounded, "sdd", "repo", "path", repoID)
	command.Dir = dir

	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("sdd repo path %s: %w", repoID, err)
	}

	return strings.TrimSpace(string(output)), nil
}

// sddConfigLimit bounds one `sdd config get`, which only reads files.
const sddConfigLimit = 5 * time.Second

// SddConfig reads sdd's configuration as sdd layers it for a directory.
type SddConfig struct{}

// EmbeddingConfig is what `sdd config get embedding` prints in dir, or nothing
// where sdd is missing or fails.
func (config SddConfig) EmbeddingConfig(ctx context.Context, dir string) string {
	return config.get(ctx, dir, "embedding")
}

func (SddConfig) get(ctx context.Context, dir string, key string) string {
	_, err := exec.LookPath("sdd")
	if err != nil {
		return ""
	}

	bounded, cancel := context.WithTimeout(ctx, sddConfigLimit)
	defer cancel()

	command := exec.CommandContext(bounded, "sdd", "config", "get", key)
	command.Dir = dir

	output, err := command.Output()
	if err != nil {
		return ""
	}

	return string(output)
}
