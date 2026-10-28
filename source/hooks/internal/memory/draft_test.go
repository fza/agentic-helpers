package memory_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func (h *harness) promptSaying(prompt string) string {
	data, _ := json.Marshal(map[string]string{"session_id": h.session, "prompt": prompt})

	return string(data)
}

// shipWithoutState is a seated session that committed, wrote a file and was
// prompted, without writing its state.
func shipWithoutState(t *testing.T) *harness {
	t.Helper()

	h := newHarness(t)
	h.must("", "claim", "main")
	h.must(h.promptSaying("fix the gate for MCP reads"), "prompt")
	h.callTool("Edit", map[string]string{"file_path": filepath.Join(h.repo, "internal", "gate", "gate.go")})
	h.callTool("Write", map[string]string{"file_path": filepath.Join(h.repo, "internal", "gate", "gate.go")})
	h.git.Commit = "9c0ffee1"
	h.git.Log = map[string]string{"15af1a8c..HEAD": "9c0ffee1 fix: the gate reads MCP calls\n"}

	return h
}

const wantDraft = "commits since the claim: 9c0ffee1 fix: the gate reads MCP calls\n" +
	"files written: internal/gate/gate.go\nlast prompt: fix the gate for MCP reads"

func TestASessionThatNeverWritesStateLeavesADraft(t *testing.T) {
	cases := []struct {
		name string
		hook func(h *harness) ran
	}{
		{name: "at Stop", hook: func(h *harness) ran { return h.run(h.stop(), "stop") }},
		{name: "at SessionEnd", hook: func(h *harness) ran { return h.run(h.sessionEnd("prompt_input_exit"), "session-end") }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := shipWithoutState(t)

			got := tc.hook(h)
			if got.out != "" || strings.Contains(got.err, "draft") {
				t.Errorf("writing a draft should print nothing, got %q %q", got.out, got.err)
			}

			h.must("", "claim", "main")

			if out := h.must("", "brief"); !strings.Contains(out, "branch main, hook draft): "+wantDraft) {
				t.Errorf("the brief should open on the hook's draft, marked, got:\n%s", out)
			}

			var brief struct {
				State struct {
					Body  string `json:"body"`
					Draft bool   `json:"draft"`
				} `json:"state"`
			}

			err := json.Unmarshal([]byte(h.must("", "brief", "--json")), &brief)
			if err != nil || !brief.State.Draft || brief.State.Body != wantDraft {
				t.Errorf("brief --json should carry the draft, marked, got %+v, %v", brief.State, err)
			}
		})
	}
}

func TestADraftNeverReplacesAStateWrittenSinceTheClaim(t *testing.T) {
	h := shipWithoutState(t)
	h.must("the gate reads MCP calls; next: docs", "state")

	before := h.logBytes()
	h.must(h.stop(), "stop")

	if !h.logUnchanged(before) {
		t.Error("a Stop after the agent's own state write should write no draft row")
	}

	if out := h.must("", "brief"); !strings.Contains(out, "): the gate reads MCP calls; next: docs") || strings.Contains(out, "hook draft") {
		t.Errorf("a state the agent wrote since the claim should stand, got:\n%s", out)
	}
}

func TestTheAgentsStateWriteReplacesADraft(t *testing.T) {
	h := shipWithoutState(t)
	h.run(h.stop(), "stop")
	h.must("the gate reads MCP calls; next: docs", "state")
	h.must(h.stop(), "stop")

	if out := h.must("", "brief"); !strings.Contains(out, "): the gate reads MCP calls; next: docs") || strings.Contains(out, "hook draft") {
		t.Errorf("the agent's state write should replace the draft, got:\n%s", out)
	}
}

func TestADraftCountsForNoStateWrite(t *testing.T) {
	t.Run("the shipped-work hold", func(t *testing.T) {
		h := newHarness(t)
		h.must("", "claim", "main")
		h.must(h.promptSaying("rework the gate"), "prompt")
		h.writeFiles(4)

		if h.stopHeld() {
			t.Fatal("four writes should not hold")
		}

		h.writeFiles(1)

		if !h.stopHeld() {
			t.Error("the draft written at the last Stop should not reset the writes counted toward the hold")
		}
	})

	t.Run("the compaction nudge", func(t *testing.T) {
		h := newHarness(t)
		h.must("", "claim", "main")
		h.sensor(70)

		if !strings.Contains(h.must(h.promptSaying("rework the gate"), "prompt"), "Context near compaction") {
			t.Fatal("a nudge should fire past 65% of the window")
		}

		h.must(h.stop(), "stop")

		if !strings.Contains(h.must(h.promptSaying("go on"), "prompt"), "Context near compaction") {
			t.Error("a draft should not answer the compaction nudge")
		}
	})
}

func TestAnUnchangedDraftIsNotWrittenAgain(t *testing.T) {
	h := shipWithoutState(t)
	h.run(h.stop(), "stop")

	before := h.logBytes()
	h.run(h.stop(), "stop")

	if !h.logUnchanged(before) {
		t.Error("a Stop finding the draft unchanged should write nothing")
	}
}

func TestADraftKeepsTheUsersLastPromptOverClaudeCodesOwnTurns(t *testing.T) {
	cases := []struct {
		name string
		turn string
	}{
		{name: "a background task's notification", turn: "<task-notification>\n<summary>Stop hook feedback</summary>\n</task-notification>"},
		{name: "a local command's caveat", turn: "<local-command-caveat>The command below was run directly</local-command-caveat>"},
		{name: "a local command's output", turn: "  <local-command-stdout>Updated agentic-helpers.</local-command-stdout>"},
		{name: "a shell command's output", turn: "<bash-stdout>clean</bash-stdout>"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := shipWithoutState(t)
			h.must(h.promptSaying(tc.turn), "prompt")
			h.run(h.stop(), "stop")

			out := h.must("", "brief")
			if !strings.Contains(out, "last prompt: fix the gate for MCP reads") {
				t.Errorf("a turn Claude Code sent should not stand as the last prompt, got:\n%s", out)
			}
		})
	}
}

func TestADraftTakesAPromptMentioningATagInside(t *testing.T) {
	h := shipWithoutState(t)
	h.must(h.promptSaying("why does <task-notification> reach the prompt hook"), "prompt")
	h.run(h.stop(), "stop")

	if out := h.must("", "brief"); !strings.Contains(out, "last prompt: why does <task-notification> reach the prompt hook") {
		t.Errorf("a prompt only mentioning a tag is the user's, got:\n%s", out)
	}
}

func TestADraftNeverCarriesAPromptHoldingACredential(t *testing.T) {
	credential := test.SecretFixture(t, "positive/github-pat.txt")
	h := shipWithoutState(t)
	h.must(h.promptSaying("deploy with this\n"+credential), "prompt")
	h.run(h.stop(), "stop")

	out := h.must("", "brief")
	if strings.Contains(out, credential) || !strings.Contains(out, "last prompt: fix the gate for MCP reads") {
		t.Errorf("a prompt holding a credential should never reach the draft, got:\n%s", out)
	}
}

func TestADraftKeepsToTheWordLimit(t *testing.T) {
	h := shipWithoutState(t)
	test.WriteFile(t, h.settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"entry_max_words":6}}}}`)
	h.run(h.stop(), "stop")

	if out := h.must("", "brief"); !strings.Contains(out, "hook draft): commits since the claim: 9c0ffee1 fix:\n") {
		t.Errorf("a draft should be cut to entry_max_words, got:\n%s", out)
	}
}
