package embed

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/span"
)

// Sdd answers what `sdd config get embedding` prints in dir, or nothing where
// sdd is missing or fails.
type Sdd interface {
	EmbeddingConfig(ctx context.Context, dir string) string
}

// ErrProjectEndpoint refuses sdd's embedder where its endpoint comes from the
// repository's committed `.sdd/config.yaml`: a clone would choose where every
// memory entry and prompt is sent.
var ErrProjectEndpoint = errors.New("sdd's embedding endpoint is set in this repository's .sdd/config.yaml")

// projectLayer is the layer sdd names the repository's committed
// `.sdd/config.yaml`.
const projectLayer = "project"

// sddLine is one line of `sdd config get embedding`: the key below
// `embedding.`, its value, then the layer it comes from in parentheses.
var sddLine = regexp.MustCompile(`^embedding\.(\S+)\s+(.*?)\s+\(([^()]*)\)$`)

// parseSdd is sdd's embedding block by key, and the layer each key comes
// from.
func parseSdd(text string) (map[string]string, map[string]string) {
	block, layers := map[string]string{}, map[string]string{}

	for _, line := range strings.Split(text, "\n") {
		parts := sddLine.FindStringSubmatch(strings.TrimSpace(line))
		if parts == nil {
			continue
		}

		block[parts[1]], layers[parts[1]] = parts[2], parts[3]
	}

	return block, layers
}

// Settle is own, or where fromSdd says so, sdd's whole embedding block read
// in dir. Under sdd's block no API key is sent: sdd prints its keys masked,
// and its configuration is a file any agent can write. An empty Provider
// means no embedder, the error saying why where sdd sets one it refuses.
func Settle(ctx context.Context, own Config, fromSdd bool, sdd Sdd, dir string) (Config, error) {
	if !fromSdd {
		return own, nil
	}

	if sdd == nil {
		return Config{}, nil
	}

	block, layers := parseSdd(sdd.EmbeddingConfig(ctx, dir))
	if layers["endpoint"] == projectLayer {
		return Config{}, ErrProjectEndpoint
	}

	config := Config{
		Provider:         block["provider"],
		Model:            block["model"],
		Endpoint:         block["endpoint"],
		QueryTemplate:    block["query_template"],
		DocumentTemplate: block["document_template"],
		Timeout:          own.Timeout,
	}

	config.Dimensions, _ = strconv.Atoi(block["dimensions"])

	timeout, err := span.Parse(block["timeout"])
	if err == nil && timeout > 0 {
		config.Timeout = timeout
	}

	if !slices.Contains([]string{"openai", "ollama"}, config.Provider) {
		return Config{}, nil
	}

	return config, nil
}
