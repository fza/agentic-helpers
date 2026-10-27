package tools_test

import (
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestEveryToolDescribesItsCallInKeys(t *testing.T) {
	h := newHarness(t)

	lines := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"

	for _, line := range strings.Split(h.must2(lines, "mcp"), "\n") {
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
