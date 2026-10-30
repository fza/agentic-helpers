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

// sddConfigLimit bounds one `sdd config get`, which only reads files.
const sddConfigLimit = 5 * time.Second

// SddConfig reads sdd's configuration as sdd layers it for a directory.
type SddConfig struct{}

// EmbeddingConfig is what `sdd config get embedding` prints in dir, or nothing
// where sdd is missing or fails.
func (SddConfig) EmbeddingConfig(ctx context.Context, dir string) string {
	_, err := exec.LookPath("sdd")
	if err != nil {
		return ""
	}

	bounded, cancel := context.WithTimeout(ctx, sddConfigLimit)
	defer cancel()

	command := exec.CommandContext(bounded, "sdd", "config", "get", "embedding")
	command.Dir = dir

	output, err := command.Output()
	if err != nil {
		return ""
	}

	return string(output)
}
