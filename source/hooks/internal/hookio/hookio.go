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
	ToolUseID      string          `json:"tool_use_id"`
	ToolInput      ToolInput       `json:"tool_input"`
	ToolResponse   json.RawMessage `json:"tool_response"`
	LastReply      string          `json:"last_assistant_message"`
	Prompt         string          `json:"prompt"`
	Reason         string          `json:"reason"`
	// FilePath is the instruction file an InstructionsLoaded event reports;
	// a tool's own path is ToolInput.FilePath.
	FilePath string `json:"file_path"`
	// AgentTranscriptPath is the subagent's own transcript, which a
	// SubagentStop event names.
	AgentTranscriptPath string `json:"agent_transcript_path"`
}

type ToolInput struct {
	Command      string `json:"command"`
	Prompt       string `json:"prompt"`
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
	// SubagentType is the Agent call's agent type, `fork` for a fork of the
	// calling context.
	SubagentType string          `json:"subagent_type"`
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

type preToolOutput struct {
	HookSpecificOutput preToolAnswer `json:"hookSpecificOutput"`
}

// preToolAnswer never carries an allow: an allow sent with a rewrite skips
// the user's permission prompt, and a rewrite alone goes through it as the
// call Claude sent would.
type preToolAnswer struct {
	HookEventName            string          `json:"hookEventName"`
	PermissionDecision       string          `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string          `json:"permissionDecisionReason,omitempty"`
	UpdatedInput             json.RawMessage `json:"updatedInput,omitempty"`
}

// Rewrite answers a PreToolUse event with the tool input to run in place of
// the one Claude sent, leaving the permission decision where it stands.
func Rewrite(out io.Writer, input json.RawMessage) error {
	err := json.NewEncoder(out).Encode(preToolOutput{HookSpecificOutput: preToolAnswer{
		HookEventName: "PreToolUse", UpdatedInput: input,
	}})
	if err != nil {
		return fmt.Errorf("writing the rewritten input: %w", err)
	}

	return nil
}

// Deny answers a PreToolUse event refusing the call, the reason shown to
// Claude.
func Deny(out io.Writer, reason string) error {
	err := json.NewEncoder(out).Encode(preToolOutput{HookSpecificOutput: preToolAnswer{
		HookEventName: "PreToolUse", PermissionDecision: "deny", PermissionDecisionReason: reason,
	}})
	if err != nil {
		return fmt.Errorf("writing the refusal: %w", err)
	}

	return nil
}

// WithField is the tool input with one top-level key set to value, every other
// key kept as Claude sent it: an updated input replaces the whole input.
func (input ToolInput) WithField(key string, value string) (json.RawMessage, error) {
	fields := map[string]json.RawMessage{}

	if len(input.Raw) > 0 {
		err := json.Unmarshal(input.Raw, &fields)
		if err != nil {
			return nil, fmt.Errorf("reading the tool input: %w", err)
		}
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encoding %s: %w", key, err)
	}

	fields[key] = encoded

	updated, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("encoding the tool input: %w", err)
	}

	return updated, nil
}
