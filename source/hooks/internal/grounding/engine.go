package grounding

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// servesEngine reports whether the project's `.mcp.json` declares sdd's MCP
// server under the name its tools carry, `mcp__sdd__*`. A server configured at
// user or local scope lives outside the project and goes unseen.
func (env Env) servesEngine() bool {
	raw, err := os.ReadFile(filepath.Join(env.project(), ".mcp.json"))
	if err != nil {
		return false
	}

	var declared struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}

	err = json.Unmarshal(raw, &declared)
	if err != nil {
		return false
	}

	_, found := declared.MCPServers["sdd"]

	return found
}
