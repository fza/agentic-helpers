package transcript_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/transcript"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestFirstPromptIsTheFirstRecordsText(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"a subagent's transcript", "memory/subagent/agent.jsonl",
			"agentic-helpers level: shared (set by the spawner)\nYou are Q. Reply Q done."},
		{"a transcript not written yet", "", ""},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "agent.jsonl")
			if each.content != "" {
				test.WriteFile(t, path, test.Fixture(t, each.content))
			}

			got, err := transcript.FirstPrompt(context.Background(), path)
			if err != nil || got != each.want {
				t.Errorf("the prompt should be the first record's text alone, got %q, %v", got, err)
			}
		})
	}
}
