package tools_test

import (
	"encoding/json"
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

func TestEveryReadThatFoldsStatesTheFoldRule(t *testing.T) {
	h := newHarness(t)

	lines := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"

	var listing struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		} `json:"result"`
	}

	for _, line := range strings.Split(h.must2(lines, "mcp"), "\n") {
		if strings.Contains(line, `"id":2`) {
			_ = json.Unmarshal([]byte(line), &listing)
		}
	}

	descriptions := map[string]string{}
	for _, tool := range listing.Result.Tools {
		descriptions[tool.Name] = tool.Description
	}

	for _, tool := range []string{"find", "list"} {
		t.Run(tool, func(t *testing.T) {
			if !strings.HasSuffix(descriptions[tool], "; a fold (a more, a count of what is not listed) hides unread data: "+
				"before acting on, answering about or concluding the absence of what it folds, fetch it") {
				t.Errorf("the description of a read that folds should close with the fold rule: %q", descriptions[tool])
			}
		})
	}
}
