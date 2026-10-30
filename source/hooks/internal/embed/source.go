package embed

import (
	"context"
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

// sddLine is one line of `sdd config get embedding`: the key below
// `embedding.`, its value, then the layer it comes from in parentheses.
var sddLine = regexp.MustCompile(`^embedding\.(\S+)\s+(.*?)\s+\([^()]*\)$`)

// parseSdd is sdd's embedding block by key.
func parseSdd(text string) map[string]string {
	block := map[string]string{}

	for _, line := range strings.Split(text, "\n") {
		parts := sddLine.FindStringSubmatch(strings.TrimSpace(line))
		if parts == nil {
			continue
		}

		block[parts[1]] = parts[2]
	}

	return block
}

// Settle is own, or where fromSdd says so, sdd's whole embedding block read
// in dir. Under sdd's block no API key is sent: sdd prints its keys masked,
// and its configuration is a file any agent can write. An empty Provider
// means no embedder.
func Settle(ctx context.Context, own Config, fromSdd bool, sdd Sdd, dir string) Config {
	if !fromSdd {
		return own
	}

	if sdd == nil {
		return Config{}
	}

	block := parseSdd(sdd.EmbeddingConfig(ctx, dir))
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
		return Config{}
	}

	return config
}
