package memory_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

var listedSchemas struct {
	sync.Mutex
	byTool map[string]json.RawMessage
}

// mcp sends one tools/call and returns the server's answers, after checking
// the call's answer against the shape every MCP answer keeps: its text is its
// structuredContent serialized, a refusal carries the error key alone, and
// any other answer fits the tool's outputSchema.
func (h *harness) mcp(call string) string {
	h.t.Helper()

	lines := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" +
		fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":%s}`, call) + "\n"

	var answers []string

	scanner := bufio.NewScanner(strings.NewReader(h.must(lines, "mcp")))
	for scanner.Scan() {
		answers = append(answers, scanner.Text())
	}

	h.checkAnswer(call, answers)

	return strings.Join(answers, "\n")
}

func (h *harness) checkAnswer(call string, answers []string) {
	h.t.Helper()

	var sent struct {
		Name string `json:"name"`
	}

	_ = json.Unmarshal([]byte(call), &sent)

	for _, line := range answers {
		var answer struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
		}

		if json.Unmarshal([]byte(line), &answer) != nil || answer.ID != 2 || answer.Result == nil {
			continue
		}

		err := test.AnswerFits(h.listedTools(), sent.Name, line)
		if err != nil {
			h.t.Errorf("the %s answer should keep the MCP answer's shape: %v\n%s", sent.Name, err, line)
		}
	}
}

// listedTools is each tool's outputSchema, read once: the listing depends on
// no store.
func (h *harness) listedTools() map[string]json.RawMessage {
	h.t.Helper()

	listedSchemas.Lock()
	defer listedSchemas.Unlock()

	if listedSchemas.byTool != nil {
		return listedSchemas.byTool
	}

	lines := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"

	for _, line := range strings.Split(h.must(lines, "mcp"), "\n") {
		if !strings.Contains(line, `"id":2`) {
			continue
		}

		schemas, err := test.OutputSchemas(line)
		if err != nil || len(schemas) == 0 {
			h.t.Fatalf("the server should list its tools: %v", err)
		}

		listedSchemas.byTool = schemas
	}

	return listedSchemas.byTool
}

func TestEveryToolAnswersItsOutputSchema(t *testing.T) {
	h, other := rowShelf(t)
	other.must("", "lifetime", "temporary")
	other.must("", "release")

	called := map[string]bool{}
	call := func(tool string, arguments string) map[string]any {
		t.Helper()

		called[tool] = true
		answers := h.mcp(`{"name":"` + tool + `","arguments":` + arguments + `}`)

		if strings.Contains(answers, `"isError":true`) {
			t.Fatalf("the %s call should succeed, got %s", tool, answers)
		}

		return decoded(t, mcpText(t, answers))
	}

	first, _ := call("new", `{"title":"Gate finding filed first","topic":["gate"],"body":"one"}`)["id"].(string)
	second, _ := call("new", `{"title":"Gate finding filed second","topic":["gate"],"body":"two"}`)["id"].(string)
	notes := filepath.Join(h.repo, "notes.txt")
	test.WriteFile(t, notes, "attached bytes")

	calls := []struct{ tool, arguments string }{
		{"show", `{"ids":["` + first + `"]}`},
		{"search", `{"term":["Gate"]}`},
		{"brief", `{}`},
		{"view", `{"layout":"as-list"}`},
		{"view", `{"explain":"brief"}`},
		{"topics", `{}`},
		{"who", `{}`},
		{"holder", `{"name":"impl"}`},
		{"confirm", `{"ids":["` + first + `"]}`},
		{"retain", `{"ids":["` + first + `"],"level":"long"}`},
		{"attach", `{"id":"` + first + `","paths":["` + notes + `"]}`},
		{"read", `{"id":"` + first + `"}`},
		{"rekind", `{"id":"` + first + `","kind":"trap"}`},
		{"state", `{"body":"where the work stands"}`},
		{"retitle", `{"id":"` + first + `","title":"Gate finding filed renamed"}`},
		{"tag", `{"id":"` + first + `","topic":["hooks"]}`},
		{"untag", `{"id":"` + first + `","topics":["hooks"]}`},
		{"link", `{"id":"` + first + `","target":"` + second + `"}`},
		{"unlink", `{"id":"` + first + `","target":"` + second + `"}`},
		{"retire", `{"ids":["` + second + `"],"because":"done"}`},
		{"revive", `{"id":"` + second + `"}`},
		{"supersede", `{"ids":["` + second + `"],"because":"wrong"}`},
		{"follow", `{"topic":["infra"]}`},
		{"unfollow", `{"topic":["infra"]}`},
		{"merge", `{"from_topic":"gate","into":"gates"}`},
		{"trust", `{"path":"` + impostor(h).repo + `"}`},
	}

	answers := map[string]map[string]any{}
	for _, sent := range calls {
		answers[sent.tool+sent.arguments] = call(sent.tool, sent.arguments)
	}

	if explained, _ := answers[`view{"explain":"brief"}`]["message"].(string); !strings.Contains(explained, "kind(rule)") {
		t.Errorf("view's explain should answer its text as the message: %v", answers[`view{"explain":"brief"}`])
	}

	h.must("", "release")
	h.must("", "claim", "main")
	rule := h.file("Read the board on claim", "body", "--kind", "rule", "--from", "user", "--on", "claim")
	h.must("", "release")
	call("claim", `{"name":"main"}`)
	call("drop", `{"shelf":"row-7ah"}`)
	call("ack", `{"ids":["`+rule+`"],"done":true}`)
	call("rename", `{"name":"chief"}`)
	call("lifetime", `{"value":"durable"}`)
	call("release", `{}`)

	for tool := range h.listedTools() {
		if !called[tool] {
			t.Errorf("the %s tool should be among the calls checked", tool)
		}
	}
}

func TestAMessageIsTheCommandLineText(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	printed := strings.TrimSpace(h.must("where the work stands", "state"))
	answered := decoded(t, mcpText(t, h.mcp(`{"name":"state","arguments":{"body":"where the work stands"}}`)))

	if answered["message"] != printed {
		t.Errorf("the MCP message should be what the command prints: %q against %q", answered["message"], printed)
	}

	plain := mcpText(t, h.mcp(`{"name":"topics","arguments":{}}`))
	if keyed := mcpText(t, h.mcp(`{"name":"topics","arguments":{"json":true}}`)); keyed != plain {
		t.Errorf("the json key should change nothing: %s against %s", keyed, plain)
	}
}

func TestTheViewSchemaTypesGroupedSections(t *testing.T) {
	h := newHarness(t)

	if schema := string(h.listedTools()["view"]); !strings.Contains(schema, `"groups"`) || !strings.Contains(schema, `"expanded"`) {
		t.Errorf("view's output schema should type grouped sections and expanded entries: %s", schema)
	}
}
