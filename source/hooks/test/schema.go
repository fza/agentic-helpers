package test

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Conforms says why data does not fit schema, or nothing where it does. It
// reads the JSON Schema keywords mcpserver.SchemaOf writes and a refusal's
// shape uses: type, properties, required, additionalProperties, items, anyOf.
func Conforms(schema json.RawMessage, data []byte) error {
	var node map[string]any

	err := json.Unmarshal(schema, &node)
	if err != nil {
		return fmt.Errorf("reading the schema: %w", err)
	}

	var value any

	err = json.Unmarshal(data, &value)
	if err != nil {
		return fmt.Errorf("reading the answer: %w", err)
	}

	return conforms(node, value, "$")
}

func conforms(node map[string]any, value any, at string) error {
	alternatives, hasAlternatives := node["anyOf"].([]any)
	if hasAlternatives {
		var reasons []string

		for _, alternative := range alternatives {
			branch, _ := alternative.(map[string]any)

			err := conforms(branch, value, at)
			if err == nil {
				return nil
			}

			reasons = append(reasons, err.Error())
		}

		return fmt.Errorf("%s fits no alternative: %s", at, strings.Join(reasons, "; "))
	}

	wanted, typed := node["type"].(string)
	if !typed {
		return nil
	}

	if typeOf(value) != wanted && (wanted != "number" || typeOf(value) != "integer") {
		return fmt.Errorf("%s is %s, not %s", at, typeOf(value), wanted)
	}

	switch held := value.(type) {
	case map[string]any:
		return objectConforms(node, held, at)
	case []any:
		items, _ := node["items"].(map[string]any)

		for index, item := range held {
			err := conforms(items, item, fmt.Sprintf("%s[%d]", at, index))
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func objectConforms(node map[string]any, held map[string]any, at string) error {
	properties, _ := node["properties"].(map[string]any)

	required, _ := node["required"].([]any)
	for _, name := range required {
		key, _ := name.(string)

		_, present := held[key]
		if !present {
			return fmt.Errorf("%s lacks the required key %q", at, key)
		}
	}

	keys := make([]string, 0, len(held))
	for key := range held {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	for _, key := range keys {
		property, listed := properties[key].(map[string]any)
		if !listed {
			property, listed = node["additionalProperties"].(map[string]any)
		}

		if !listed {
			if node["additionalProperties"] == false {
				return fmt.Errorf("%s carries the key %q its schema does not list", at, key)
			}

			continue
		}

		err := conforms(property, held[key], at+"."+key)
		if err != nil {
			return err
		}
	}

	return nil
}

func typeOf(value any) string {
	switch held := value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64:
		if held == float64(int64(held)) {
			return "integer"
		}

		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "unknown"
	}
}

// Readable is the text a person reads in an MCP answer's JSON: its message, a
// refusal's error, or the JSON itself where it carries neither.
func Readable(answer string) string {
	var object struct {
		Message *string `json:"message"`
		Error   *string `json:"error"`
	}

	if json.Unmarshal([]byte(answer), &object) != nil {
		return answer
	}

	if object.Error != nil {
		return *object.Error
	}

	if object.Message != nil {
		return *object.Message
	}

	return answer
}
