package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/mcpserver"
)

// serveMCP answers the plugin's `tools` server: every verb as a tool of the
// same name, taking exactly the keys its schema lists.
func (lib *library) serveMCP(ctx context.Context) int {
	var served []mcpserver.Tool

	for _, held := range commands() {
		served = append(served, mcpserver.Tool{
			Name:         held.name,
			Description:  held.description,
			InputSchema:  json.RawMessage(strings.TrimSuffix(held.schema, "}") + `,"additionalProperties":false}`),
			OutputSchema: outputSchema(held.name),
			Handle: func(ctx context.Context, arguments json.RawMessage) mcpserver.Result {
				return lib.callTool(ctx, held, arguments)
			},
		})
	}

	err := mcpserver.ServeIdle(ctx, lib.streams.In, lib.streams.Out, "tools", lib.env.Version, served, lib.env.Idle)
	if err != nil {
		return lib.refuse(err.Error())
	}

	return 0
}

func (lib *library) callTool(ctx context.Context, held command, arguments json.RawMessage) mcpserver.Result {
	err := exactKeys(held, arguments)
	if err != nil {
		return mcpserver.Refusal("refused: " + err.Error())
	}

	var req request

	if len(bytes.TrimSpace(arguments)) > 0 {
		err = json.Unmarshal(arguments, &req)
		if err != nil {
			return mcpserver.Refusal(fmt.Sprintf("refused: the arguments do not fit %s: %v", held.name, err))
		}
	}

	if req.Limit < 0 || req.Page < 0 {
		return mcpserver.Refusal("refused: limit and page take a number of 1 or more")
	}

	read := slices.Contains(mcpReads, held.name)
	req.JSON = read

	lib.forget()

	answer := lib.run(ctx, held, req)
	if answer.failed {
		return mcpserver.Refusal("refused: " + answer.text)
	}

	if read {
		return mcpserver.Result{Structured: json.RawMessage(answer.text)}
	}

	return mcpserver.Result{Structured: mcpserver.Messaged(answer.text, answer.fields)}
}

// outputSchema is the shape a tool answers: a read's listing, or its message
// beside the fields of what it produced.
func outputSchema(name string) json.RawMessage {
	if slices.Contains(mcpReads, name) {
		return mcpserver.SchemaOf(mcpOutputs[name])
	}

	return mcpserver.MessageSchema(mcpOutputs[name])
}

// exactKeys refuses a key the tool's schema does not list, spelled exactly:
// JSON decoding would match a key in any case and drop one it does not know.
func exactKeys(held command, arguments json.RawMessage) error {
	var given map[string]json.RawMessage

	if len(bytes.TrimSpace(arguments)) > 0 {
		err := json.Unmarshal(arguments, &given)
		if err != nil {
			return fmt.Errorf("the arguments do not fit %s: %w", held.name, err)
		}
	}

	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}

	err := json.Unmarshal([]byte(held.schema), &schema)
	if err != nil {
		return fmt.Errorf("reading the %s schema: %w", held.name, err)
	}

	var unknown []string

	_, takesJSON := flagOf(held, "json")

	for key := range given {
		// A read answers JSON over MCP whatever json says, so the schema
		// leaves the key out, and a call still sending it is let through.
		if key == "json" && takesJSON {
			continue
		}

		_, listed := schema.Properties[key]
		if !listed {
			unknown = append(unknown, fmt.Sprintf("%q", key))
		}
	}

	if len(unknown) == 0 {
		return nil
	}

	slices.Sort(unknown)

	takes := sortedKeys(schema.Properties)

	listedKeys := "none"
	if len(takes) > 0 {
		listedKeys = strings.Join(takes, ", ")
	}

	return fmt.Errorf("%s takes no key %s; it takes %s", held.name, strings.Join(unknown, ", "), listedKeys)
}
