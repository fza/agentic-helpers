package memory_test

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

// toolDescriptions is every MCP tool's description, by name.
func toolDescriptions(t *testing.T) map[string]string {
	t.Helper()

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

	for _, line := range strings.Split(h.must(lines, "mcp"), "\n") {
		if strings.Contains(line, `"id":2`) {
			_ = json.Unmarshal([]byte(line), &listing)
		}
	}

	descriptions := map[string]string{}
	for _, tool := range listing.Result.Tools {
		descriptions[tool.Name] = tool.Description
	}

	return descriptions
}

func TestEveryReadThatFoldsStatesTheFoldRule(t *testing.T) {
	descriptions := toolDescriptions(t)

	for _, tool := range []string{"search", "show", "brief"} {
		t.Run(tool, func(t *testing.T) {
			if !strings.HasSuffix(descriptions[tool], "; a fold (a more, a count of what is not listed) hides unread data: "+
				"before acting on, answering about or concluding the absence of what it folds, fetch it") {
				t.Errorf("the description of a read that folds should close with the fold rule: %q", descriptions[tool])
			}
		})
	}
}

func TestEveryRuleBetweenKeysIsStatedInItsToolDescription(t *testing.T) {
	descriptions := toolDescriptions(t)

	for _, rule := range []struct{ tool, stated string }{
		{"supersede", "takes by, because or both, at least one"},
		{"ack", "takes one of done, decline, undo, remove, add or order"},
		{"search", "needs term, query or status"},
		{"view", "takes layout or explain"},
	} {
		t.Run(rule.tool, func(t *testing.T) {
			if !strings.Contains(descriptions[rule.tool], rule.stated) {
				t.Errorf("the tool description should state the rule between its keys: %q", descriptions[rule.tool])
			}
		})
	}
}
