package memory

import (
	"encoding/json"
	"slices"

	"github.com/fza/agentic-helpers/source/hooks/internal/mcpserver"
)

type jsonShow struct {
	Notice  string      `json:"notice"`
	Entries []jsonEntry `json:"entries"`
}

type jsonWho struct {
	Seats []jsonSeat `json:"seats"`
}

type jsonTopics struct {
	Topics map[string]map[string]int `json:"topics"`
}

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

// answerFields are the answers per tool: a read's JSON object, or the fields
// of a message answer. A tool missing here answers its message alone.
var answerFields = map[string]any{
	"show": jsonShow{}, "search": jsonSearch{}, "brief": jsonBrief{}, "who": jsonWho{}, "topics": jsonTopics{},
	"view": jsonView{}, "new": filedFields{}, "confirm": idsFields{}, "ack": ackFields{}, "state": seatFields{},
	"retire": idsFields{}, "supersede": supersededFields{}, "retitle": retitledFields{}, "tag": topicsFields{},
	"untag": topicsFields{}, "link": linkFields{}, "claim": seatFields{}, "release": releasedFields{},
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
