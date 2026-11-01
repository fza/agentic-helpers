package memory_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/cacheprune"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func pruneStarts(starter *test.Starter) int {
	count := 0

	for _, args := range starter.Starts() {
		if len(args) == 1 && args[0] == cacheprune.Command {
			count++
		}
	}

	return count
}

func TestSessionStartStartsTheDailyCachePruneOnce(t *testing.T) {
	h := newHarness(t)
	h.starter = &test.Starter{}

	h.must(unrelatedStart(), "session-start")
	h.must(unrelatedStart(), "session-start")

	if pruneStarts(h.starter) != 1 {
		t.Error("SessionStart should start the cache prune once a day, detached")
	}
}

func TestSessionStartTellsAFailedCachePruneOnce(t *testing.T) {
	h := newHarness(t)

	status, err := json.Marshal(cacheprune.Status{Started: "2026-10-31T00:00:00Z", Finished: "2026-10-31T00:00:01Z",
		Error: "deleting the vector store: permission denied"})
	if err != nil {
		t.Fatalf("encoding the status: %v", err)
	}

	err = os.MkdirAll(h.roots().Data, 0o700)
	if err != nil {
		t.Fatalf("creating the data root: %v", err)
	}

	err = os.WriteFile(filepath.Join(h.roots().Data, cacheprune.StatusName), status, 0o600)
	if err != nil {
		t.Fatalf("writing the status: %v", err)
	}

	first := h.must(unrelatedStart(), "session-start")
	second := h.must(unrelatedStart(), "session-start")

	if !strings.Contains(first, "cache prune failed") || !strings.Contains(first, "permission denied") {
		t.Error("the first SessionStart after a failed prune should name its error")
	}

	if strings.Contains(second, "cache prune failed") {
		t.Error("a failed prune should be told once")
	}
}
