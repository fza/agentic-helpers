package hookio_test

import (
	"encoding/json"
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

func TestARewriteKeepsEveryOtherKeyAndAnswersNoDecision(t *testing.T) {
	payload, err := hookio.ReadPayload(strings.NewReader(
		`{"tool_name":"Agent","tool_input":{"description":"d","prompt":"old","run_in_background":true}}`))
	if err != nil {
		t.Fatalf("reading the payload: %v", err)
	}

	updated, err := payload.ToolInput.WithField("prompt", "new <prompt>")
	if err != nil {
		t.Fatalf("rewriting the input: %v", err)
	}

	var out strings.Builder

	err = hookio.Rewrite(&out, updated)
	if err != nil {
		t.Fatalf("writing the answer: %v", err)
	}

	var answer map[string]map[string]any

	err = json.Unmarshal([]byte(out.String()), &answer)
	if err != nil {
		t.Fatalf("the answer should be JSON: %v", err)
	}

	given := answer["hookSpecificOutput"]
	input, _ := given["updatedInput"].(map[string]any)

	if given["hookEventName"] != "PreToolUse" || input["prompt"] != "new <prompt>" || input["description"] != "d" ||
		input["run_in_background"] != true {
		t.Errorf("the rewrite should change the one key and keep every other, got %v", given)
	}

	if _, decided := given["permissionDecision"]; decided {
		t.Error("a rewrite should leave the permission decision where it stands")
	}
}
