package test

import (
	"encoding/json"
	"fmt"
	"slices"
)

// EmptyKeys is the path of every key in a JSON answer holding an empty value:
// an empty list or object, null, an empty string or false. A read answer
// carries none, since an absent key reads as empty. A list's elements are
// walked, never judged: an element is no key.
func EmptyKeys(data []byte) ([]string, error) {
	var value any

	err := json.Unmarshal(data, &value)
	if err != nil {
		return nil, fmt.Errorf("reading the answer: %w", err)
	}

	var found []string

	walkEmpty(value, "$", &found)

	return found, nil
}

func walkEmpty(value any, at string, found *[]string) {
	switch held := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(held))
		for key := range held {
			keys = append(keys, key)
		}

		slices.Sort(keys)

		for _, key := range keys {
			if isEmpty(held[key]) {
				*found = append(*found, at+"."+key)

				continue
			}

			walkEmpty(held[key], at+"."+key, found)
		}
	case []any:
		for index, item := range held {
			walkEmpty(item, fmt.Sprintf("%s[%d]", at, index), found)
		}
	}
}

func isEmpty(value any) bool {
	switch held := value.(type) {
	case nil:
		return true
	case bool:
		return !held
	case string:
		return held == ""
	case []any:
		return len(held) == 0
	case map[string]any:
		return len(held) == 0
	}

	return false
}
