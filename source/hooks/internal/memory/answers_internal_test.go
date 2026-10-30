package memory

import (
	"encoding/json"
	"slices"

	"github.com/fza/agentic-helpers/source/hooks/internal/mcpserver"
)

// jsonView is view's answer: the notice and its sections, each a list, a
// count of topics or groups of entries, or, for `explain`, the message alone.
type jsonView struct {
	Notice   string                     `json:"notice,omitempty"`
	Sections map[string]jsonViewSection `json:"sections,omitempty"`
	Message  string                     `json:"message,omitempty"`
}

type jsonViewSection struct {
	Entries []jsonEntry               `json:"entries,omitempty"`
	More    *jsonMore                 `json:"more,omitempty"`
	Topics  map[string]jsonTopicCount `json:"topics,omitempty"`
	Groups  map[string]jsonGroup      `json:"groups,omitempty"`
}

// answerFields are the reads' --json objects.
var answerFields = map[string]any{
	"show": jsonShow{}, "search": jsonSearch{}, "brief": jsonBrief{}, "who": jsonWho{}, "topics": jsonTopics{},
	"view": jsonView{},
}

// JSONSchema is the shape a read's --json answer keeps, derived from the
// types the answer is built from.
func JSONSchema(name string) json.RawMessage {
	return mcpserver.SchemaOf(answerFields[name])
}

// AnswerSchema is the shape a tool's MCP answer keeps, so a test holds every
// MCP answer to it although the server lists no outputSchema: a read's --json
// object where MCP answers it, the message alone everywhere else.
func AnswerSchema(name string) json.RawMessage {
	if slices.Contains(mcpJSONReads, name) {
		return JSONSchema(name)
	}

	return mcpserver.MessageSchema(nil)
}
