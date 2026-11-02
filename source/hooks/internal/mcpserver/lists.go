package mcpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// A model asked for an array holding one value often writes the value bare
// and unquoted (`"term": Tom`), which the client cannot parse and never sends.
// Every top-level array key is therefore listed as one value or an array of
// them, and a call sending one value is handed to the tool as a one-item array,
// so the tool reads a list either way.

var errNotObject = errors.New("not a JSON object")

// arrayOnlyKeys are the keywords that describe an array rather than its items,
// kept on the array branch of a key listed as one value or many.
var arrayOnlyKeys = []string{"type", "items", "minItems", "maxItems", "uniqueItems"}

type member struct {
	key   string
	value json.RawMessage
}

// oneOrMany is a tool's input schema with every top-level array property
// listed as its item or an array of items, and the names of those properties.
// A schema without properties is answered unchanged.
func oneOrMany(schema json.RawMessage) (json.RawMessage, []string, error) {
	top, err := members(schema)
	if err != nil {
		return nil, nil, fmt.Errorf("reading the schema: %w", err)
	}

	var lists []string

	for index, field := range top {
		if field.key != "properties" {
			continue
		}

		properties, err := members(field.value)
		if err != nil {
			return nil, nil, fmt.Errorf("reading the schema's properties: %w", err)
		}

		for at, property := range properties {
			widened, isList, err := widenList(property.value)
			if err != nil {
				return nil, nil, fmt.Errorf("reading property %q: %w", property.key, err)
			}

			if isList {
				properties[at].value = widened
				lists = append(lists, property.key)
			}
		}

		top[index].value = encode(properties)
	}

	return encode(top), lists, nil
}

// widenList turns an array property into `anyOf` its item and the array, its
// other keywords (a description) kept beside the choice.
func widenList(property json.RawMessage) (json.RawMessage, bool, error) {
	fields, err := members(property)
	if err != nil {
		return nil, false, err
	}

	typeAt := slices.IndexFunc(fields, func(field member) bool { return field.key == "type" })
	if typeAt < 0 || string(bytes.TrimSpace(fields[typeAt].value)) != `"array"` {
		return property, false, nil
	}

	item := json.RawMessage(`{}`)

	var array, rest []member

	for _, field := range fields {
		if field.key == "items" {
			item = field.value
		}

		if slices.Contains(arrayOnlyKeys, field.key) {
			array = append(array, field)
		} else {
			rest = append(rest, field)
		}
	}

	choice := json.RawMessage(`[` + string(item) + `,` + string(encode(array)) + `]`)

	return encode(append([]member{{key: "anyOf", value: choice}}, rest...)), true, nil
}

// asLists is a call's arguments with every list key holding one value made a
// one-item array. Arguments that are no object go to the tool unchanged, to be
// refused there.
func asLists(arguments json.RawMessage, lists []string) json.RawMessage {
	given, err := members(arguments)
	if err != nil {
		return arguments
	}

	for index, field := range given {
		value := bytes.TrimSpace(field.value)
		if !slices.Contains(lists, field.key) || bytes.HasPrefix(value, []byte("[")) || string(value) == "null" {
			continue
		}

		given[index].value = json.RawMessage(`[` + string(value) + `]`)
	}

	return encode(given)
}

// members reads a JSON object's members in the order it holds them.
func members(object json.RawMessage) ([]member, error) {
	decoder := json.NewDecoder(bytes.NewReader(object))

	opening, err := decoder.Token()
	if err != nil {
		return nil, err
	}

	if opening != json.Delim('{') {
		return nil, errNotObject
	}

	var fields []member

	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}

		key, isKey := token.(string)
		if !isKey {
			return nil, errNotObject
		}

		var value json.RawMessage

		err = decoder.Decode(&value)
		if err != nil {
			return nil, err
		}

		fields = append(fields, member{key: key, value: value})
	}

	_, err = decoder.Token()
	if err != nil {
		return nil, err
	}

	return fields, nil
}

func encode(fields []member) json.RawMessage {
	var out bytes.Buffer

	out.WriteByte('{')

	for index, field := range fields {
		if index > 0 {
			out.WriteByte(',')
		}

		// A key read from valid JSON always encodes.
		key, _ := json.Marshal(field.key)
		out.Write(key)
		out.WriteByte(':')
		out.Write(field.value)
	}

	out.WriteByte('}')

	return out.Bytes()
}
