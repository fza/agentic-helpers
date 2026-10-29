package tools

// What each MCP tool answers beside its message, or, for find and list, the
// listing they answer. Keys are only ever added.

type nameFields struct {
	Name string `json:"name"`
}

type updatedFields struct {
	Name       string `json:"name"`
	Version    int    `json:"version"`
	ForkedFrom string `json:"forked_from,omitempty"`
}

type takenFields struct {
	Into  string      `json:"into"`
	Tools []takenTool `json:"tools"`
}

type takenTool struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
	Copy    string `json:"copy"`
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
	"find": listing[scoredTool]{}, "list": listing[listedTool]{}, "take": takenFields{}, "add": nameFields{},
	"update": updatedFields{}, "merge": mergedFields{}, "reindex": reindexedFields{},
}

// mcpReads answer their listing over MCP whatever the call's json key says.
var mcpReads = []string{"find", "list"}
