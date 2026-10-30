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

// answerFields are the reads' JSON objects; every other tool answers its
// message alone.
var answerFields = map[string]any{
	"show": jsonShow{}, "search": jsonSearch{}, "brief": jsonBrief{}, "who": jsonWho{}, "topics": jsonTopics{},
	"view": jsonView{},
}

// AnswerSchema is the shape a tool's answer keeps, derived from the types the
// answer is built from, so a test holds every MCP answer to it although the
// server lists no outputSchema.
func AnswerSchema(name string) json.RawMessage {
	if slices.Contains(jsonReads, name) {
		return mcpserver.SchemaOf(answerFields[name])
	}

	return mcpserver.MessageSchema(answerFields[name])
}
