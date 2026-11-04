package memory

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestEveryHintTakesItsChannelsForm(t *testing.T) {
	held := &entry{id: "m-abc123", title: "Port the clock", topics: []string{"gate", "the clock"}}

	cases := []struct {
		name string
		call hintCall
		cli  string
		mcp  string
	}{
		{"add a state line", addStateHint(),
			`agentic-memory ack state --add '<line>' --until <end>`,
			`memory ack {"ids":["state"],"add":["<line>"],"until":["<end>"]}`},
		{"tick a state line", settleHint("state#<item>", "done"),
			`agentic-memory ack state#<item> --done`,
			`memory ack {"ids":["state#<item>"],"done":true}`},
		{"undo an ended line", settleHint("state#k3x", "undo"),
			`agentic-memory ack state#k3x --undo`,
			`memory ack {"ids":["state#k3x"],"undo":true}`},
		{"file an entry", newEntryHint(),
			`agentic-memory new --kind <kind> --title '<3-10 words>' --topic <name>... < body`,
			`memory new {"kind":"<kind>","title":"<3-10 words>","topic":["<name>"],"body":"<body>"}`},
		{"file the steps as a todo", stepsTodoHint(held, []string{"read the timer", "run CI"}),
			`agentic-memory new --kind todo --absorbs m-abc123 --title 'Port the clock' --topic gate --topic 'the clock' --item 'read the timer' --item 'run CI'`,
			`memory new {"kind":"todo","absorbs":["m-abc123"],"title":"Port the clock","topic":["gate","the clock"],"item":["read the timer","run CI"]}`},
		{"show the ledger's history", showHint(ledgerID, "with_history"),
			`agentic-memory show state --with-history`,
			`memory show {"ids":["state"],"with_history":true}`},
		{"show every item", showHint("m-abc123", "all_items"),
			`agentic-memory show m-abc123 --all-items`,
			`memory show {"ids":["m-abc123"],"all_items":true}`},
		{"brief the due entries", briefHint(hintSwitch("due")),
			`agentic-memory brief --due`,
			`memory brief {"due":true}`},
		{"brief the notes", briefHint(hintFlag("kind", kindNote)),
			`agentic-memory brief --kind note`,
			`memory brief {"kind":"note"}`},
		{"brief a topic", briefHint(hintFlag("topic", []string{"<name>"})),
			`agentic-memory brief --topic <name>`,
			`memory brief {"topic":["<name>"]}`},
		{"the brief", briefHint(),
			`agentic-memory brief`,
			`memory brief {}`},
		{"search other seats", searchHint(hintSwitch("all_seats"), hintFlag("status", []string{statusLive})),
			`agentic-memory search --all-seats --status live`,
			`memory search {"all_seats":true,"status":["live"]}`},
		{"search a topic past the budget", searchHint(hintFlag("topic", []string{"the clock"}).spelledBy(shellWord),
			hintFlag("term", []string{"."})),
			`agentic-memory search --topic 'the clock' --term .`,
			`memory search {"topic":["the clock"],"term":["."]}`},
		{"search by meaning", searchHint(hintText("query", "it's late")),
			`agentic-memory search --query 'it'\''s late'`,
			`memory search {"query":"it's late"}`},
		{"retitle", retitleHint("m-abc123"),
			`agentic-memory retitle m-abc123 '<title>'`,
			`memory retitle {"id":"m-abc123","title":"<title>"}`},
		{"tag", tagHint("m-abc123"),
			`agentic-memory tag m-abc123 <topic>...`,
			`memory tag {"id":"m-abc123","topic":["<topic>"]}`},
		{"retire", retireHint("m-abc123", "landed in 15af1a8"),
			`agentic-memory retire m-abc123 --because 'landed in 15af1a8'`,
			`memory retire {"ids":["m-abc123"],"because":"landed in 15af1a8"}`},
		{"supersede", supersedeHint("m-abc123", "credential"),
			`agentic-memory supersede m-abc123 --because 'credential'`,
			`memory supersede {"ids":["m-abc123"],"because":"credential"}`},
		{"link a capture", linkCaptureHint(),
			`agentic-memory link <todo> <graph id>`,
			`memory link {"id":"<todo>","target":"<graph id>"}`},
		{"claim", claimHint(),
			`agentic-memory claim <seat>`,
			`memory claim {"name":"<seat>"}`},
		{"release", releaseHint(),
			`agentic-memory release`,
			`memory release {}`},
		{"who", whoHint(),
			`agentic-memory who`,
			`memory who {}`},
		{"who lists the unused seats", whoHint("all", "unused"),
			`agentic-memory who --all --unused`,
			`memory who {"all":true,"unused":true}`},
		{"explain the brief", explainBriefHint(),
			`agentic-memory view --explain brief`,
			`memory view {"explain":"brief"}`},
		{"merge topics, which the server does not list", mergeTopicsHint("clock", "clocks"),
			`agentic-memory merge clock clocks`,
			`agentic-memory merge clock clocks`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.call.in(channelCLI); got != tc.cli {
				t.Errorf("the hint should take the command line's form on the command line:\n got %s\nwant %s", got, tc.cli)
			}

			got := tc.call.in(channelMCP)
			if got != tc.mcp {
				t.Errorf("the hint should take the MCP form over MCP:\n got %s\nwant %s", got, tc.mcp)
			}

			checkMCPHints(t, got)
		})
	}
}

