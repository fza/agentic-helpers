// Package hookio carries what Claude Code hands a hook: the JSON payload on
// stdin, and the streams a hook answers on.
package hookio

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type Streams struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

type Payload struct {
	SessionID      string          `json:"session_id"`
	AgentID        string          `json:"agent_id"`
	Source         string          `json:"source"`
	TranscriptPath string          `json:"transcript_path"`
	ToolName       string          `json:"tool_name"`
	ToolInput      ToolInput       `json:"tool_input"`
	ToolResponse   json.RawMessage `json:"tool_response"`
}

type ToolInput struct {
	Command  string `json:"command"`
	FilePath string `json:"file_path"`
}

type ToolResponse struct {
	IsError     bool `json:"is_error"`
	Interrupted bool `json:"interrupted"`
	ExitCode    *int `json:"exit_code"`
}

func ReadPayload(in io.Reader) (Payload, error) {
	var payload Payload

	err := json.NewDecoder(in).Decode(&payload)
	if err != nil {
		return Payload{}, fmt.Errorf("reading the hook payload: %w", err)
	}

	payload.SessionID = strings.TrimSpace(payload.SessionID)

	return payload, nil
}

// Failed reports whether the tool call came back refused. A missing or
// unreadable response is no evidence of failure: nothing guarantees the field.
func (payload Payload) Failed() bool {
	var response ToolResponse

	err := json.Unmarshal(payload.ToolResponse, &response)
	if err != nil {
		return false
	}

	return response.IsError || response.Interrupted || (response.ExitCode != nil && *response.ExitCode != 0)
}
