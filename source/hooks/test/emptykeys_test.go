package test_test

import (
	"slices"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestEmptyKeysNamesEveryKindOfEmptiness(t *testing.T) {
	cases := []struct {
		name   string
		answer string
		want   []string
	}{
		{"an empty list", `{"for":[]}`, []string{"$.for"}},
		{"an empty object", `{"topics":{}}`, []string{"$.topics"}},
		{"null", `{"status_change":null}`, []string{"$.status_change"}},
		{"an empty string", `{"commit":""}`, []string{"$.commit"}},
		{"false", `{"other_branch":false}`, []string{"$.other_branch"}},
		{"nested in a list", `{"entries":[{"id":"m-a","links":[]}]}`, []string{"$.entries[0].links"}},
		{"values kept", `{"live":0,"heat":0.0,"seen":true,"for":["all"],"holder":{"pid":0},"items":[""]}`, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := test.EmptyKeys([]byte(tc.answer))
			if err != nil || !slices.Equal(got, tc.want) {
				t.Errorf("only a key holding an empty value should be named, got %v %v", got, err)
			}
		})
	}
}
