package test

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// refusalSchema is the one shape a refused tool call answers.
const refusalSchema = `{"type":"object","properties":{"error":{"type":"string"}},"required":["error"],` +
	`"additionalProperties":false}`

// OutputSchemas is each tool's outputSchema from a tools/list answer line.
func OutputSchemas(line string) (map[string]json.RawMessage, error) {
	var listing struct {
		Result struct {
			Tools []struct {
				Name         string          `json:"name"`
				OutputSchema json.RawMessage `json:"outputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}

	err := json.Unmarshal([]byte(line), &listing)
	if err != nil {
		return nil, fmt.Errorf("reading the tool listing: %w", err)
	}

	schemas := map[string]json.RawMessage{}
	for _, tool := range listing.Result.Tools {
		schemas[tool.Name] = tool.OutputSchema
	}

	return schemas, nil
}

// ListedTools is each tool's listing from a tools/list answer line, by name:
// its whole object as the server sent it.
func ListedTools(line string) (map[string]map[string]json.RawMessage, error) {
	var listing struct {
		Result struct {
			Tools []map[string]json.RawMessage `json:"tools"`
		} `json:"result"`
	}

	err := json.Unmarshal([]byte(line), &listing)
	if err != nil {
		return nil, fmt.Errorf("reading the tool listing: %w", err)
	}

	tools := map[string]map[string]json.RawMessage{}
	for _, tool := range listing.Result.Tools {
		var name string

		err = json.Unmarshal(tool["name"], &name)
		if err != nil {
			return nil, fmt.Errorf("reading a listed tool's name: %w", err)
		}

		tools[name] = tool
	}

	return tools, nil
}

// AnswerFits says why a tools/call answer line breaks the shape every MCP
// answer keeps, or nothing where it keeps it: its text is its
// structuredContent serialized, a refusal carries the error key alone, and any
// other answer fits the tool's outputSchema. A protocol error carries no
// result and fits.
func AnswerFits(schemas map[string]json.RawMessage, tool string, line string) error {
	var answer struct {
		Result *struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			Structured json.RawMessage `json:"structuredContent"`
			IsError    bool            `json:"isError"`
		} `json:"result"`
	}

	err := json.Unmarshal([]byte(line), &answer)
	if err != nil {
		return fmt.Errorf("reading the answer: %w", err)
	}

	if answer.Result == nil {
		return nil
	}

	if len(answer.Result.Content) != 1 || answer.Result.Content[0].Text != string(answer.Result.Structured) {
		return errors.New("the text is not the structured content serialized")
	}

	schema := json.RawMessage(refusalSchema)

	if !answer.Result.IsError {
		listed, found := schemas[tool]
		if !found {
			return fmt.Errorf("no tool %s is listed", tool)
		}

		schema = listed
	}

	return Conforms(schema, answer.Result.Structured)
}

// UndescribedInputs lists what in a tools/list answer line leaves a caller to
// guess a call's shape: a tool description in command-line form, which names
// flags and positionals where the call takes keys, and an input key without a
// description.
func UndescribedInputs(line string) ([]string, error) {
	var listing struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				InputSchema struct {
					Properties map[string]struct {
						Description string `json:"description"`
					} `json:"properties"`
				} `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}

	err := json.Unmarshal([]byte(line), &listing)
	if err != nil {
		return nil, fmt.Errorf("reading the tool listing: %w", err)
	}

	var gaps []string

	for _, tool := range listing.Result.Tools {
		if strings.Contains(tool.Description, "--") || strings.Contains(tool.Description, "<") {
			gaps = append(gaps, fmt.Sprintf("%s: description in command-line form: %s", tool.Name, tool.Description))
		}

		for key, property := range tool.InputSchema.Properties {
			if strings.TrimSpace(property.Description) == "" {
				gaps = append(gaps, fmt.Sprintf("%s: key %s has no description", tool.Name, key))
			}
		}
	}

	slices.Sort(gaps)

	return gaps, nil
}
