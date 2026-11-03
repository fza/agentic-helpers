package slots_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
)

// stuck is a kind whose minimal form still passes its ceiling.
type stuck struct {
	Text string `json:"text"`
}

func (*stuck) Ceiling() int { return 10 }

func (*stuck) Limit() {}

func (*stuck) Shrink() bool { return false }

func TestAValueOverItsCeilingAtItsMinimalFormIsRefused(t *testing.T) {
	_, err := slots.Encode(&stuck{Text: strings.Repeat("x", 100)})
	if !errors.Is(err, slots.ErrOverCeiling) {
		t.Errorf("a value its fit cannot bring within its ceiling should be refused, got %v", err)
	}
}

func TestCutJSONKeepsTheLongestStartWithinItsBytes(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		limit int
		want  string
	}{
		{"text within its bytes", "abc", 5, "abc"},
		{"plain text past them", "abcdef", 5, "abc"},
		{"escaped characters past them", "<<<", 14, "<<"},
		{"a rune never split", "aé", 4, "a"},
		{"nothing fitting", "abc", 2, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := slots.CutJSON(tc.text, tc.limit); got != tc.want {
				t.Errorf("the cut should keep the longest start within its bytes, got %q", got)
			}
		})
	}
}

func TestAValuePastMaxValueIsRefused(t *testing.T) {
	root := t.TempDir()
	file := slots.Open(root, filepath.Join(root, "sessions.slots"))

	err := file.Put(context.Background(), "k", []byte(strings.Repeat("v", slots.MaxValue+1)))
	if !errors.Is(err, slots.ErrTooLarge) {
		t.Errorf("a value past the largest a slot holds should be refused, got %v", err)
	}

	err = file.Put(context.Background(), "k", []byte(strings.Repeat("v", slots.MaxValue)))
	if err != nil {
		t.Errorf("a value of the largest a slot holds should be stored: %v", err)
	}
}
