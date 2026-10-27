package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/mcpserver"
)

// serveMCP answers the plugin's `memory` server: every agent command as a tool
// of the same name, taking the command's arguments as JSON and answering JSON:
// a read its --json object, any other command its text as the message beside
// what it did. A refusal comes back as an error result carrying its text.
func (mem *memory) serveMCP(ctx context.Context) int {
	var tools []mcpserver.Tool

	for _, held := range commands() {
		tools = append(tools, mcpserver.Tool{
			Name:         held.name,
			Description:  fmt.Sprintf("%s. CLI form: %s %s", held.description, self, held.usage),
			InputSchema:  json.RawMessage(closedSchema(held.schema)),
			OutputSchema: outputSchema(held.name),
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
	mem.here = nil

	defer func() { mem.hashCache.save() }()

	refusal := mem.storeRefusal()
	if refusal != "" && held.name != "who" {
		return mcpserver.Refusal(self + ": " + refusal)
	}

	admitted := mem.admission(ctx, held.name)
	if admitted != nil {
		return mcpserver.Refusal(self + ": " + admitted.Error())
	}

	err := exactKeys(held, arguments)
	if err != nil {
		return mcpserver.Refusal(self + ": " + err.Error())
	}

	req := request{given: map[string]bool{}}

	err = json.Unmarshal(arguments, &req)
	if err != nil {
		return mcpserver.Refusal(fmt.Sprintf("%s: the arguments do not fit %s: %v", self, held.name, err))
	}

	read := slices.Contains(mcpReads, held.name)
	req.JSON = read

	if req.ID != "" && len(req.IDs) == 0 {
		req.IDs = []string{req.ID}
	}

	req.normalize()

	answer := held.run(mem, ctx, req)
	if answer.failed {
		return mcpserver.Refusal(self + ": " + answer.text)
	}

	// A read answering text, as view's explain does, answers it as a message.
	if read && json.Valid([]byte(answer.text)) {
		return mcpserver.Result{Structured: json.RawMessage(answer.text)}
	}

	return mcpserver.Result{Structured: mcpserver.Messaged(answer.text, answer.fields)}
}

// outputSchema is what a tool answers: a read's JSON object, or its message
// beside the fields of what the call produced.
func outputSchema(name string) json.RawMessage {
	if slices.Contains(mcpReads, name) {
		return mcpserver.SchemaOf(mcpOutputs[name])
	}

	return mcpserver.MessageSchema(mcpOutputs[name])
}

// closedSchema marks a tool's schema as taking no key beyond its properties,
// so a client validating against it sees the rule exactKeys enforces.
func closedSchema(schema string) string {
	return strings.TrimSuffix(schema, "}") + `,"additionalProperties":false}`
}

// exactKeys refuses a call naming a key the tool's schema does not list,
// spelled exactly: arguments decode into one request shared by every tool,
// which would match a key in any case and drop one it does not know.
func exactKeys(held command, arguments json.RawMessage) error {
	var given map[string]json.RawMessage

	if len(bytes.TrimSpace(arguments)) > 0 {
		err := json.Unmarshal(arguments, &given)
		if err != nil {
			return fmt.Errorf("the arguments do not fit %s: %w", held.name, err)
		}
	}

	listed, err := schemaKeys(held.schema)
	if err != nil {
		return fmt.Errorf("reading the %s schema: %w", held.name, err)
	}

	var unknown []string

	for key := range given {
		if instead, gone := held.replaced[key]; gone && !slices.Contains(listed, key) {
			return fmt.Errorf("%s takes no key %q: %s", held.name, key, instead)
		}

		if !slices.Contains(listed, key) {
			unknown = append(unknown, fmt.Sprintf("%q", key))
		}
	}

	if len(unknown) == 0 {
		return nil
	}

	slices.Sort(unknown)

	takes := "none"
	if len(listed) > 0 {
		takes = strings.Join(listed, ", ")
	}

	return fmt.Errorf("%s takes no key %s; it takes %s", held.name, strings.Join(unknown, ", "), takes)
}

// schemaKeys lists a schema's top-level properties in the order it names them.
func schemaKeys(schema string) ([]string, error) {
	var parsed struct {
		Properties json.RawMessage `json:"properties"`
	}

	err := json.Unmarshal([]byte(schema), &parsed)
	if err != nil {
		return nil, err
	}

	if len(parsed.Properties) == 0 {
		return nil, nil
	}

	decoder := json.NewDecoder(bytes.NewReader(parsed.Properties))

	opening, err := decoder.Token()
	if err != nil {
		return nil, err
	}

	if opening != json.Delim('{') {
		return nil, errors.New("properties is not an object")
	}

	var keys []string

	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}

		key, isKey := token.(string)
		if !isKey {
			return nil, errors.New("a property name is not a string")
		}

		keys = append(keys, key)

		var skipped json.RawMessage

		err = decoder.Decode(&skipped)
		if err != nil {
			return nil, err
		}
	}

	return keys, nil
}
