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
	LastReply      string          `json:"last_assistant_message"`
	Prompt         string          `json:"prompt"`
	Reason         string          `json:"reason"`
}

type ToolInput struct {
	Command      string          `json:"command"`
	FilePath     string          `json:"file_path"`
	NotebookPath string          `json:"notebook_path"`
	Raw          json.RawMessage `json:"-"`
}

// UnmarshalJSON keeps the whole input beside the fields every tool shares,
// since an MCP tool's arguments are its own.
func (input *ToolInput) UnmarshalJSON(data []byte) error {
	type plain ToolInput

	var decoded plain

	err := json.Unmarshal(data, &decoded)
	if err != nil {
		return fmt.Errorf("reading the tool input: %w", err)
	}

	*input = ToolInput(decoded)
	input.Raw = append(json.RawMessage(nil), data...)

	return nil
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

type hookOutput struct {
	HookSpecificOutput hookSpecificOutput `json:"hookSpecificOutput"`
}

type hookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

// AddContext answers an event with a note for the agent, which Claude Code
// hands over without showing it to the user.
func AddContext(out io.Writer, event string, note string) error {
	err := json.NewEncoder(out).Encode(hookOutput{HookSpecificOutput: hookSpecificOutput{
		HookEventName: event, AdditionalContext: note,
	}})
	if err != nil {
		return fmt.Errorf("writing the hook's context: %w", err)
	}

	return nil
}
