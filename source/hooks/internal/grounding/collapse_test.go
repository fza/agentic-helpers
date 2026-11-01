package grounding_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/grounding"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const session = "11111111-2222-3333-4444-555555555555"

// sddCall is the PostToolUse payload of one sdd MCP tool answering the
// fixture named, called by agent, the main thread where empty.
func sddCall(t *testing.T, transcriptDir string, tool string, fixture string, agent string) map[string]any {
	t.Helper()

	payload := map[string]any{
		"session_id":      session,
		"tool_name":       "mcp__sdd__" + tool,
		"tool_response":   json.RawMessage(test.Fixture(t, "collapse/"+fixture)),
		"transcript_path": filepath.Join(transcriptDir, session+".jsonl"),
	}

	if agent != "" {
		payload["agent_id"] = agent
	}

	return payload
}

// collapsedText is the answer the agent is handed in place of sdd's, empty
// where the hook leaves sdd's answer as it is.
func collapsedText(t *testing.T, got result) string {
	t.Helper()

	if got.code != 0 {
		t.Fatalf("collapsing should never fail the call, got %+v", got)
	}

	if got.stdout == "" {
		return ""
	}

	var answer struct {
		HookSpecificOutput struct {
			HookEventName string `json:"hookEventName"`
			Updated       struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"updatedMCPToolOutput"`
		} `json:"hookSpecificOutput"`
	}

	err := json.Unmarshal([]byte(got.stdout), &answer)
	if err != nil || answer.HookSpecificOutput.HookEventName != "PostToolUse" || len(answer.HookSpecificOutput.Updated.Content) != 1 {
		t.Fatalf("the hook should answer one replacement text block, got %s (%v)", got.stdout, err)
	}

	return answer.HookSpecificOutput.Updated.Content[0].Text
}

func collapses(t *testing.T, got result) {
	t.Helper()

	if !strings.Contains(collapsedText(t, got), `"shown_earlier":true`) {
		t.Errorf("the answer should collapse what the agent was shown, got %s", got.stdout)
	}
}

func printsWhole(t *testing.T, got result) {
	t.Helper()

	if collapsedText(t, got) != "" {
		t.Errorf("the answer should reach the agent as sdd gave it, got %s", got.stdout)
	}
}

func TestARepeatedShowCollapsesForTheSameAgent(t *testing.T) {
	env, transcripts := savingOff(t, bare(t)), t.TempDir()

	printsWhole(t, run(t, env, "collapse", sddCall(t, transcripts, "show", "show.json", "")))
	collapses(t, run(t, env, "collapse", sddCall(t, transcripts, "show", "show.json", "")))
}

func TestAViewCollapsesWhatAShowPrinted(t *testing.T) {
	env, transcripts := savingOff(t, bare(t)), t.TempDir()

	printsWhole(t, run(t, env, "collapse", sddCall(t, transcripts, "view", "view-text.json", "")))
	printsWhole(t, run(t, env, "collapse", sddCall(t, transcripts, "view", "view.json", "")))
	collapses(t, run(t, env, "collapse", sddCall(t, transcripts, "show", "show.json", "")))
	collapses(t, run(t, env, "collapse", sddCall(t, transcripts, "view", "view.json", "")))
}

func TestAnAnswerCollapsingNothingStillGrowsTheSet(t *testing.T) {
	cases := []struct {
		name  string
		tool  string
		first string
		next  string
	}{
		{name: "a new line", tool: "search", first: "search.json", next: "search-more.json"},
		{name: "a changed line", tool: "search", first: "search.json", next: "search-changed.json"},
		{name: "a new piece", tool: "next", first: "next.json", next: "next-more.json"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			env, transcripts := savingOff(t, bare(t)), t.TempDir()

			printsWhole(t, run(t, env, "collapse", sddCall(t, transcripts, testCase.tool, testCase.first, "")))
			printsWhole(t, run(t, env, "collapse", sddCall(t, transcripts, testCase.tool, testCase.next, "")))
			collapses(t, run(t, env, "collapse", sddCall(t, transcripts, testCase.tool, testCase.next, "")))
		})
	}
}

func TestAFreshSubagentKeepsASetOfItsOwn(t *testing.T) {
	env, transcripts := savingOff(t, bare(t)), t.TempDir()

	run(t, env, "collapse", sddCall(t, transcripts, "show", "show.json", ""))
	printsWhole(t, run(t, env, "collapse", sddCall(t, transcripts, "show", "show.json", "a1")))

	run(t, env, "collapse", sddCall(t, transcripts, "search", "search.json", "a1"))

	got := collapsedText(t, run(t, env, "collapse", sddCall(t, transcripts, "search", "search.json", "")))
	if !strings.Contains(got, "Savings came from removing repetition") {
		t.Errorf("a subagent's read should never collapse anything for its parent, got %s", got)
	}
}

func TestAForkStartsFromItsSpawnersSet(t *testing.T) {
	env, transcripts := savingOff(t, bare(t)), t.TempDir()
	test.WriteFile(t, filepath.Join(transcripts, session, "subagents", "agent-a2.meta.json"), `{"toolUseId":"toolu_01"}`)

	run(t, env, "collapse", sddCall(t, transcripts, "show", "show.json", ""))
	run(t, env, "spawn", map[string]any{
		"session_id": session, "tool_name": "Agent", "tool_use_id": "toolu_01",
		"tool_input": map[string]any{"subagent_type": "fork", "prompt": "go"},
	})
	run(t, env, "collapse", sddCall(t, transcripts, "search", "search.json", ""))

	collapses(t, run(t, env, "collapse", sddCall(t, transcripts, "show", "show.json", "a2")))

	got := collapsedText(t, run(t, env, "collapse", sddCall(t, transcripts, "search", "search.json", "a2")))
	if !strings.Contains(got, "Savings came from removing repetition") {
		t.Errorf("a fork should get in full what its spawner read after the spawn, got %s", got)
	}

	leftover, _ := filepath.Glob(filepath.Join(env.ProjectDir, ".sdd", "tmp", "grounding-gate", "*spawn-*"))
	if len(leftover) != 0 {
		t.Errorf("a fork's first answer should spend what its spawn kept, left %v", leftover)
	}
}

func TestOnlyAFreshSpawnSkipsTheSpawnersSet(t *testing.T) {
	env, transcripts := savingOff(t, bare(t)), t.TempDir()
	test.WriteFile(t, filepath.Join(transcripts, session, "subagents", "agent-a3.meta.json"), `{"toolUseId":"toolu_02"}`)

	run(t, env, "collapse", sddCall(t, transcripts, "show", "show.json", ""))
	run(t, env, "spawn", map[string]any{
		"session_id": session, "tool_name": "Agent", "tool_use_id": "toolu_02",
		"tool_input": map[string]any{"subagent_type": "general-purpose", "prompt": "go"},
	})

	printsWhole(t, run(t, env, "collapse", sddCall(t, transcripts, "show", "show.json", "a3")))
}

func TestTheSetEmptiesWhereTheContextDoes(t *testing.T) {
	for source, empties := range map[string]bool{"compact": true, "clear": true, "resume": true, "startup": false} {
		t.Run(source, func(t *testing.T) {
			env, transcripts := savingOff(t, bare(t)), t.TempDir()

			run(t, env, "collapse", sddCall(t, transcripts, "show", "show.json", ""))
			run(t, env, "start", map[string]any{"session_id": session, "source": source})

			got := run(t, env, "collapse", sddCall(t, transcripts, "show", "show.json", ""))
			if empties {
				printsWhole(t, got)
			} else {
				collapses(t, got)
			}
		})
	}
}

func TestTheSessionEndRemovesItsSets(t *testing.T) {
	env, transcripts := savingOff(t, bare(t)), t.TempDir()

	run(t, env, "collapse", sddCall(t, transcripts, "show", "show.json", ""))
	run(t, env, "collapse", sddCall(t, transcripts, "show", "show.json", "a1"))
	run(t, env, "end", map[string]any{"session_id": session})

	left, _ := filepath.Glob(filepath.Join(env.ProjectDir, ".sdd", "tmp", "grounding-gate", "*.shown.json"))
	if len(left) != 0 {
		t.Errorf("a session's end should remove its shown sets, left %v", left)
	}
}

func TestAFailedCallIsLeftAlone(t *testing.T) {
	env, transcripts := savingOff(t, bare(t)), t.TempDir()

	run(t, env, "collapse", sddCall(t, transcripts, "show", "show.json", ""))

	var answer map[string]any

	err := json.Unmarshal([]byte(test.Fixture(t, "collapse/show.json")), &answer)
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}

	answer["is_error"] = true
	failed := sddCall(t, transcripts, "show", "show.json", "")
	failed["tool_response"] = answer

	printsWhole(t, run(t, env, "collapse", failed))
}

func TestOnlySddsToolsAndKnownSessionsCollapse(t *testing.T) {
	for name, change := range map[string]func(map[string]any){
		"another server's tool": func(payload map[string]any) { payload["tool_name"] = "mcp__other__show" },
		"no session":            func(payload map[string]any) { payload["session_id"] = "" },
	} {
		t.Run(name, func(t *testing.T) {
			env, transcripts := savingOff(t, bare(t)), t.TempDir()

			first, second := sddCall(t, transcripts, "next", "next.json", ""), sddCall(t, transcripts, "next", "next.json", "")
			change(first)
			change(second)

			run(t, env, "collapse", first)
			printsWhole(t, run(t, env, "collapse", second))
		})
	}
}

func TestCollapsingFollowsTheGatesMachineSwitchOnly(t *testing.T) {
	t.Run("gate off on the machine", func(t *testing.T) {
		env, transcripts := savingOff(t, withOptions(t, bare(t), `"grounding_gate_enabled":false`)), t.TempDir()

		run(t, env, "collapse", sddCall(t, transcripts, "show", "show.json", ""))
		printsWhole(t, run(t, env, "collapse", sddCall(t, transcripts, "show", "show.json", "")))
	})

	t.Run("gate lifted for the project", func(t *testing.T) {
		env, transcripts := savingOff(t, configured(t, "disabled.yaml")), t.TempDir()

		run(t, env, "collapse", sddCall(t, transcripts, "show", "show.json", ""))
		collapses(t, run(t, env, "collapse", sddCall(t, transcripts, "show", "show.json", "")))
	})
}

func TestAProjectWithoutAGraphGetsNothing(t *testing.T) {
	root := t.TempDir()
	env := grounding.Env{ProjectDir: root, WorkingDir: root, Home: t.TempDir(), Runner: &test.Runner{}}
	transcripts := t.TempDir()

	run(t, env, "collapse", sddCall(t, transcripts, "show", "show.json", ""))
	printsWhole(t, run(t, env, "collapse", sddCall(t, transcripts, "show", "show.json", "")))

	_, err := os.Stat(filepath.Join(root, ".sdd"))
	if err == nil {
		t.Error("collapsing should create nothing in a project without a graph")
	}
}

// savingOff is env after the session's start, where sdd's
// `sessions.token_saving` reads off.
func savingOff(t *testing.T, env grounding.Env) grounding.Env {
	t.Helper()

	return startedWith(t, env, "off", "startup")
}

func startedWith(t *testing.T, env grounding.Env, saving string, source string) grounding.Env {
	t.Helper()

	env.SddValues = &test.SddValues{Values: map[string]string{"sessions.token_saving": saving}}
	run(t, env, "start", map[string]any{"session_id": session, "source": source})

	return env
}

func TestCollapsingRunsOnlyWhereSddsTokenSavingIsOff(t *testing.T) {
	for saving, runs := range map[string]bool{"off": true, "on": false, "": false, "Off": false} {
		t.Run("token_saving "+saving, func(t *testing.T) {
			env, transcripts := startedWith(t, bare(t), saving, "startup"), t.TempDir()

			run(t, env, "collapse", sddCall(t, transcripts, "show", "show.json", ""))

			got := run(t, env, "collapse", sddCall(t, transcripts, "show", "show.json", ""))
			if runs {
				collapses(t, got)
			} else {
				printsWhole(t, got)
			}
		})
	}

	t.Run("no sdd to ask", func(t *testing.T) {
		env, transcripts := bare(t), t.TempDir()
		run(t, env, "start", map[string]any{"session_id": session, "source": "startup"})

		run(t, env, "collapse", sddCall(t, transcripts, "show", "show.json", ""))
		printsWhole(t, run(t, env, "collapse", sddCall(t, transcripts, "show", "show.json", "")))
	})
}

func TestASpawnKeepsNothingWhereTokenSavingIsOn(t *testing.T) {
	env := startedWith(t, bare(t), "on", "startup")

	run(t, env, "spawn", map[string]any{
		"session_id": session, "tool_name": "Agent", "tool_use_id": "toolu_03",
		"tool_input": map[string]any{"subagent_type": "fork", "prompt": "go"},
	})

	kept, _ := filepath.Glob(filepath.Join(env.ProjectDir, ".sdd", "tmp", "grounding-gate", "*spawn-*"))
	if len(kept) != 0 {
		t.Errorf("a spawn should keep no set for a session sdd stubs itself, kept %v", kept)
	}
}

func TestTokenSavingTurnedOnStopsCollapsingAtTheNextStart(t *testing.T) {
	env, transcripts := savingOff(t, bare(t)), t.TempDir()

	run(t, env, "collapse", sddCall(t, transcripts, "show", "show.json", ""))
	env = startedWith(t, env, "on", "resume")

	run(t, env, "collapse", sddCall(t, transcripts, "show", "show.json", ""))
	printsWhole(t, run(t, env, "collapse", sddCall(t, transcripts, "show", "show.json", "")))
}

func TestAProjectLiftingTheGateStillEmptiesTheSetAtCompaction(t *testing.T) {
	env, transcripts := savingOff(t, configured(t, "disabled.yaml")), t.TempDir()

	run(t, env, "collapse", sddCall(t, transcripts, "show", "show.json", ""))
	run(t, env, "start", map[string]any{"session_id": session, "source": "compact"})

	printsWhole(t, run(t, env, "collapse", sddCall(t, transcripts, "show", "show.json", "")))
}
