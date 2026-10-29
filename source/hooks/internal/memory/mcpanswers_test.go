package memory_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/memory"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

var listedTools struct {
	sync.Mutex
	byName map[string]map[string]json.RawMessage
}

// mcp sends one tools/call and returns the server's answers, after checking
// the call's answer against the shape every MCP answer keeps: its text is its
// structuredContent serialized, a refusal carries the error key alone, and
// any other answer fits the shape its tool's answer keeps.
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

		err := test.AnswerFits(h.answerSchemas(), sent.Name, line)
		if err != nil {
			h.t.Errorf("the %s answer should keep the MCP answer's shape: %v\n%s", sent.Name, err, line)
		}
	}
}

// listedTools is each tool's listing, read once: the listing depends on no
// store.
func (h *harness) listedTools() map[string]map[string]json.RawMessage {
	h.t.Helper()

	listedTools.Lock()
	defer listedTools.Unlock()

	if listedTools.byName != nil {
		return listedTools.byName
	}

	lines := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"

	for _, line := range strings.Split(h.must(lines, "mcp"), "\n") {
		if !strings.Contains(line, `"id":2`) {
			continue
		}

		tools, err := test.ListedTools(line)
		if err != nil || len(tools) == 0 {
			h.t.Fatalf("the server should list its tools: %v", err)
		}

		listedTools.byName = tools
	}

	return listedTools.byName
}

// answerSchemas is the shape each listed tool's answer keeps.
func (h *harness) answerSchemas() map[string]json.RawMessage {
	h.t.Helper()

	schemas := map[string]json.RawMessage{}
	for name := range h.listedTools() {
		schemas[name] = memory.AnswerSchema(name)
	}

	return schemas
}

func TestNoToolListsAnOutputSchema(t *testing.T) {
	h := newHarness(t)

	for name, tool := range h.listedTools() {
		if _, listed := tool["outputSchema"]; listed {
			t.Errorf("the %s tool should list no output schema, keeping the listing small", name)
		}
	}
}

func TestNoToolListsTheJSONKey(t *testing.T) {
	h := newHarness(t)

	for name, tool := range h.listedTools() {
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}

		err := json.Unmarshal(tool["inputSchema"], &schema)
		if err != nil {
			t.Fatalf("the %s input schema should decode: %v", name, err)
		}

		if _, listed := schema.Properties["json"]; listed {
			t.Errorf("the %s tool should not list the json key: every answer is JSON", name)
		}
	}
}

func TestEveryToolAnswersItsShape(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "impl")

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

	calls := []struct{ tool, arguments string }{
		{"show", `{"ids":["` + first + `"]}`},
		{"search", `{"term":["Gate"]}`},
		{"brief", `{}`},
		{"view", `{"layout":"as-list"}`},
		{"view", `{"explain":"brief"}`},
		{"topics", `{}`},
		{"who", `{}`},
		{"confirm", `{"ids":["` + first + `"]}`},
		{"state", `{"body":"where the work stands"}`},
		{"retitle", `{"id":"` + first + `","title":"Gate finding filed renamed"}`},
		{"tag", `{"id":"` + first + `","topic":["hooks"]}`},
		{"untag", `{"id":"` + first + `","topics":["hooks"]}`},
		{"link", `{"id":"` + first + `","target":"` + second + `"}`},
		{"retire", `{"ids":["` + second + `"],"because":"done"}`},
		{"supersede", `{"ids":["` + second + `"],"because":"wrong"}`},
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
	call("ack", `{"ids":["`+rule+`"],"done":true}`)
	call("release", `{}`)

	for tool := range h.listedTools() {
		if !called[tool] {
			t.Errorf("the %s tool should be among the calls checked", tool)
		}
	}
}

func TestACommandOffTheListingNamesItsCommandLine(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	for _, name := range []string{"lifetime", "drop", "follow", "unfollow", "bind", "holder", "rename", "rekind",
		"retain", "merge", "unlink", "revive", "attach", "read", "private"} {
		t.Run(name, func(t *testing.T) {
			if _, listed := h.listedTools()[name]; listed {
				t.Errorf("the %s command should not be listed as a tool", name)
			}

			answer := h.mcp(`{"name":"` + name + `","arguments":{}}`)
			if !strings.Contains(answer, `"isError":true`) ||
				!strings.Contains(answer, "agentic-memory: "+name+" is no MCP tool; run it through Bash: agentic-memory "+name+" ") {
				t.Errorf("a call naming the %s command should be refused naming its command line, got %s", name, answer)
			}
		})
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

func TestTheViewShapeTypesGroupedSections(t *testing.T) {
	if schema := string(memory.AnswerSchema("view")); !strings.Contains(schema, `"groups"`) || !strings.Contains(schema, `"expanded"`) {
		t.Errorf("view's answer shape should type grouped sections and expanded entries: %s", schema)
	}
}

func TestTheViewLayoutKeyNamesEveryRenderWord(t *testing.T) {
	h := newHarness(t)

	refused := h.run("", "view", "--layout", "active")
	_, words, found := strings.Cut(refused.out+refused.err, "a section ends with a render word: ")
	words, _, _ = strings.Cut(words, `: "`)

	if refused.code == 0 || !found || words == "" {
		t.Fatalf("a section without a render word should be refused naming the render words: %+v", refused)
	}

	listed := h.must(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`+"\n"+
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`+"\n", "mcp")

	if !strings.Contains(listed, "ending in "+words+";") {
		t.Errorf("view's layout key should name the render words its refusal names (%s):\n%s", words, listed)
	}
}
