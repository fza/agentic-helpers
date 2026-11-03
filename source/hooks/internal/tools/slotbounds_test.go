package tools_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

// TestASessionWriteTheSlotFileRefusesIsReported plants a file that is no slot
// file where the store keeps its sessions: a take still copies the tool and
// says on stderr that keeping the session failed.
func TestASessionWriteTheSlotFileRefusesIsReported(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of a file")

	path := filepath.Join(h.roots().StoreCache(filepath.Base(filepath.Dir(h.store())), home.Tools), "sessions.slots")
	test.WriteFile(t, path, "not a slot file")

	got := h.run("", "take", "word-count")
	if got.code != 0 || !strings.Contains(got.err, "agentic-tools: keeping session/"+h.session+":") {
		t.Errorf("a session write that fails should be reported, exit %d, stderr:\n%s", got.code, got.err)
	}
}
