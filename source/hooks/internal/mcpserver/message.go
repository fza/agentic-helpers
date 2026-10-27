package mcpserver

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
)

// Messaged is an answer carrying a command's text under `message` beside the
// fields of what the call produced, a struct whose keys sit at the top level.
// A nil fields leaves the message alone.
func Messaged(message string, fields any) any {
	return messaged{message: message, fields: fields}
}

type messaged struct {
	message string
	fields  any
}

func (answer messaged) MarshalJSON() ([]byte, error) {
	object := map[string]json.RawMessage{}

	if answer.fields != nil {
		data, err := json.Marshal(answer.fields)
		if err != nil {
			return nil, fmt.Errorf("encoding the answer's fields: %w", err)
		}

		err = json.Unmarshal(data, &object)
		if err != nil {
			return nil, fmt.Errorf("the answer's fields are not an object: %w", err)
		}
	}

	// A string always encodes.
	object["message"], _ = json.Marshal(answer.message)

	return json.Marshal(object)
}

// MessageSchema is the schema of a Messaged answer over fields' type.
func MessageSchema(fields any) json.RawMessage {
	node := schemaNode{"type": "object", "properties": schemaNode{}}
	if fields != nil {
		node = schemaOf(reflect.TypeOf(fields), map[reflect.Type]bool{})
	}

	properties, _ := node["properties"].(schemaNode)
	properties["message"] = schemaNode{"type": "string"}

	required, _ := node["required"].([]string)
	node["required"] = append([]string{"message"}, slices.DeleteFunc(slices.Clone(required),
		func(name string) bool { return name == "message" })...)

	// Every schema value is a map, string, bool or slice of those, which
	// always encode.
	data, _ := json.Marshal(node)

	return data
}
