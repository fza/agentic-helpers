package memory_test

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestANoteIsToldAtTheFileItIsAbout(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	note := h.file("Gate reads only Bash today", "the gate skips MCP calls", "--about", "internal/gate/gate.go")
	todo := h.file("Readme owes the install line", "add the go install line", "--kind", "todo", "--about", "README.md")

	first := h.callTool("Edit", map[string]string{"file_path": filepath.Join(h.repo, "internal", "gate", "gate.go")})
	if !strings.Contains(first, "agentic-memory: about internal/gate/gate.go: "+note+" Gate reads only Bash today  [") {
		t.Errorf("a call touching a path a note declares should tell the note:\n%s", first)
	}

	if again := h.callTool("Read", map[string]string{"file_path": filepath.Join(h.repo, "internal", "gate", "gate.go")}); strings.Contains(again, note) {
		t.Errorf("a note should be told once per session:\n%s", again)
	}

	if out := h.callTool("Bash", map[string]string{"command": "cat README.md"}); !strings.Contains(out, "about README.md: "+todo) {
		t.Errorf("a todo should be told at the path it declares as a note is:\n%s", out)
	}

	h.must(hookPayload("compact", "", ""), "session-start")

	if after := h.callTool("Read", map[string]string{"file_path": filepath.Join(h.repo, "internal", "gate", "gate.go")}); !strings.Contains(after, "about internal/gate/gate.go: "+note) {
		t.Errorf("a compaction should let the note be told again:\n%s", after)
	}
}

func TestANotePathMentionedOnlyInItsBodyTellsNothing(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("Gate note without a declared path", "editing internal/gate/gate.go changes the hook")

	if out := h.callTool("Read", map[string]string{"file_path": filepath.Join(h.repo, "internal", "gate", "gate.go")}); strings.Contains(out, "about ") {
		t.Errorf("a path the body only mentions should never tell its note:\n%s", out)
	}
}

func TestACallTouchingSeveralNotesTellsTheHottestOnly(t *testing.T) {
	for _, warmed := range []struct {
		name  string
		first bool
	}{{"first filed warmed", true}, {"last filed warmed", false}} {
		t.Run(warmed.name, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")
			first := h.file("Gate first note here", "first", "--about", "internal/gate/gate.go")
			last := h.file("Gate last note here", "last", "--about", "internal/gate/gate.go")

			hot, cold := last, first
			if warmed.first {
				hot, cold = first, last
			}

			// The index is built at the first call and keeps its order until the
			// log changes; a show warms without a log event, so only the heat
			// read at the call can pick the warmed note.
			h.callTool("Read", map[string]string{"file_path": filepath.Join(h.repo, "README.md")})
			h.must("", "show", hot)

			out := h.callTool("Read", map[string]string{"file_path": filepath.Join(h.repo, "internal", "gate", "gate.go")})
			if strings.Count(out, "agentic-memory: about ") != 1 || !strings.Contains(out, hot) || strings.Contains(out, cold) {
				t.Errorf("a call touching several notes should tell only the hottest:\n%s", out)
			}

			if next := h.callTool("Read", map[string]string{"file_path": filepath.Join(h.repo, "internal", "gate", "gate.go")}); !strings.Contains(next, cold) {
				t.Errorf("the note left untold should be told at the next call touching its path:\n%s", next)
			}
		})
	}
}

func TestTellingANoteAddsNoHeat(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	warmed := h.file("Readme note warmed by a show", "body")
	h.must("", "show", warmed)
	note := h.file("Gate note told at a call", "body", "--about", "internal/gate/gate.go")

	h.callTool("Read", map[string]string{"file_path": filepath.Join(h.repo, "internal", "gate", "gate.go")})

	if !h.heatOf(note).Used.IsZero() {
		t.Errorf("being told at a call should add no heat to a note")
	}

	h.must("", "show", note)

	if h.heatOf(note).Used.IsZero() {
		t.Errorf("a show after the notice should warm the note")
	}
}

func TestAReceivedNoteIsToldAtTheFileItIsAbout(t *testing.T) {
	h, other := twoSeats(t)
	note := other.file("Gate note for the main seat", "body", "--about", "internal/gate/gate.go", "--for", "main")

	out := h.callTool("Read", map[string]string{"file_path": filepath.Join(h.repo, "internal", "gate", "gate.go")})
	if !strings.Contains(out, "about internal/gate/gate.go: "+note) {
		t.Errorf("a note received through --for should be told as the seat's own:\n%s", out)
	}
}
