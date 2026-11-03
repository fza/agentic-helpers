package memory

import (
	"testing"
)

func TestRewritePrompt(t *testing.T) {
	block := []string{"agentic-helpers level: silent", "scratch: .tmp/seats/main/"}

	cases := []struct {
		name   string
		prompt string
		want   string
	}{
		{"the marker gives way to the block", "Context.\nagentic-helpers: silent\nTask.",
			"Context.\nagentic-helpers level: silent\nscratch: .tmp/seats/main/\nTask."},
		{"no marker puts the block above", "Task.",
			"agentic-helpers level: silent\nscratch: .tmp/seats/main/\n\nTask."},
		{"a later marker stays as text", "agentic-helpers: silent\nTask.\n  agentic-helpers: full  ",
			"agentic-helpers level: silent\nscratch: .tmp/seats/main/\nTask.\n  agentic-helpers: full  "},
		{"a level line after the marker goes", "agentic-helpers: silent\nTask.\nagentic-helpers level: full",
			"agentic-helpers level: silent\nscratch: .tmp/seats/main/\nTask."},
		{"a level line the prompt carries goes", "agentic-helpers level: full, seat main\nagentic-helpers: silent\nTask.",
			"agentic-helpers level: silent\nscratch: .tmp/seats/main/\nTask."},
		{"a marker naming no level stays", "agentic-helpers: maximal\nTask.",
			"agentic-helpers level: silent\nscratch: .tmp/seats/main/\n\nagentic-helpers: maximal\nTask."},
		{"a prompt the hook already rewrote stays", "agentic-helpers level: silent\nscratch: .tmp/seats/main/\n\nTask.",
			"agentic-helpers level: silent\nscratch: .tmp/seats/main/\n\nTask."},
		{"the block where a marker stood stays", "Context.\nagentic-helpers level: silent\nscratch: .tmp/seats/main/\nTask.",
			"Context.\nagentic-helpers level: silent\nscratch: .tmp/seats/main/\nTask."},
		{"a part of the block is no block", "scratch: .tmp/seats/main/\nTask.",
			"agentic-helpers level: silent\nscratch: .tmp/seats/main/\n\nscratch: .tmp/seats/main/\nTask."},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			if got := rewritePrompt(each.prompt, block); got != each.want {
				t.Errorf("the rewrite should place the block at the first marker alone, got:\n%q", got)
			}
		})
	}
}

func TestRequestedLevel(t *testing.T) {
	cases := []struct {
		prompt string
		want   level
		marked bool
	}{
		{"Task.", levelNone, false},
		{"agentic-helpers: shared\nTask.", levelShared, true},
		{"Task.\n agentic-helpers:full", levelFull, true},
		{"the line agentic-helpers: full inside prose", levelNone, false},
		{"agentic-helpers: rules\nagentic-helpers: full", levelRules, true},
	}

	for _, each := range cases {
		got, marked := requestedLevel(each.prompt)
		if got != each.want || marked != each.marked {
			t.Errorf("the first marker line alone should name the level of %q, got %s %v", each.prompt, got, marked)
		}
	}
}

func TestMemoryCallsAndTheirFlag(t *testing.T) {
	cases := []struct {
		name    string
		command string
		verbs   []string
		nested  []bool
		flagged string
	}{
		{"a direct call", "agentic-memory new --title x", []string{"new"}, []bool{false},
			"agentic-memory --agent a1 new --title x"},
		{"a launched call", "agentic-helpers bound 30s agentic-memory state < s.md", []string{"state"}, []bool{false},
			"agentic-helpers bound 30s agentic-memory --agent a1 state < s.md"},
		{"a typed flag", "agentic-memory --agent other show m-1", []string{"show"}, []bool{false},
			"agentic-memory --agent a1 show m-1"},
		{"a typed flag with its value joined", "agentic-memory --agent=other show m-1", []string{"show"}, []bool{false},
			"agentic-memory --agent a1 show m-1"},
		{"a path to the program", "/opt/bin/agentic-memory brief", []string{"brief"}, []bool{false},
			"/opt/bin/agentic-memory --agent a1 brief"},
		{"code a shell runs", "bash -c 'agentic-memory retire m-1'", []string{"retire"}, []bool{true},
			"bash -c 'agentic-memory retire m-1'"},
		{"the name as text", "echo agentic-memory; grep -rn agentic-memory docs", nil, nil,
			"echo agentic-memory; grep -rn agentic-memory docs"},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			calls, parsed := memoryCalls(each.command, false)
			if !parsed {
				t.Fatal("the command should parse")
			}

			if len(calls) != len(each.verbs) {
				t.Fatalf("every agentic-memory call and only those should be found, got %+v", calls)
			}

			for index, held := range calls {
				if held.verb != each.verbs[index] || held.nested != each.nested[index] {
					t.Errorf("a call's verb and nesting should be read, got %+v", held)
				}
			}

			if got := flagCalls(each.command, calls, "a1"); got != each.flagged {
				t.Errorf("every direct call should carry the agent alone, got %q", got)
			}
		})
	}
}

func TestSubagentRefusal(t *testing.T) {
	cases := []struct {
		verb    string
		at      level
		refused bool
	}{
		{"search", levelNone, true},
		{"search", levelRules, true},
		{"search", levelSilent, false},
		{"who", levelShared, false},
		{"new", levelShared, true},
		{"", levelSilent, true},
		{"new", levelFull, false},
		{"state", levelFull, false},
		{"claim", levelFull, true},
		{"follow", levelFull, true},
	}

	for _, each := range cases {
		if refused := subagentRefusal(each.verb, each.at) != ""; refused != each.refused {
			t.Errorf("%q at %s should be refused %v", each.verb, each.at, each.refused)
		}
	}
}
