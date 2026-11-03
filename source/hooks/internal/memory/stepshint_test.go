package memory_test

import (
	"encoding/json"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

const stepsHintLine = "agentic-memory: this body lists steps; a todo ticks each one: "

const planBody = "The waiter moves to the new clock.\n- read the timer\n- swap the clock's source\n1. run CI\n"

type shownEntry struct {
	Kind   string          `json:"kind"`
	Body   json.RawMessage `json:"body"`
	Status string          `json:"status"`
	Items  []struct {
		Text string `json:"text"`
	} `json:"items"`
}

func (h *harness) shown(id string) shownEntry {
	h.t.Helper()

	var answer struct {
		Entries []shownEntry `json:"entries"`
	}

	err := json.Unmarshal([]byte(h.must("", "show", id, "--json", "--again", "--no-heat", "--all-items")), &answer)
	if err != nil || len(answer.Entries) != 1 {
		h.t.Fatalf("show should answer the one entry: %v", err)
	}

	return answer.Entries[0]
}

// readyArgs is the arguments of the ready command a reply offers on its
// `  → agentic-memory ` line, as the shell reads them.
func readyArgs(t *testing.T, reply string) []string {
	t.Helper()

	for line := range strings.Lines(reply) {
		command, found := strings.CutPrefix(strings.TrimRight(line, "\n"), "  → agentic-memory ")
		if !found {
			continue
		}

		file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
		if err != nil || len(file.Stmts) != 1 {
			t.Fatalf("the ready command should parse as one shell command: %v\n%s", err, command)
		}

		call, _ := file.Stmts[0].Cmd.(*syntax.CallExpr)
		if call == nil {
			t.Fatalf("the ready command should be a plain call:\n%s", command)
		}

		args, err := expand.Fields(nil, call.Args...)
		if err != nil || len(args) != len(call.Args) {
			t.Fatalf("every word of the ready command should expand to one argument: %v\n%s", err, command)
		}

		return args
	}

	return nil
}

func TestANonTodoListingStepsGetsTheTodoHint(t *testing.T) {
	cases := []struct {
		name  string
		extra []string
	}{
		{"note", nil},
		{"rule", []string{"--kind", "rule", "--from", "user"}},
		{"trap", []string{"--kind", "trap"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := heldHarness(t)

			reply := h.must(planBody, append([]string{"new", "--title", "Port the waiter's clock", "--topic", "gate"}, tc.extra...)...)
			if !strings.Contains(reply, stepsHintLine+"--kind todo --item …") {
				t.Errorf("a body listing steps should get the todo hint:\n%s", reply)
			}

			if readyArgs(t, reply) == nil {
				t.Errorf("plain steps should come with the ready command:\n%s", reply)
			}
		})
	}
}

func TestTheReadyCommandFilesTheTodoInPlaceOfTheNote(t *testing.T) {
	h := heldHarness(t)

	note := h.file("Port the waiter's clock", planBody)

	reply := h.must(planBody, "new", "--title", "Port the waiter's clock now", "--topic", "gate", "--topic", "the clock")

	args := readyArgs(t, reply)
	if args == nil {
		t.Fatalf("plain steps should come with the ready command:\n%s", reply)
	}

	filed := idPattern.FindString(reply)

	todoReply := h.must("", args...)
	todo := idPattern.FindString(todoReply)

	shown := h.shown(todo)

	var texts []string
	for _, item := range shown.Items {
		texts = append(texts, item.Text)
	}

	if shown.Kind != "todo" || strings.Join(texts, "|") != "read the timer|swap the clock's source|run CI" {
		t.Errorf("the ready command should file a todo holding each list line as an item: %+v\n%s", shown, todoReply)
	}

	if h.shown(filed).Status != "superseded" || h.shown(note).Status == "superseded" {
		t.Errorf("the ready command should supersede the entry just filed, and that one alone:\n%s", todoReply)
	}
}

func TestOnlyAStepListGetsTheTodoHint(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		extra []string
	}{
		{"two list lines", "- read the timer\n- swap the clock\n", nil},
		{"three list lines apart", "- read the timer\n\n- swap the clock\n\n- run CI\n", nil},
		{"no list", "The waiter reads the timer.\n", nil},
		{"a todo", planBody, []string{"--kind", "todo"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := heldHarness(t)

			reply := h.must(tc.body, append([]string{"new", "--title", "Port the waiter's clock", "--topic", "gate"}, tc.extra...)...)
			if strings.Contains(reply, "this body lists steps") {
				t.Errorf("a body holding no step list, or a todo, should get no todo hint:\n%s", reply)
			}
		})
	}
}

