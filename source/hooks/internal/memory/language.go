package memory

import (
	"context"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/graphconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/project"
)

const defaultLanguage = "en"

// language is the locale entries are written in: the graph's own, which sdd
// authors its entries in, else the plugin's memory_language, else English.
// The conversation's language never decides it.
func (mem *memory) language(ctx context.Context) string {
	root := project.Root(ctx, mem.env.Base, mem.env.Checkout)

	graph := strings.TrimSpace(graphconfig.ResolveAt(root).Language())
	if graph != "" {
		return graph
	}

	configured := strings.TrimSpace(mem.options().MemoryLanguage)
	if configured != "" {
		return configured
	}

	return defaultLanguage
}
