package tools

// What each MCP tool answers beside its message, or, for find and list, the
// listing they answer, and for take its fields alone. A message answer's keys
// are only ever added; the listing carries a key only while it holds a value,
// as listedTool says. Take answers no message: its fields carry every value
// its text prints, the framing lead among them, so a message would only
// repeat them.

type nameFields struct {
	Name string `json:"name"`
}

type updatedFields struct {
	Name       string `json:"name"`
	Version    int    `json:"version"`
	ForkedFrom string `json:"forked_from,omitempty"`
}

type takenFields struct {
	Framing string      `json:"framing"`
	Into    string      `json:"into"`
	Tools   []takenTool `json:"tools"`
}

type takenTool struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
	Summary string `json:"summary"`
	// MergedFrom is the name the call gave, merged into the tool taken.
	MergedFrom string `json:"merged_from,omitempty"`
	Copy       string `json:"copy"`
	Usage      string `json:"usage,omitempty"`
	Inputs     string `json:"inputs,omitempty"`
	Outputs    string `json:"outputs,omitempty"`
	Foreign    bool   `json:"foreign,omitempty"`
}

type mergedFields struct {
	Survivor string   `json:"survivor"`
	Members  []string `json:"members"`
}

type reindexedFields struct {
	Tools   int `json:"tools"`
	Skipped int `json:"skipped"`
}

// mcpOutputs are the answers per tool: a read's listing, or the fields of a
// message answer.
var mcpOutputs = map[string]any{
	"find": findListing{}, "list": listing{}, "take": takenFields{}, "add": nameFields{},
	"update": updatedFields{}, "merge": mergedFields{}, "reindex": reindexedFields{},
}

// mcpReads answer their listing over MCP whatever the call's json key says.
var mcpReads = []string{"find", "list"}

// mcpBare answer their fields with no message beside them.
var mcpBare = []string{"take"}
