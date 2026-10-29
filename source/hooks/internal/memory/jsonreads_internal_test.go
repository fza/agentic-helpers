package memory

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

// reencoded is data decoded into the entry object and encoded again, read
// back as plain JSON values.
func reencoded(t *testing.T, data string) map[string]any {
	t.Helper()

	var object jsonEntry

	err := json.Unmarshal([]byte(data), &object)
	if err != nil {
		t.Fatalf("the fixture should decode into the entry object: %v", err)
	}

	encoded, err := json.Marshal(object)
	if err != nil {
		t.Fatalf("the entry object should encode: %v", err)
	}

	var plain map[string]any

	err = json.Unmarshal(encoded, &plain)
	if err != nil {
		t.Fatalf("the encoded entry should read back: %v", err)
	}

	return plain
}

func plainJSON(t *testing.T, data string) map[string]any {
	t.Helper()

	var plain map[string]any

	err := json.Unmarshal([]byte(data), &plain)
	if err != nil {
		t.Fatalf("the fixture should be one JSON object: %v", err)
	}

	return plain
}

func TestTheEntryObjectRoundTripsEveryValue(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
	}{
		{"an entry holding every key", "memory/answers/entry-full.json"},
		{"an entry holding no list", "memory/answers/entry-bare.json"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := test.Fixture(t, tc.fixture)

			got := reencoded(t, data)
			if want := plainJSON(t, data); !reflect.DeepEqual(got, want) {
				t.Errorf("decoding an answer and encoding it again should keep every key holding a value, and add none:\n%v\n%v",
					got, want)
			}
		})
	}
}

func TestTheFullFixtureHoldsEveryKey(t *testing.T) {
	full := plainJSON(t, test.Fixture(t, "memory/answers/entry-full.json"))

	for field := range reflect.TypeFor[jsonEntry]().Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" || name == "-" || name == "seen" {
			continue
		}

		if _, present := full[name]; !present {
			t.Errorf("the fixture of an entry holding every value should carry the key %s", name)
		}
	}
}

func TestACompactAnswerDecodesToTheFullAnswersValues(t *testing.T) {
	compact := reencoded(t, test.Fixture(t, "memory/answers/entry-bare.json"))
	everyKey := reencoded(t, test.Fixture(t, "memory/answers/entry-bare-every-key.json"))

	if !reflect.DeepEqual(compact, everyKey) {
		t.Errorf("an answer leaving its empty keys out should decode to the values of one carrying them empty:\n%v\n%v",
			compact, everyKey)
	}
}

func TestPercentOfScoresAHitAgainstTheBest(t *testing.T) {
	cases := []struct {
		name  string
		value float64
		best  float64
		want  int
	}{
		{"the best hit", 0.83, 0.83, 100},
		{"half the best", 2, 4, 50},
		{"a share rounding up", 2, 3, 67},
		{"a share rounding down", 1, 3, 33},
		{"a value below zero", -0.2, 0.5, 0},
		{"a best of zero, tied", 0, 0, 100},
		{"a best below zero, tied", -0.1, -0.1, 100},
		{"a best below zero, below it", -0.3, -0.1, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := percentOf(tc.value, tc.best); got != tc.want {
				t.Errorf("a hit's score should be its value in whole percent of the best, got %d", got)
			}
		})
	}
}
