package transcript_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/transcript"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestTheUUIDsAForkIsFoundBy(t *testing.T) {
	cases := []struct {
		name      string
		fixtures  []string
		first     string
		forkStart string
	}{
		{name: "a transcript that never compacted", fixtures: []string{"memory/inherit/parent.jsonl"},
			first: "5d1c0f6e-parent-first", forkStart: "5d1c0f6e-parent-first"},
		{name: "a compacted transcript", fixtures: []string{"memory/inherit/parent.jsonl", "memory/inherit/boundary.jsonl"},
			first: "5d1c0f6e-parent-first", forkStart: "7b2e4c90-parent-boundary"},
		{name: "a fork of a compacted transcript", fixtures: []string{"memory/inherit/fork-compacted.jsonl"},
			first: "7b2e4c90-parent-boundary", forkStart: "7b2e4c90-parent-boundary"},
		{name: "a transcript holding no message yet", fixtures: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.jsonl")

			content := `{"type":"permission-mode","permissionMode":"auto","sessionId":"s-1"}` + "\n"
			for _, fixture := range tc.fixtures {
				content += test.Fixture(t, fixture)
			}

			test.WriteFile(t, path, content)

			first, err := transcript.FirstUUID(context.Background(), path)
			if err != nil || first != tc.first {
				t.Errorf("the first uuid should be the first record carrying one, got %q, %v", first, err)
			}

			start, err := transcript.ForkStart(context.Background(), path)
			if err != nil || start != tc.forkStart {
				t.Errorf("a fork should start at the latest compaction boundary, else the first record, got %q, %v", start, err)
			}
		})
	}
}

func TestATranscriptThatIsGoneHoldsNoUUID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gone.jsonl")

	first, err := transcript.FirstUUID(context.Background(), path)
	if err != nil || first != "" {
		t.Errorf("a missing transcript should read as holding no uuid, got %q, %v", first, err)
	}

	start, err := transcript.ForkStart(context.Background(), path)
	if err != nil || start != "" {
		t.Errorf("a missing transcript should read as holding no fork start, got %q, %v", start, err)
	}
}
