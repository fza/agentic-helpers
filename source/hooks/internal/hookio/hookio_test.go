package hookio_test

import (
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
)

func TestReadPayload(t *testing.T) {
	cases := []struct {
		name    string
		stdin   string
		session string
		fails   bool
	}{
		{name: "session", stdin: `{"session_id": "abc"}`, session: "abc"},
		{name: "padded session", stdin: `{"session_id": "  abc\n"}`, session: "abc"},
		{name: "no session", stdin: `{"prompt": "hi"}`},
		{name: "garbled", stdin: `{not json`, fails: true},
		{name: "not an object", stdin: `[1, 2]`, fails: true},
		{name: "empty", stdin: ``, fails: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := hookio.ReadPayload(strings.NewReader(tc.stdin))

			if (err != nil) != tc.fails {
				t.Fatalf("only unreadable input should fail, got: %v", err)
			}

			if payload.SessionID != tc.session {
				t.Error("the session identifier should be read and trimmed")
			}
		})
	}
}
