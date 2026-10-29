package test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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

// mcpJSONKey matches prose naming an MCP json key: the bare key in backticks
// or quotes. The command line's --json and the JSON keys of an answer do not
// match.
var mcpJSONKey = regexp.MustCompile("`json`|\"json\"")

// MCPJSONKeyMentions lists every line of a Markdown file under root naming an
// MCP json key, as file:line: text.
func MCPJSONKeyMentions(root string) ([]string, error) {
	var hits []string

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}

		for number, line := range strings.Split(string(content), "\n") {
			if mcpJSONKey.MatchString(line) {
				hits = append(hits, fmt.Sprintf("%s:%d: %s", path, number+1, line))
			}
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking %s: %w", root, err)
	}

	return hits, nil
}
