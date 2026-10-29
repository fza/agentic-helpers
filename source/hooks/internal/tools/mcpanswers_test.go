package tools_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestEveryToolAnswersItsOutputSchema(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")
	h.tool("line-count", "Number the lines of a log file")

	added := h.file("scripts/csv-sum.py", "# csv-sum\nprint('sum')\n")
	updated := h.file("scripts/word-count.py", "# word-count\nprint('words, faster')\n")
	merged := h.file("merged/count.py", "# count\nprint('both')\n")
	clone := h.elsewhere(ownRepo)
	h.elsewhere(otherRepo).tool("tally-rows", "Tally the rows of a table")

	calls := []struct{ tool, arguments string }{
		{"list", `{}`},
		{"list", `{"json":true}`},
		{"find", `{"term":["count"]}`},
		{"take", `{"names":["word-count"]}`},
		{"add", `{"name":"csv-sum","from":"` + added + `","summary":"Sum a column of a CSV file","usage":"python3 csv-sum.py"}`},
		{"update", `{"name":"word-count","from":"` + updated + `","why":"faster"}`},
		{"update", `{"name":"tally-rows","summary":"Tally the rows of a table, headers aside","why":"headers"}`},
		{"merge", `{"survivor":"word-count","members":["line-count"],"from":"` + merged + `","why":"one counter"}`},
		{"trust", `{"path":"` + clone.repo + `"}`},
		{"reindex", `{}`},
		{"list", `{"Json":true}`},
	}

	lines := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	}

	for position, sent := range calls {
		lines = append(lines, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s}}`,
			position+3, sent.tool, sent.arguments))
	}

	answers := map[int]string{}

	for _, line := range strings.Split(strings.TrimSpace(h.must2(strings.Join(lines, "\n")+"\n", "mcp")), "\n") {
		var answer struct {
			ID int `json:"id"`
		}

		_ = json.Unmarshal([]byte(line), &answer)
		answers[answer.ID] = line
	}

	schemas, err := test.OutputSchemas(answers[2])
	if err != nil {
		t.Fatal(err)
	}

	for position, sent := range calls {
		answer := answers[position+3]

		refusal := strings.Contains(sent.arguments, "Json")
		if strings.Contains(answer, `"isError":true`) != refusal {
			t.Errorf("the %s call should succeed only with keys it lists, got %s", sent.tool, answer)
		}

		err = test.AnswerFits(schemas, sent.tool, answer)
		if err != nil {
			t.Errorf("the %s answer should keep the MCP answer's shape: %v\n%s", sent.tool, err, answer)
		}
	}

	if forked := structured(answers[9]); !strings.Contains(forked, `"forked_from":"`+otherRepo+`"`) {
		t.Errorf("an update forking a foreign tool should name where it forked from: %s", forked)
	}

	if structured(answers[3]) != structured(answers[4]) {
		t.Errorf("the json key should change nothing: %s against %s", answers[4], answers[3])
	}

	for tool := range schemas {
		if !strings.Contains(fmt.Sprint(calls), "{"+tool+" ") {
			t.Errorf("the %s tool should be among the calls checked", tool)
		}
	}
}

func structured(line string) string {
	var answer struct {
		Result struct {
			Structured json.RawMessage `json:"structuredContent"`
		} `json:"result"`
	}

	_ = json.Unmarshal([]byte(line), &answer)

	return string(answer.Result.Structured)
}

func TestNoToolListsTheJSONKey(t *testing.T) {
	h := newHarness(t)

	lines := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"

	for _, line := range strings.Split(h.must2(lines, "mcp"), "\n") {
		if !strings.Contains(line, `"id":2`) {
			continue
		}

		var listing struct {
			Result struct {
				Tools []struct {
					Name        string `json:"name"`
					InputSchema struct {
						Properties map[string]json.RawMessage `json:"properties"`
					} `json:"inputSchema"`
				} `json:"tools"`
			} `json:"result"`
		}

		err := json.Unmarshal([]byte(line), &listing)
		if err != nil {
			t.Fatalf("the tool listing should decode: %v", err)
		}

		if len(listing.Result.Tools) == 0 {
			t.Fatal("the server should list its tools")
		}

		for _, tool := range listing.Result.Tools {
			if _, listed := tool.InputSchema.Properties["json"]; listed {
				t.Errorf("the %s tool should not list the json key: every answer is JSON", tool.Name)
			}
		}

		return
	}

	t.Fatal("the server should answer the tool listing")
}

func TestTheJSONKeyPassesOnlyWhereTheCommandTakesJSON(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")

	cases := []struct {
		name, tool, without, with, refusal string
	}{
		{name: "find, whose command takes --json", tool: "find", without: `{"term":["words"]}`, with: `{"term":["words"],"json":true}`},
		{name: "list, whose command takes --json", tool: "list", without: `{}`, with: `{"json":false}`},
		{name: "take, whose command takes no --json", tool: "take", with: `{"names":["word-count"],"json":true}`,
			refusal: `take takes no key \"json\"; it takes names`},
		{name: "reindex, whose command takes no --json", tool: "reindex", with: `{"json":true}`,
			refusal: `reindex takes no key \"json\"; it takes none`},
	}

	for _, sent := range cases {
		t.Run(sent.name, func(t *testing.T) {
			lines := []string{
				`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
				fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, sent.tool, sent.with),
			}

			if sent.without != "" {
				lines = append(lines,
					fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, sent.tool, sent.without))
			}

			answers := map[int]string{}

			for _, line := range strings.Split(strings.TrimSpace(h.must2(strings.Join(lines, "\n")+"\n", "mcp")), "\n") {
				var answer struct {
					ID int `json:"id"`
				}

				_ = json.Unmarshal([]byte(line), &answer)
				answers[answer.ID] = line
			}

			if sent.refusal != "" {
				if !strings.Contains(answers[2], `"isError":true`) || !strings.Contains(answers[2], sent.refusal) {
					t.Errorf("a tool whose command takes no --json should refuse the json key by name, got %s", answers[2])
				}

				return
			}

			if strings.Contains(answers[2], `"isError":true`) {
				t.Errorf("a tool whose command takes --json should let the json key through, got %s", answers[2])
			}

			if structured(answers[2]) != structured(answers[3]) {
				t.Errorf("the json key should change nothing: %s against %s", answers[2], answers[3])
			}
		})
	}
}
