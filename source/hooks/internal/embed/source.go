package embed

import (
	"context"
	"errors"
	"fmt"
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

// ErrRepositoryEndpoint refuses sdd's embedder where its endpoint comes from a
// layer inside the repository: a clone would choose where every memory entry
// and prompt is sent.
var ErrRepositoryEndpoint = errors.New("sdd's embedding endpoint is set in this repository's .sdd/")

// trustedLayers are sdd's layers living outside any repository: the user's
// global configuration and sdd's built-in defaults. Every other layer, the
// committed `.sdd/config.yaml`, the `.sdd/config.local.yaml` a repository can
// commit as well, and any layer a later sdd adds, is the repository's.
var trustedLayers = []string{"global", "default"}

// sddLine is one line of `sdd config get embedding`: the key below
// `embedding.`, its value, then the layer it comes from in parentheses.
var sddLine = regexp.MustCompile(`^embedding\.(\S+)\s+(.*?)\s+\(([^()]*)\)$`)

// parseSdd is sdd's embedding block by key, and the layer each key comes
// from; nothing where a key is printed twice, which sdd never does.
func parseSdd(text string) (map[string]string, map[string]string) {
	block, layers := map[string]string{}, map[string]string{}

	for _, line := range strings.Split(text, "\n") {
		parts := sddLine.FindStringSubmatch(strings.TrimSpace(line))
		if parts == nil {
			continue
		}

		_, twice := block[parts[1]]
		if twice {
			return nil, nil
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

	layer, set := layers["endpoint"]
	if set && !slices.Contains(trustedLayers, layer) {
		return Config{}, fmt.Errorf("%w (layer %s)", ErrRepositoryEndpoint, layer)
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
