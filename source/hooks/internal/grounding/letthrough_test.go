package grounding_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/grounding"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func notebook(path string) map[string]any {
	return map[string]any{"session_id": "s1", "tool_name": "NotebookEdit", "tool_input": map[string]any{"notebook_path": path}}
}

func TestAGatedToolIsLetThroughWithANote(t *testing.T) {
	cases := []struct {
		name    string
		payload func(env grounding.Env) map[string]any
		draft   bool
	}{
		{name: "a question", payload: func(grounding.Env) map[string]any { return ask("s1") }},
		{name: "a draft written", payload: func(env grounding.Env) map[string]any { return save("Write", draftIn(t, env)) }, draft: true},
		{name: "a draft edited", payload: func(env grounding.Env) map[string]any { return edit(draftIn(t, env)) }, draft: true},
		{name: "a notebook edited under drafts", payload: func(env grounding.Env) map[string]any {
			return notebook(filepath.Join(env.ProjectDir, "drafts", "probe.ipynb"))
		}, draft: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := areaProject(t)
			got := run(t, env, "gate", tc.payload(env))

			if got.code != 0 || !got.noted || got.stderr != "" {
				t.Fatalf("a gated tool owing reads should go ahead with a note and no error, got exit %d, stdout %q, stderr %q",
					got.code, got.stdout, got.stderr)
			}

			if !strings.HasPrefix(got.said, "GROUNDING GATE: let through, reads owed before the next question or draft.") {
				t.Errorf("the note should say the call went ahead and the reads are owed before what comes next, got: %s", got.said)
			}

			if strings.Contains(got.said, "again") {
				t.Errorf("a call that went ahead should never be asked for again, got: %s", got.said)
			}

			if strings.Contains(got.said, "This draft is not grounded") != tc.draft {
				t.Errorf("only a draft's note should say its capture owes the reads, got: %s", got.said)
			}

			ground(t, env)
			passes(t, run(t, env, "gate", tc.payload(env)))
		})
	}
}

func TestAConfigErrorLetsAGatedToolThroughWithoutOwingReads(t *testing.T) {
	env := areaProject(t)
	ground(t, env)
	test.WriteFile(t, filepath.Join(env.ProjectDir, ".sdd", "grounding.yaml"), "listing_prefix: [unclosed")

	got := run(t, env, "gate", ask("s1"))
	if got.code != 0 || !got.noted || !strings.HasPrefix(got.said, "GROUNDING GATE: let through. ") ||
		strings.Contains(got.said, "reads owed") || !strings.Contains(got.said, "grounding.yaml") {
		t.Errorf("a broken config should let a question through naming the file, owing no reads, got exit %d: %s", got.code, got.said)
	}

	if strings.Contains(got.said, "again") {
		t.Errorf("a question that went ahead should never be asked for again, got: %s", got.said)
	}
}

func TestANoteNamesOnlyTheReadsOwed(t *testing.T) {
	cases := []struct {
		name   string
		reads  []string
		owed   []string
		credit []string
		hint   bool
	}{
		{
			name:   "only the listing missing",
			reads:  []string{semanticRead, literalRead},
			owed:   []string{"\nlisting of every topic\n  sdd view --layout \"active:as-counts\"\n"},
			credit: []string{"--query", "--term", "search"},
		},
		{
			name:   "only the literal search missing",
			reads:  []string{semanticRead, "sdd view --layout \"active:as-counts\""},
			owed:   []string{"\n`--term` search\n  sdd search --term '<literal>' --limit 5 --max-citations 0\n"},
			credit: []string{"--query", "listing of every topic", "view"},
			hint:   true,
		},
		{
			name: "every read missing",
			owed: []string{
				"\n`--query` search\n  sdd search --query '<subject>' --limit 5 --max-citations 0\n",
				"\n`--term` search\n  sdd search --term '<literal>' --limit 5 --max-citations 0\n",
				"\nlisting of every topic\n  sdd view --layout \"active:as-counts\"\n",
			},
			hint: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := bare(t)
			for _, read := range tc.reads {
				record(t, env, bash(read))
			}

			for _, got := range []result{run(t, env, "gate", ask("s1")), run(t, env, "gate", bash("sdd new d tac body"))} {
				refuses(t, got, tc.owed...)

				for _, credited := range tc.credit {
					if strings.Contains(got.said, credited) {
						t.Errorf("a read already credited should not be asked for, got: %s", got.said)
					}
				}

				if strings.Contains(got.said, "Empty search ≠ absence") != tc.hint {
					t.Errorf("the search hint should come only with a search owed, got: %s", got.said)
				}
			}
		})
	}
}

func TestARefusalAsksForTheCallAgain(t *testing.T) {
	got := run(t, bare(t), "gate", bash("sdd new d tac body"))

	if got.code != 2 || !strings.HasPrefix(got.stderr, "GROUNDING GATE: refused. Run the reads owed, then run this call again.\n") {
		t.Errorf("a refused call should owe the reads and ask for the call once they ran, got exit %d: %s", got.code, got.stderr)
	}
}

