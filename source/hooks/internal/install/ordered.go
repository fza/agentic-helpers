package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

var errNotObject = errors.New("not a JSON object")

// object is a JSON object that keeps its keys in the order it read them, so a
// rewrite changes only what it means to.
type object []field

type field struct {
	key   string
	value json.RawMessage
}

func parseObject(data []byte) (object, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))

	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("reading an object: %w", err)
	}

	if delim, isDelim := token.(json.Delim); !isDelim || delim != '{' {
		return nil, errNotObject
	}

	var held object

	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("reading a key: %w", err)
		}

		key, isKey := token.(string)
		if !isKey {
			return nil, errNotObject
		}

		var value json.RawMessage

		err = decoder.Decode(&value)
		if err != nil {
			return nil, fmt.Errorf("reading the value of %s: %w", key, err)
		}

		held = append(held, field{key: key, value: value})
	}

	_, err = decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("closing an object: %w", err)
	}

	return held, nil
}

func (held object) get(key string) (json.RawMessage, bool) {
	for _, item := range held {
		if item.key == key {
			return item.value, true
		}
	}

	return nil, false
}

// set returns a copy carrying the value, so the object it was called on keeps
// what it held.
func (held object) set(key string, value json.RawMessage) object {
	copied := slices.Clone(held)

	for index, item := range copied {
		if item.key == key {
			copied[index].value = value

			return copied
		}
	}

	return append(copied, field{key: key, value: value})
}

func (held object) without(key string) object {
	kept := object{}

	for _, item := range held {
		if item.key != key {
			kept = append(kept, item)
		}
	}

	return kept
}

func (held object) MarshalJSON() ([]byte, error) {
	var buffer bytes.Buffer

	buffer.WriteByte('{')

	for index, item := range held {
		if index > 0 {
			buffer.WriteByte(',')
		}

		key, err := json.Marshal(item.key)
		if err != nil {
			return nil, fmt.Errorf("writing a key: %w", err)
		}

		buffer.Write(key)
		buffer.WriteByte(':')
		buffer.Write(item.value)
	}

	buffer.WriteByte('}')

	return buffer.Bytes(), nil
}

func raw(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage("null")
	}

	return data
}

func indented(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encoding the settings: %w", err)
	}

	var buffer bytes.Buffer

	err = json.Indent(&buffer, data, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("indenting the settings: %w", err)
	}

	buffer.WriteByte('\n')

	return buffer.Bytes(), nil
}

func sameJSON(left json.RawMessage, right json.RawMessage) bool {
	var compactLeft, compactRight bytes.Buffer

	if json.Compact(&compactLeft, left) != nil || json.Compact(&compactRight, right) != nil {
		return false
	}

	return bytes.Equal(compactLeft.Bytes(), compactRight.Bytes())
}
