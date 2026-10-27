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
