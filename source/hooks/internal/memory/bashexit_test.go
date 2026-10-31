package memory_test

import (
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestABashCallWithoutMemoryNeverLocatesTheStore(t *testing.T) {
	cases := []struct {
		name    string
		agentID string
	}{
		{"from the main thread", ""},
		{"from a full subagent", "afull000000000003"},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			h := newHarness(t)
			checkout := &test.CountedCommonDir{Lookup: test.GitDirOf{}}
			h.checkout = checkout
			h.must("", "claim", "main")

			if each.agentID != "" {
				h.spawned(each.agentID, "agentic-helpers: full")
			}

			before := len(h.git.Calls()) + checkout.Calls()

			answer := h.bashBy(each.agentID, "ls -la")
			if answer.HookSpecificOutput.HookEventName != "" {
				t.Errorf("a Bash call naming no agentic-memory should pass untouched, got %+v", answer)
			}

			if asked := len(h.git.Calls()) + checkout.Calls() - before; asked != 0 {
				t.Errorf("a Bash call naming no agentic-memory should be answered before the store is located, got %d asks",
					asked)
			}
		})
	}
}
