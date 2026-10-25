package tools_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestToolsOffOnTheMachine(t *testing.T) {
	h := newHarness(t)
	test.WriteFile(t, h.settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"tools_enabled":false}}}}`)

	for _, verb := range []string{"prompt", "notice", "stop", "end"} {
		got := h.run(`{"session_id":"`+test.Session+`","prompt":"x"}`, verb)
		if got.code != 0 || got.out != "" || got.err != "" {
			t.Errorf("the %s hook should say nothing while the tool library is off, got %+v", verb, got)
		}
	}

	got := h.run("", "list")
	if got.code != 1 || strings.TrimSpace(got.err) != "agentic-tools: off on this machine (plugin option tools_enabled)" {
		t.Errorf("a command should refuse while the tool library is off, naming the option, got %+v", got)
	}

	served := h.run(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`+"\n"+
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`+"\n", "mcp")
	if !strings.Contains(served.out, "agentic-tools is off on this machine; plugin option tools_enabled turns it on") ||
		!strings.Contains(served.out, `"tools":[]`) {
		t.Errorf("the MCP server should list no tools and say which option turns it on, got %+v", served)
	}
}

func TestTheSeatNameRestsOnMemory(t *testing.T) {
	cases := []struct {
		name    string
		options string
		stamped bool
	}{
		{"memory on", "", true},
		{"memory off", `"memory_enabled":false`, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			test.ClaimSeatByID(t, filepath.Join(h.home, ".claude", "agentic-memory", filepath.FromSlash(h.repoID)),
				"s-aaaaaa", "reviewer", h.session)
			test.WriteFile(t, h.settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{`+tc.options+`}}}}`)

			h.tool("word-count", "Count the words of standard input")

			if strings.Contains(h.read("word-count/history.jsonl"), `"role":"reviewer"`) != tc.stamped {
				t.Errorf("the seat name should be stamped exactly while memory is on")
			}
		})
	}
}
