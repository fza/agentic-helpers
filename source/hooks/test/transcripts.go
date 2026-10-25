package test

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// AppendTurns adds turns to a Claude Code transcript, each a prompt and a reply
// of about chars characters, one a minute from start, so a test can grow the
// turns since an entry past any length it needs.
func AppendTurns(t *testing.T, path string, start time.Time, turns int, chars int) {
	t.Helper()

	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("opening the transcript: %v", err)
	}

	defer func() { _ = file.Close() }()

	encoder := json.NewEncoder(file)

	for index := range turns {
		at := start.Add(time.Duration(index) * time.Minute).UTC().Format("2006-01-02T15:04:05.000Z")
		text := fmt.Sprintf("turn %d %s", index, strings.Repeat("word ", chars/5))

		lines := []map[string]any{
			{"type": "user", "uuid": fmt.Sprintf("u-%d-%d", start.Unix(), index), "timestamp": at,
				"message": map[string]any{"role": "user", "content": "prompt " + text}},
			{"type": "assistant", "uuid": fmt.Sprintf("a-%d-%d", start.Unix(), index), "timestamp": at,
				"message": map[string]any{"role": "assistant", "content": []map[string]any{{"type": "text", "text": "reply " + text}}}},
		}

		for _, line := range lines {
			err = encoder.Encode(line)
			if err != nil {
				t.Fatalf("writing the transcript: %v", err)
			}
		}
	}
}
