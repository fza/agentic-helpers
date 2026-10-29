package memory_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestViewExplainsTheBriefsFixedLayout(t *testing.T) {
	cases := []struct {
		name     string
		settings string
		notes    string
	}{
		{name: "the default note limit", notes: "n(10)"},
		{name: "a set note limit", settings: `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"brief_note_limit":3}}}}`, notes: "n(3)"},
		{name: "no notes at all", settings: `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"brief_note_limit":0}}}}`, notes: "n(0)"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")

			if tc.settings != "" {
				test.WriteFile(t, h.settings, tc.settings)
			}

			want := "brief, fixed: its first line, the state slot, the due section, changed by other seats and ended " +
				"since last brief; then these sections; then retire?, new from other seats and the topics. Nothing " +
				"configures it.\n" +
				`active:mine:kind(rule):not(due):rank(heat):name("rules (from the user, verify before relying)"):brief:as-list,` + "\n" +
				`active:mine:kind(trap):not(due):rank(heat):name("traps"):brief:as-list,` + "\n" +
				`active:mine:kind(todo):not(due):rank(heat):name("todos"):brief:as-list,` + "\n" +
				`active:mine:kind(note):not(due):rank(heat):` + tc.notes + `:name("notes"):brief:as-list`

			if got := strings.TrimSpace(h.must("", "view", "--explain", "brief")); got != want {
				t.Errorf("--explain brief should print the brief's fixed layout, its note limit the option's, got:\n%s", got)
			}
		})
	}
}

func TestTheBriefRunsThroughItsLayout(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	test.WriteFile(t, h.settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"brief_note_limit":0}}}}`)

	for index := range 2 {
		h.file(fmt.Sprintf("Gate note number %d here", index), "note")
	}

	brief := h.must("", "brief")
	if strings.Contains(brief, "Gate note number") || !strings.Contains(brief, "notes:\n+2 more notes: agentic-memory brief --kind note") {
		t.Errorf("a note limit of 0 should print the notes heading and name every note as more, got:\n%s", brief)
	}

	for _, heading := range []string{"rules (", "traps:", "todos:"} {
		if strings.Contains(brief, heading) {
			t.Errorf("a section the seat holds nothing of should print no heading, got:\n%s", brief)
		}
	}

	view := h.must("", "view", "--layout", "active:mine:kind(note):rank(heat):n(0):brief:as-list")
	if !strings.Contains(view, "  no entry\n  2 more: skip(0)") {
		t.Errorf("n(0) should list nothing and name the rest, got:\n%s", view)
	}
}

func TestViewRunsNoBrief(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	got := h.run("", "view", "--layout", "brief")
	if got.code != 1 || !strings.Contains(got.err,
		"brief is the brief's own layout: agentic-memory brief prints it, agentic-memory view --explain brief shows it") {
		t.Errorf("view --layout brief should point at the brief, got %d %q", got.code, got.err)
	}
}
