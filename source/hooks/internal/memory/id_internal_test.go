package memory

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func TestDrawIDRedrawsBytesThatWouldBiasTheAlphabet(t *testing.T) {
	id, err := drawID(bytes.NewReader([]byte{252, 255, 0, 35, 36, 251}), 4)
	if err != nil {
		t.Fatalf("drawing from enough bytes should succeed: %v", err)
	}

	if id != "m-a9a9" {
		t.Errorf("a byte at or above 252 should be drawn again, the rest taken modulo the alphabet, got %s", id)
	}
}

func TestDrawIDFailsWhenTheRandomSourceRunsDry(t *testing.T) {
	_, err := drawID(bytes.NewReader([]byte{0, 1, 252}), 4)
	if err == nil {
		t.Error("a random source running dry should be an error, not a short id")
	}
}

func TestIDSchemaTakesEveryLengthAStoreHolds(t *testing.T) {
	var schema struct {
		Pattern string `json:"pattern"`
	}

	err := json.Unmarshal([]byte(idSchema), &schema)
	if err != nil {
		t.Fatal(err)
	}

	pattern := regexp.MustCompile(schema.Pattern)

	cases := []struct {
		name  string
		id    string
		valid bool
	}{
		{name: "an id minted with four characters", id: "m-r3t1", valid: true},
		{name: "an id minted at the default length", id: "m-r3t1ab", valid: true},
		{name: "an id at the longest length", id: "m-" + strings.Repeat("a", 16), valid: true},
		{name: "an id longer than any length mints", id: "m-" + strings.Repeat("a", 17)},
		{name: "an id shorter than any length minted", id: "m-r3t"},
		{name: "an id with an uppercase character", id: "m-R3T1ab"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if pattern.MatchString(tc.id) != tc.valid {
				t.Error("the MCP id schema should accept exactly the lengths a store can hold")
			}
		})
	}
}
