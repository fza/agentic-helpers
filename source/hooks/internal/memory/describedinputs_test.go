package memory_test

import (
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestEveryToolDescribesItsCallInKeys(t *testing.T) {
	h := newHarness(t)

	lines := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"

	for _, line := range strings.Split(h.must(lines, "mcp"), "\n") {
		if !strings.Contains(line, `"id":2`) {
			continue
		}

		gaps, err := test.UndescribedInputs(line)
		if err != nil {
			t.Fatalf("the server should list its tools: %v", err)
		}

		for _, gap := range gaps {
			t.Errorf("a tool should describe its call in its keys: %s", gap)
		}

		return
	}

	t.Fatal("the server should answer the tool listing")
}

func TestSupersedeTakesASuccessorAndAReasonTogether(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	old := h.file("old finding", "infra")
	successor := h.file("new finding", "infra")

	answer := h.mcp(`{"name":"supersede","arguments":{"ids":["` + old + `"],"by":"` + successor + `","because":"the cache moved"}}`)
	if strings.Contains(answer, `"isError":true`) {
		t.Fatalf("a supersede naming its successor and its reason should land: %s", answer)
	}

	shown := h.must("", "show", old, "--with-superseded")
	if !strings.Contains(shown, "by "+successor+": the cache moved") {
		t.Errorf("a superseded entry should carry its successor and its reason: %s", shown)
	}
}
