package tools

import "github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"

// Read limits. Every store is untrusted text, so no read is unbounded.
const (
	toolLimit     = pluginconfig.ToolMaxMB << 20
	versionLimit  = toolLimit + 64<<10
	indexLimit    = 4 << 20
	lineFileLimit = 4 << 20
	sessionLimit  = 1 << 20
	currentLimit  = 64
)