func TestHintFragmentsTakeTheirChannelsForm(t *testing.T) {
	cases := []struct {
		name string
		text func(channel) string
		cli  string
		mcp  string
	}{
		{"a choice of calls", func(at channel) string {
			return hintChoice(at, settleHint("state#<item>", "done"), settleHint("state#<item>", "remove"))
		}, `agentic-memory ack state#<item> --done | ack state#<item> --remove`,
			`memory ack {"ids":["state#<item>"],"done":true} | ack {"ids":["state#<item>"],"remove":true}`},
		{"a page's arguments", func(at channel) string { return hintArgs(at, hintFlag("skip", 10)) },
			`--skip 10`, `"skip":10`},
		{"a key", func(at channel) string { return hintKey(at, "all_items") }, `--all-items`, `"all_items"`},
		{"a verb", func(at channel) string { return hintVerb(at, "new") }, `agentic-memory new`, `memory new`},
		{"a verb the server does not list", func(at channel) string { return hintVerb(at, "merge") },
			`agentic-memory merge`, `agentic-memory merge`},
		{"the ledger nudge", ledgerAsk,
			`Add or tick state lines where work moved: agentic-memory ack state --add '<line>' --until <end> | ack state#<item> --done`,
			`Add or tick state lines where work moved: memory ack {"ids":["state"],"add":["<line>"],"until":["<end>"]} | ack {"ids":["state#<item>"],"done":true}`},
		{"the no-seat refusal", noSeatText,
			`claim a seat first: agentic-memory claim <seat>`,
			`claim a seat first: memory claim {"name":"<seat>"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.text(channelCLI); got != tc.cli {
				t.Errorf("the text should name the command line's form on the command line:\n got %s\nwant %s", got, tc.cli)
			}

			if got := tc.text(channelMCP); got != tc.mcp {
				t.Errorf("the text should name the MCP form over MCP:\n got %s\nwant %s", got, tc.mcp)
			}
		})
	}
}

// checkMCPHints holds every `memory <tool> {...}` call the text names to the
// tool's schema: the server refuses a key the schema does not list.
func checkMCPHints(t *testing.T, text string) {
	t.Helper()

	for _, call := range mcpHintCalls(text) {
		held, found := commandByName(call.verb)
		if !found || held.cliOnly {
			t.Errorf("an MCP hint should name a tool the server lists: %s", call.verb)

			continue
		}

		err := exactKeys(held, json.RawMessage(call.arguments))
		if err != nil {
			t.Errorf("an MCP hint should send only keys its tool takes: %v", err)
		}
	}
}

type mcpHintCall struct {
	verb      string
	arguments string
}

// mcpHintCalls is every `memory <tool> {...}` call in text, each object read
// to its closing brace.
func mcpHintCalls(text string) []mcpHintCall {
	var calls []mcpHintCall

	for rest := text; ; {
		_, after, found := strings.Cut(rest, mcpServer+" ")
		if !found {
			return calls
		}

		rest = after

		verb, object, hasObject := strings.Cut(after, " {")
		if !hasObject || strings.ContainsAny(verb, " \n") {
			continue
		}

		decoder := json.NewDecoder(strings.NewReader("{" + object))

		var arguments json.RawMessage

		err := decoder.Decode(&arguments)
		if err != nil {
			continue
		}

		calls = append(calls, mcpHintCall{verb: verb, arguments: string(arguments)})
	}
}

func TestMCPHintCallsReadsEveryCall(t *testing.T) {
	text := `x: memory ack {"ids":["state"],"add":["<line>"]} | ack {"ids":["a"]}; memory brief {} and memory new`

	var verbs []string
	for _, call := range mcpHintCalls(text) {
		verbs = append(verbs, call.verb)
	}

	if !slices.Equal(verbs, []string{"ack", "brief"}) {
		t.Errorf("every whole MCP call should be read, and nothing else: %v", verbs)
	}
}