func TestStepsThatAreNotPlainGetTheHintAlone(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"nested", "- read the timer\n  - the old one\n- swap the clock\n- run CI\n"},
		{"ticked", "- [x] read the timer\n- swap the clock\n- run CI\n"},
		{"continued", "- read the timer\n  and its drift\n- swap the clock\n- run CI\n"},
		{"empty box", "- [ ]\n- swap the clock\n- run CI\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := heldHarness(t)

			reply := h.must(tc.body, "new", "--title", "Port the waiter's clock", "--topic", "gate")
			if !strings.Contains(reply, stepsHintLine) {
				t.Errorf("a body listing steps should get the todo hint:\n%s", reply)
			}

			if strings.Contains(reply, "  → ") {
				t.Errorf("a list line that is no plain step should leave the ready command out:\n%s", reply)
			}
		})
	}
}

func TestAnUncheckedBoxIsAPlainStep(t *testing.T) {
	h := heldHarness(t)

	reply := h.must("- [ ] read the timer\n- [ ] swap the clock\n- [ ] run CI\n", "new", "--title", "Port the waiter's clock", "--topic", "gate")

	args := readyArgs(t, reply)
	if !strings.Contains(strings.Join(args, "|"), "--item|read the timer|--item|swap the clock|--item|run CI") {
		t.Errorf("an unchecked box should turn into its step's text: %q\n%s", args, reply)
	}
}

func TestTheTodoHintLeavesTheEntryAsFiled(t *testing.T) {
	h := heldHarness(t)

	reply := h.must(planBody, "new", "--title", "Port the waiter's clock", "--topic", "gate")
	shown := h.shown(idPattern.FindString(reply))

	var body string

	_ = json.Unmarshal(shown.Body, &body)

	if shown.Kind != "note" || shown.Status != "live" || body != strings.TrimRight(planBody, "\n") || len(shown.Items) > 0 {
		t.Errorf("the entry should file as sent, the hint aside: %+v", shown)
	}
}

func TestTheTodoHintOverMCPOffersTheNewCall(t *testing.T) {
	h := heldHarness(t)

	body, _ := json.Marshal(planBody)

	answer := mcpMessage(t, h.mcp(`{"name":"new","arguments":{"title":"Port the waiter's clock","topic":["gate"],"body":`+string(body)+`}}`))
	note := idPattern.FindString(answer)
	if !strings.Contains(answer, stepsHintLine+`"kind":"todo","item":["…"]`) {
		t.Fatalf("a body listing steps should get the todo hint over MCP:\n%s", answer)
	}

	_, call, found := strings.Cut(answer, "  → memory new ")
	call, _, _ = strings.Cut(call, "\n")

	if !found || !json.Valid([]byte(call)) {
		t.Fatalf("plain steps should come with the new tool's arguments:\n%s", answer)
	}

	filed := mcpMessage(t, h.mcp(`{"name":"new","arguments":`+call+`}`))

	shown := h.shown(idPattern.FindString(filed))
	if shown.Kind != "todo" || len(shown.Items) != 3 {
		t.Errorf("the offered call should file a todo holding each list line as an item: %+v\n%s", shown, filed)
	}

	if h.shown(note).Status != "superseded" {
		t.Errorf("the offered call should supersede the entry just filed:\n%s", filed)
	}
}