func TestWhatStillRefuses(t *testing.T) {
	cases := []struct {
		name    string
		payload func(env grounding.Env) map[string]any
	}{
		{name: "a capture", payload: func(env grounding.Env) map[string]any { return bash("agentic-capture " + draftIn(t, env)) }},
		{name: "a plain sdd new", payload: func(grounding.Env) map[string]any { return bash("sdd new d tac 'a body'") }},
		{name: "a discarded stream", payload: func(grounding.Env) map[string]any { return bash("sdd search --term x 2>/dev/null") }},
		{name: "a short show", payload: func(grounding.Env) map[string]any { return bash("sdd show 20260925-144721-d-tac-a9x") }},
		{name: "an MCP capture", payload: func(grounding.Env) map[string]any {
			return map[string]any{"session_id": "s1", "tool_name": "mcp__sdd__start_procedure", "tool_input": map[string]any{"canonical": "capture"}}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := areaProject(t)
			got := run(t, env, "gate", tc.payload(env))

			if got.code != 2 || got.noted || !strings.HasPrefix(got.stderr, "GROUNDING GATE: refused.") {
				t.Errorf("a call outside the gated tools should still be refused, got exit %d, stdout %q, stderr %q",
					got.code, got.stdout, got.stderr)
			}
		})
	}
}

func TestADraftWrittenWhileReadsAreOwedIsNotGrounded(t *testing.T) {
	env := areaProject(t)
	draft := draftIn(t, env)

	run(t, env, "gate", save("Write", draft))
	record(t, env, save("Write", draft))
	ground(t, env)
	compact(t, env)

	owesEveryRead(t, run(t, env, "gate", bash("agentic-capture "+draft)))
}

func TestTheNoteFiresOncePerCause(t *testing.T) {
	cases := []struct {
		name    string
		between func(t *testing.T, env grounding.Env)
		rearms  bool
	}{
		{name: "the same reads missing under the same graph", between: func(*testing.T, grounding.Env) {}},
		{name: "a graph change", between: func(t *testing.T, env grounding.Env) {
			entryIn(t, env, "26-090000-d-tac-new.md")
		}, rearms: true},
		{name: "a different set of reads missing", between: func(t *testing.T, env grounding.Env) {
			record(t, env, bash(semanticRead))
		}, rearms: true},
		{name: "a compaction", between: compact, rearms: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := areaProject(t)
			draft := draftIn(t, env)

			first := run(t, env, "gate", ask("s1"))
			if !first.noted {
				t.Fatalf("the first gated call owing reads should carry the note, got exit %d: %s", first.code, first.said)
			}

			tc.between(t, env)

			for position, payload := range []map[string]any{ask("s1"), save("Write", draft)} {
				got := run(t, env, "gate", payload)

				if got.code != 0 || got.stderr != "" {
					t.Fatalf("a gated call owing reads should go ahead, got exit %d, stderr %q", got.code, got.stderr)
				}

				rearmed := tc.rearms && position == 0
				if got.noted != rearmed {
					t.Errorf("the note should fire again only on a new cause, and once, got noted %v: %s", got.noted, got.said)
				}

				if rearmed && !strings.HasPrefix(got.said, "GROUNDING GATE: let through, reads owed before the next question or draft.") {
					t.Errorf("a re-armed note should keep its wording, got: %s", got.said)
				}
			}
		})
	}
}

func TestACaptureIsRefusedOnEveryCallAfterTheNote(t *testing.T) {
	env := areaProject(t)
	draft := draftIn(t, env)

	if !run(t, env, "gate", ask("s1")).noted {
		t.Fatal("the first gated call owing reads should carry the note")
	}

	passesSilently := run(t, env, "gate", ask("s1"))
	if passesSilently.noted {
		t.Fatalf("a second question with the same cause should carry no note, got: %s", passesSilently.said)
	}

	for range 2 {
		got := run(t, env, "gate", bash("agentic-capture "+draft))
		if got.code != 2 || !strings.HasPrefix(got.stderr, "GROUNDING GATE: refused. Run the reads owed, then run this call again.\n") {
			t.Errorf("a capture should be refused with the full text on every call, got exit %d: %s", got.code, got.stderr)
		}

		owesEveryRead(t, got)
	}
}

func TestTheSkillAndDocsStateTheNoteFiresOncePerCause(t *testing.T) {
	for _, path := range []string{
		"../../../../plugin/skills/agent-grounding/SKILL.md",
		"../../../../docs/grounding-gate/usage.md",
		"../../../../docs/grounding-gate/architecture.md",
	} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}

		prose := strings.Join(strings.Fields(string(content)), " ")

		if !strings.Contains(prose, "fires once per cause") {
			t.Errorf("%s should state that the let-through note fires once per cause", path)
		}

		if strings.Contains(prose, "every gated call while reads are owed") {
			t.Errorf("%s should no longer say the note comes on every gated call", path)
		}
	}
}
