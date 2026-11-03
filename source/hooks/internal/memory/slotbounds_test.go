package memory_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

type ladderSlot struct {
	Wrote      []string `json:"wrote"`
	LastPrompt string   `json:"last_prompt"`
}

func (h *harness) ladder() ladderSlot {
	h.t.Helper()

	var held ladderSlot

	err := json.Unmarshal([]byte(h.slot("ladder/"+h.session)), &held)
	if err != nil {
		h.t.Fatalf("the session's ladder should be JSON: %v", err)
	}

	return held
}

// TestAPromptOfOneHugeTokenKeepsTheLadderMoving sends a prompt longer than
// a slot holds, without a space in it: the ladder still takes every prompt
// after it.
func TestAPromptOfOneHugeTokenKeepsTheLadderMoving(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	h.must(h.promptOf(strings.Repeat("<", 200_000)), "prompt")

	encoded, _ := json.Marshal(h.ladder().LastPrompt)
	if h.ladder().LastPrompt == "" || len(encoded) > 3_500 {
		t.Errorf("the ladder should keep the prompt cut to its bytes, %d bytes as JSON", len(encoded))
	}

	h.must(h.promptOf("count the words"), "prompt")

	if got := h.ladder().LastPrompt; got != "count the words" {
		t.Errorf("a prompt after a huge one should still reach the ladder, got %.40q", got)
	}
}

// TestALongPromptKeepsTheWordsOfTheAskedLine sends a prompt of many words:
// the ladder keeps the words the asked line shows, not the whole prompt.
func TestALongPromptKeepsTheWordsOfTheAskedLine(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	h.must(h.promptOf(strings.Repeat("word ", 5_000)), "prompt")

	if words := len(strings.Fields(h.ladder().LastPrompt)); words == 0 || words > pluginconfig.DefaultEntryTargetWords {
		t.Errorf("the ladder should keep the prompt's first words only, kept %d", words)
	}
}

// TestAWriteTheSlotFileRefusesIsReported plants a file that is no slot file
// where the store keeps its session state: the prompt hook says on stderr
// that its write failed, and still answers.
func TestAWriteTheSlotFileRefusesIsReported(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.must(h.promptOf("start"), "prompt")

	path := filepath.Join(h.cacheDir(), "sessions.slots")

	err := os.Remove(path)
	if err != nil {
		t.Fatal(err)
	}

	test.WriteFile(t, path, "not a slot file")

	got := h.run(h.promptOf("go on"), "prompt")
	if got.code != 0 || !strings.Contains(got.err, "agentic-memory: keeping ladder/"+h.session+":") {
		t.Errorf("a slot write that fails should be reported, exit %d, stderr:\n%s", got.code, got.err)
	}
}
