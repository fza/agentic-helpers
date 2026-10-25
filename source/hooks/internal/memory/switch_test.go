package memory_test

import (
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestMemoryOffOnTheMachine(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	test.WriteFile(t, h.settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"memory_enabled":false}}}}`)

	for _, verb := range []string{"session-start", "prompt", "tool", "stop", "session-end"} {
		got := h.run(hookPayload("startup", "", "gate"), verb)
		if got.code != 0 || got.out != "" || got.err != "" {
			t.Errorf("the %s hook should say nothing while memory is off, got %+v", verb, got)
		}
	}

	for _, args := range [][]string{{"claim", "main"}, {"search", "--term", "x"}, {"who"}} {
		got := h.run("", args...)
		if got.code != 1 || strings.TrimSpace(got.err) != "agentic-memory: off on this machine (plugin option memory_enabled)" {
			t.Errorf("%v should refuse while memory is off, naming the option, got %+v", args, got)
		}
	}

	answer := h.mcp(`{"name":"search","arguments":{"term":["x"]}}`)
	if !strings.Contains(answer, `"instructions":"agentic-memory is off on this machine; plugin option memory_enabled turns it on"`) ||
		!strings.Contains(answer, "unknown tool: search") {
		t.Errorf("the MCP server should list no tools and say which option turns memory on, got %s", answer)
	}
}
