package transcript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// agentIDShape is what an agent id may hold before it names a file.
var agentIDShape = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// AgentIDShaped is whether an agent id may name a file.
func AgentIDShaped(agentID string) bool {
	return agentIDShape.MatchString(agentID)
}

// OfSubagent is a subagent's own transcript: own, the path a SubagentStop
// names, else the one beside its session's transcript where Claude Code
// writes it, `<session>/subagents/agent-<id>.jsonl`.
func OfSubagent(session string, own string, agentID string) string {
	if own != "" {
		return own
	}

	if session == "" || !agentIDShape.MatchString(agentID) {
		return ""
	}

	return filepath.Join(strings.TrimSuffix(session, filepath.Ext(session)), "subagents", "agent-"+agentID+".jsonl")
}

// SpawnCall is the id of the Agent call that spawned the subagent whose
// transcript is at path, which Claude Code writes into the
// `agent-<id>.meta.json` beside it; empty where none is written.
func SpawnCall(path string) string {
	if path == "" {
		return ""
	}

	data, err := os.ReadFile(strings.TrimSuffix(path, filepath.Ext(path)) + ".meta.json")
	if err != nil {
		return ""
	}

	var meta struct {
		ToolUseID string `json:"toolUseId"`
	}

	err = json.Unmarshal(data, &meta)
	if err != nil {
		return ""
	}

	return meta.ToolUseID
}
