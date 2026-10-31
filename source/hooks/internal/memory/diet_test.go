package memory_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestTheBriefPrintsShortLines(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	id := h.file("Gate finding in the brief", "finding")

	brief := h.must("", "brief")
	if !regexp.MustCompile(`(?m)^  ` + id + ` Gate finding in the brief  \[0m\]$`).MatchString(brief) {
		t.Errorf("the brief should print the short line:\n%s", brief)
	}

	if out := h.must("", "search", "--term", "in the brief", "--again"); !strings.Contains(out, "[main, note, from agent, ") {
		t.Errorf("search should keep the full line:\n%s", out)
	}
}

func TestTheBriefCapsItsNotes(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	test.WriteFile(t, h.settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"brief_note_limit":3}}}}`)

	for index := range 5 {
		h.file(fmt.Sprintf("Gate note number %d here", index), "note")
		h.file(fmt.Sprintf("Gate trap number %d here", index), "trap", "--kind", "trap")
		h.file(fmt.Sprintf("Gate todo number %d here", index), "todo", "--kind", "todo")
	}

	brief := h.must("", "brief")

	if strings.Count(brief, "Gate note number") != 3 || !strings.Contains(brief, "+2 more notes: agentic-memory brief --kind note") {
		t.Errorf("the brief should print the notes up to the limit and name the rest:\n%s", brief)
	}

	if strings.Count(brief, "Gate trap number") != 5 || strings.Count(brief, "Gate todo number") != 5 {
		t.Errorf("traps and todos should print whole:\n%s", brief)
	}

	if notes := h.must("", "brief", "--kind", "note"); strings.Count(notes, "Gate note number") != 5 || strings.Contains(notes, "trap") {
		t.Errorf("brief --kind should list one kind's live entries whole:\n%s", notes)
	}

	h.file("Hook finding off the gate", "hooks", "--topic", "hooks")

	if topical := mcpText(t, h.mcp(`{"name":"brief","arguments":{"topic":["hooks"]}}`)); !strings.Contains(topical, "Hook finding off the gate") ||
		strings.Contains(topical, "Gate note number") {
		t.Errorf("the MCP key topic should list those topics' entries: %s", topical)
	}

	answer := mcpText(t, h.mcp(`{"name":"brief","arguments":{"kind":"trap"}}`))
	if strings.Count(answer, "Gate trap number") != 5 || strings.Contains(answer, "Gate note number") {
		t.Errorf("the MCP key kind should list one kind's entries: %s", answer)
	}

	for _, args := range [][]string{{"brief", "--kind", "finding"}, {"brief", "--kind", "finding", "--json"}} {
		if got := h.run("", args...); got.code != 1 || !strings.Contains(got.err, "--kind takes rule, trap, todo, note") {
			t.Errorf("%v should refuse an unknown kind, got %d %q", args, got.code, got.err)
		}
	}
}

func TestInboundLinksLeaveOutSuperseded(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	hub := h.file("Gate hub finding linked to", "hub")
	first := h.file("Gate state snapshot one", "cites "+hub)
	second := h.file("Gate state snapshot two", "cites "+hub)
	h.must("", "supersede", first, "--by", second)
	retired := h.file("Gate retired citation", "cites "+hub)
	h.must("", "retire", retired)

	shown := h.must("", "show", hub)
	if strings.Contains(shown, "  "+first+" ") || !strings.Contains(shown, "  "+second+" ") || !strings.Contains(shown, "  "+retired+" ") {
		t.Errorf("linked from should leave out superseded entries and keep retired ones:\n%s", shown)
	}

	if !strings.Contains(h.must("", "search", "--term", "hub finding", "--again"), "← 2") {
		t.Errorf("the inbound count should leave out superseded entries:\n%s", shown)
	}

	if all := h.must("", "show", hub, "--with-superseded", "--again"); !strings.Contains(all, "  "+first+" ") {
		t.Errorf("--with-superseded should list superseded entries too:\n%s", all)
	}

	answer := h.mcp(`{"name":"show","arguments":{"ids":["` + hub + `"],"with_superseded":true,"again":true}}`)
	if !strings.Contains(answer, first) {
		t.Errorf("the MCP key with_superseded should list them: %s", answer)
	}

	if brief := h.must("", "brief"); strings.Contains(brief, "← "+first) {
		t.Errorf("the brief's citing lines should leave out superseded entries:\n%s", brief)
	}
}

func TestTheNotesCapKeepsTheHottest(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	test.WriteFile(t, h.settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"brief_note_limit":2}}}}`)

	ids := make([]string, 0, 5)
	for index := range 5 {
		ids = append(ids, h.file(fmt.Sprintf("Gate note number %d here", index), "note"))
	}

	for _, used := range []string{ids[1], ids[3]} {
		h.must("", "confirm", used)
	}

	brief := h.must("", "brief")

	for index, id := range ids {
		kept := index == 1 || index == 3
		if strings.Contains(brief, id) != kept {
			t.Errorf("the capped notes should be the hottest ones, %s kept %v:\n%s", id, kept, brief)
		}
	}
}
