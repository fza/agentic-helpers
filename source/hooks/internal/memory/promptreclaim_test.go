package memory_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/promptvector"
	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
)

func TestSessionStartReclaimsThePromptVectorsOfSessionsGoneAWeek(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	file := slots.Open(h.roots().Cache, filepath.Join(promptvector.Dir(h.roots().Cache), "sessions.slots"), slots.MinWidth)

	for key, written := range map[string]time.Time{
		"crashed": time.Now().Add(-slots.StaleAge - time.Hour),
		"recent":  time.Now().Add(-time.Hour),
	} {
		file.Now = func() time.Time { return written }

		err := file.Put(context.Background(), key, []byte(`{}`))
		if err != nil {
			t.Fatalf("writing %s: %v", key, err)
		}
	}

	h.must(h.sessionStart("startup"), "session-start")

	_, crashed, err := file.Get(context.Background(), "crashed")
	if err != nil {
		t.Fatalf("reading the prompt vectors: %v", err)
	}

	_, recent, err := file.Get(context.Background(), "recent")
	if err != nil {
		t.Fatalf("reading the prompt vectors: %v", err)
	}

	if crashed || !recent {
		t.Error("SessionStart should clear a prompt vector no write touched for a week and keep one written within it")
	}
}
