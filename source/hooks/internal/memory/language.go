package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/fza/agentic-helpers/source/hooks/internal/project"
)

const defaultLanguage = "en"

// language is the locale entries are written in: the graph's own, which sdd
// authors its entries in, else the plugin's memory_language, else English.
// The conversation's language never decides it.
func (mem *memory) language(ctx context.Context) string {
	root := project.Root(ctx, mem.env.Base, mem.env.Checkout)

	raw, err := os.ReadFile(filepath.Join(root, ".sdd", "config.yaml"))
	if err == nil {
		var graph struct {
			Language string `yaml:"language"`
		}

		err = yaml.Unmarshal(raw, &graph)
		if err == nil && strings.TrimSpace(graph.Language) != "" {
			return strings.TrimSpace(graph.Language)
		}
	}

	configured := strings.TrimSpace(mem.options().MemoryLanguage)
	if configured != "" {
		return configured
	}

	return defaultLanguage
}
