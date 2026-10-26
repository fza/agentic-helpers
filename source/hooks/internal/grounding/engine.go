package grounding

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/fza/agentic-helpers/source/hooks/internal/sddcaps"
)

// servesEngine reports whether the project's `.mcp.json` declares sdd's MCP
// server under the name its tools carry, `mcp__sdd__*`. A server configured at
// user or local scope lives outside the project and goes unseen. Where the
// project names its graph repository through `project_dir`, an sdd without
// `init --project-dir` ignores the key, and its server would serve the code
// repository's own `.sdd/`, so the engine counts only where sdd follows it.
func (env Env) servesEngine(ctx context.Context) bool {
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
	if !found || !env.graphProject().Split {
		return found
	}

	return sddcaps.Probe{Runner: env.Runner, CacheDir: env.SddCache, LookPath: env.LookPath}.Of(ctx, env.project()).ProjectDir
}
