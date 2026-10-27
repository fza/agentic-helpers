package memory

import (
	"context"
	"strings"
)

const defaultLanguage = "en"

// language is the locale entries are written in: the graph's own, which sdd
// authors its entries in, else the plugin's memory_language, else English.
// The conversation's language never decides it.
func (mem *memory) language(ctx context.Context) string {
	graph := strings.TrimSpace(mem.graphProject(ctx).language)
	if graph != "" {
		return graph
	}

	configured := strings.TrimSpace(mem.options().MemoryLanguage)
	if configured != "" {
		return configured
	}

	return defaultLanguage
}
