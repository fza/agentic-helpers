package memory

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fza/agentic-helpers/source/hooks/internal/mcpserver"
)

// serveMCP answers the plugin's `memory` server: every agent command as a tool
// of the same name, taking the command's arguments as JSON and answering what
// the command prints. A refusal comes back as a tool result marked as an error.
func (mem *memory) serveMCP(ctx context.Context) int {
	var tools []mcpserver.Tool

	for _, held := range commands() {
		tools = append(tools, mcpserver.Tool{
			Name:        held.name,
			Description: fmt.Sprintf("%s. CLI form: %s %s", held.description, self, held.usage),
			InputSchema: json.RawMessage(held.schema),
			Handle: func(ctx context.Context, arguments json.RawMessage) mcpserver.Result {
				return mem.callTool(ctx, held, arguments)
			},
		})
	}

	err := mcpserver.Serve(ctx, mem.streams.In, mem.streams.Out, "memory", mem.env.Version, tools)
	if err != nil {
		return mem.refuse(err)
	}

	return 0
}

func (mem *memory) callTool(ctx context.Context, held command, arguments json.RawMessage) mcpserver.Result {
	req := request{given: map[string]bool{}}

	err := json.Unmarshal(arguments, &req)
	if err != nil {
		return mcpserver.Result{Text: fmt.Sprintf("%s: the arguments do not fit %s: %v", self, held.name, err),
			IsError: true}
	}

	if req.ID != "" && len(req.IDs) == 0 {
		req.IDs = []string{req.ID}
	}

	answer := held.run(mem, ctx, req)
	if answer.failed {
		return mcpserver.Result{Text: self + ": " + answer.text, IsError: true}
	}

	return mcpserver.Result{Text: answer.text}
}
