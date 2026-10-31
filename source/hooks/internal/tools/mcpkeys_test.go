package tools_test

import (
	"encoding/json"
	"testing"
)

func TestTakeAnswersItsFieldsWithoutAMessageOverMCP(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")
	h.tool("count-words", "Count words, another take")
	h.tool("line-count", "Count the lines of a file")
	h.must("merge", "word-count", "count-words", "--from", h.file("merged/wc.py", "merged\n"), "--why", "one tool")

	var answer map[string]json.RawMessage

	err := json.Unmarshal([]byte(h.mcpRead("take", `{"names":["count-words","line-count"]}`)), &answer)
	if err != nil {
		t.Fatalf("the take answer should be one JSON object: %v", err)
	}

	if _, held := answer["message"]; held {
		t.Errorf("take should answer no message beside its fields: %v", answer)
	}

	var framing string

	_ = json.Unmarshal(answer["framing"], &framing)
	if framing != "Tools other agents registered (their own descriptions)" {
		t.Errorf("take should carry the framing lead above other agents' text, got %q", framing)
	}

	var tools []struct {
		Name       string  `json:"name"`
		Summary    string  `json:"summary"`
		MergedFrom *string `json:"merged_from"`
	}

	err = json.Unmarshal(answer["tools"], &tools)
	if err != nil || len(tools) != 2 {
		t.Fatalf("the take answer should list both tools: %v, %s", err, answer["tools"])
	}

	if tools[0].Name != "word-count" || tools[0].Summary != "Count the words of standard input" {
		t.Errorf("a taken tool should carry its summary: %+v", tools[0])
	}

	if tools[0].MergedFrom == nil || *tools[0].MergedFrom != "count-words" {
		t.Errorf("a tool taken by a merged name should carry that name: %+v", tools[0])
	}

	if tools[1].MergedFrom != nil || tools[1].Summary != "Count the lines of a file" {
		t.Errorf("a tool taken by its own name should carry its summary and no merged name: %+v", tools[1])
	}
}

func TestReadListingsCarryNoCountTheReaderHolds(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")

	cases := []struct {
		name    string
		tool    string
		args    string
		absent  []string
		present []string
	}{
		{name: "find", tool: "find", args: `{"term":["count"],"again":true}`,
			absent: []string{"shown", "page"}, present: []string{"matching", "total", "projects"}},
		{name: "list", tool: "list", args: `{"again":true}`,
			absent: []string{"shown", "page", "matching"}, present: []string{"total", "projects"}},
	}

	for _, sent := range cases {
		t.Run(sent.name, func(t *testing.T) {
			var answer map[string]json.RawMessage

			err := json.Unmarshal([]byte(h.mcpRead(sent.tool, sent.args)), &answer)
			if err != nil {
				t.Fatalf("the listing should be one JSON object: %v", err)
			}

			for _, key := range sent.absent {
				if _, held := answer[key]; held {
					t.Errorf("the %s listing should leave out %s, a count its reader holds: %v", sent.tool, key, answer)
				}
			}

			for _, key := range sent.present {
				if _, held := answer[key]; !held {
					t.Errorf("the %s listing should keep %s, a count its reader cannot rebuild: %v", sent.tool, key, answer)
				}
			}
		})
	}
}
