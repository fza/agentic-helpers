package memory_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestAQuotedValueIsRefusedNotRepaired(t *testing.T) {
	refusal := `--from takes user, agent or tool, not "\"user\""`

	cases := []struct {
		name string
		call func(h *harness) (string, bool)
	}{
		{name: "the command line", call: func(h *harness) (string, bool) {
			got := h.run("body", "new", "--title", "Gate finding filed quoted", "--topic", "gate", "--from", `"user"`)

			return got.err, got.code == 1
		}},
		{name: "the MCP tool", call: func(h *harness) (string, bool) {
			answer := h.mcp(`{"name":"new","arguments":{"title":"Gate finding filed quoted","body":"body",` +
				`"topic":["gate"],"from":"\"user\""}}`)

			var result struct {
				Result struct {
					Content []struct {
						Text string `json:"text"`
					} `json:"content"`
					IsError bool `json:"isError"`
				} `json:"result"`
			}

			lines := strings.Split(answer, "\n")

			err := json.Unmarshal([]byte(lines[len(lines)-1]), &result)
			if err != nil || len(result.Result.Content) == 0 {
				return answer, false
			}

			return test.Readable(result.Result.Content[0].Text), result.Result.IsError
		}},
	}

	for _, sent := range cases {
		t.Run(sent.name, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")

			text, failed := sent.call(h)
			if !failed || !strings.Contains(text, refusal) {
				t.Errorf("a value wrapped in quotes should be refused naming the accepted values, got %q", text)
			}

			if out := h.must("", "search", "--status", "live"); strings.Contains(out, "filed quoted") {
				t.Errorf("a refused value should file nothing, got:\n%s", out)
			}
		})
	}
}
