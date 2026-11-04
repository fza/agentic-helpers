package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// channel is the surface an agent reaches memory through: the `memory` MCP
// server's tools, or the command line through Bash. Every command a line
// points the agent to is spelled in its channel's form, since an agent copies
// the command it is shown, and a hint in the other form moves it to a
// surface it did not choose.
type channel string

const (
	channelMCP channel = "mcp"
	channelCLI channel = "cli"
)

// mcpServer is the plugin's memory server, by whose name an MCP hint calls a
// tool: `memory ack {...}`.
const mcpServer = "memory"

// hintCall is one command a line points the agent to, held once and spelled
// in either channel's form: `agentic-memory ack state --add '<line>'` on the
// command line, `memory ack {"ids":["state"],"add":["<line>"]}` over MCP. A
// command the server does not list is spelled as a command line in both.
type hintCall struct {
	verb string
	args []hintArg
}

type argPlace int

const (
	placeFlag argPlace = iota
	placePositional
	placeStdin
)

// hintArg is one argument of a hint: key is its MCP key and, hyphenated, its
// flag; value a string, a list of them, a number or true. spell writes a
// value on the command line; more marks an argument the command line repeats.
type hintArg struct {
	key   string
	value any
	place argPlace
	spell func(string) string
	more  bool
}

func hint(verb string, args ...hintArg) hintCall {
	return hintCall{verb: verb, args: args}
}

func bare(text string) string { return text }

// hintIDs and hintID name the entries a command acts on, in the key its schema
// takes them under.
func hintIDs(ids ...string) hintArg {
	return hintArg{key: "ids", value: ids, place: placePositional, spell: bare}
}

func hintID(id string) hintArg {
	return hintArg{key: "id", value: id, place: placePositional, spell: bare}
}

func hintPositional(key string, value any) hintArg {
	return hintArg{key: key, value: value, place: placePositional, spell: bare}
}

func hintFlag(key string, value any) hintArg {
	return hintArg{key: key, value: value, spell: bare}
}

func hintText(key string, value any) hintArg {
	return hintArg{key: key, value: value, spell: shellQuoted}
}

func hintSwitch(key string) hintArg {
	return hintArg{key: key, value: true, spell: bare}
}

func hintBody(placeholder string) hintArg {
	return hintArg{key: "body", value: placeholder, place: placeStdin, spell: bare}
}

func (arg hintArg) repeated() hintArg {
	arg.more = true

	return arg
}

func (arg hintArg) spelledBy(spell func(string) string) hintArg {
	arg.spell = spell

	return arg
}

// form is the channel a call is spelled for: a command the MCP server does
// not list takes the command line in every channel.
func (call hintCall) form(in channel) channel {
	held, found := commandByName(call.verb)
	if in == channelCLI || found && held.cliOnly {
		return channelCLI
	}

	return channelMCP
}

// in is the call spelled whole for the channel.
func (call hintCall) in(at channel) string {
	if call.form(at) == channelCLI {
		return self + " " + call.bare(channelCLI)
	}

	return mcpServer + " " + call.bare(channelMCP)
}

// bare is the call without the program or server naming it, as a call
// following another in a choice reads.
func (call hintCall) bare(at channel) string {
	if call.form(at) == channelCLI {
		return strings.Join(append([]string{call.verb}, cliWords(call.args)...), " ")
	}

	return call.verb + " {" + mcpPairs(call.args) + "}"
}

// hintChoice is calls offered as alternatives: the first whole, the others
// bare.
func hintChoice(at channel, calls ...hintCall) string {
	spelled := make([]string, 0, len(calls))
	for index, call := range calls {
		if index == 0 {
			spelled = append(spelled, call.in(at))

			continue
		}

		spelled = append(spelled, call.bare(at))
	}

	return strings.Join(spelled, " | ")
}

// hintArgs is arguments alone, as a line naming what to add to a call reads:
// `--skip 10` on the command line, `"skip":10` over MCP.
func hintArgs(at channel, args ...hintArg) string {
	if at == channelCLI {
		return strings.Join(cliWords(args), " ")
	}

	return mcpPairs(args)
}

// hintKey is the name of one argument: `--item`, or `"item"`.
func hintKey(at channel, key string) string {
	if at == channelCLI {
		return "--" + strings.ReplaceAll(key, "_", "-")
	}

	return `"` + key + `"`
}

// hintVerb names a command in prose: `agentic-memory new`, or `memory new`.
func hintVerb(at channel, verb string) string {
	call := hint(verb)
	if call.form(at) == channelCLI {
		return self + " " + verb
	}

	return mcpServer + " " + verb
}

func cliWords(args []hintArg) []string {
	var words []string

	var stdin []string

	for _, arg := range args {
		var spelled []string

		switch value := arg.value.(type) {
		case bool:
			spelled = []string{hintKey(channelCLI, arg.key)}
		case int:
			spelled = []string{hintKey(channelCLI, arg.key), fmt.Sprint(value)}
		case string:
			spelled = argWords(arg, []string{value})
		case []string:
			spelled = argWords(arg, value)
		}

		if arg.more && len(spelled) > 0 {
			spelled[len(spelled)-1] += "..."
		}

		if arg.place == placeStdin {
			stdin = append(stdin, spelled...)

			continue
		}

		words = append(words, spelled...)
	}

	return append(words, stdin...)
}

func argWords(arg hintArg, values []string) []string {
	var words []string

	for _, value := range values {
		switch arg.place {
		case placePositional:
			words = append(words, arg.spell(value))
		case placeStdin:
			words = append(words, "< "+strings.Trim(value, "<>"))
		default:
			words = append(words, hintKey(channelCLI, arg.key), arg.spell(value))
		}
	}

	return words
}

// mcpPairs is the arguments as the key-value pairs of a JSON object, in the
// order given, `<` and `&` kept as written: a hint's placeholders read as
// typed.
func mcpPairs(args []hintArg) string {
	pairs := make([]string, 0, len(args))

	for _, arg := range args {
		var encoded bytes.Buffer

		encoder := json.NewEncoder(&encoded)
		encoder.SetEscapeHTML(false)

		err := encoder.Encode(arg.value)
		if err != nil {
			continue
		}

		pairs = append(pairs, `"`+arg.key+`":`+strings.TrimSuffix(encoded.String(), "\n"))
	}

	return strings.Join(pairs, ",")
}

// Every command a reply or a hook line names, each held once.

func addStateHint() hintCall {
	return hint("ack", hintIDs(ledgerID), hintText("add", []string{"<line>"}), hintFlag("until", []string{"<end>"}))
}

// settleHint changes one todo item or ledger line, or settles a rule: switch
// is done, undo or remove.
func settleHint(address string, change string) hintCall {
	return hint("ack", hintIDs(address), hintSwitch(change))
}

func newEntryHint() hintCall {
	return hint("new", hintFlag("kind", "<kind>"), hintText("title", "<3-10 words>"),
		hintFlag("topic", []string{"<name>"}).repeated(), hintBody("<body>"))
}

// stepsTodoHint files a todo whose items are the steps a body listed,
// absorbing the entry that listed them.
func stepsTodoHint(held *entry, steps []string) hintCall {
	args := []hintArg{hintFlag("kind", kindTodo), hintFlag("absorbs", []string{held.id}), hintText("title", held.title)}
	if len(held.topics) > 0 {
		args = append(args, hintFlag("topic", held.topics).spelledBy(shellWord))
	}

	return hint("new", append(args, hintText("item", steps))...)
}

func showHint(id string, switches ...string) hintCall {
	args := []hintArg{hintIDs(id)}
	for _, name := range switches {
		args = append(args, hintSwitch(name))
	}

	return hint("show", args...)
}

func briefHint(args ...hintArg) hintCall { return hint("brief", args...) }

func searchHint(args ...hintArg) hintCall { return hint("search", args...) }

func retitleHint(id string) hintCall {
	return hint("retitle", hintID(id), hintPositional("title", "<title>").spelledBy(shellQuoted))
}

func tagHint(id string) hintCall {
	return hint("tag", hintID(id), hintPositional("topic", []string{"<topic>"}).repeated())
}

func mergeTopicsHint(from string, into string) hintCall {
	return hint("merge", hintPositional("from_topic", from), hintPositional("into", into))
}

func retireHint(id string, because string) hintCall {
	return hint("retire", hintIDs(id), hintText("because", because))
}

func supersedeHint(id string, because string) hintCall {
	return hint("supersede", hintIDs(id), hintText("because", because))
}

func linkCaptureHint() hintCall {
	return hint("link", hintID("<todo>"), hintPositional("target", "<graph id>"))
}

func claimHint() hintCall { return hint("claim", hintPositional("name", "<seat>")) }

func releaseHint() hintCall { return hint("release") }

// noSeatText refuses a call that needs a seat the session does not hold.
func noSeatText(at channel) string { return "claim a seat first: " + claimHint().in(at) }

func whoHint(switches ...string) hintCall {
	args := make([]hintArg, 0, len(switches))
	for _, name := range switches {
		args = append(args, hintSwitch(name))
	}

	return hint("who", args...)
}

func explainBriefHint() hintCall { return hint("view", hintFlag("explain", briefName)) }

// channelMark is a seat's last_channel: the channel its last memory call came
// through, which every line answering no call follows.
type channelMark struct {
	LastChannel channel `json:"last_channel"`
}

func (mem *memory) channelKey(seatName string) string { return "channel/" + mem.seatKey(seatName) }

// hintChannel is the channel this run's lines are spelled for: a command's
// own, set where it is answered; in a hook, the call a tool hook follows
// where that is a memory call, else the seat's last_channel, MCP where the
// seat has none, since the plugin always ships the server.
func (mem *memory) hintChannel(ctx context.Context) channel {
	if mem.hints != "" {
		return mem.hints
	}

	mem.hints = channelMCP

	// Only a hook reaches here: every command sets its own channel first.
	payload := mem.payload()

	switch {
	case strings.HasPrefix(payload.ToolName, memoryToolPrefix):
		return mem.hints
	case payload.ToolName == "Bash" && callsMemory(payload.ToolInput.Command):
		mem.hints = channelCLI

		return mem.hints
	}

	name, _ := mem.ownSeat(ctx)

	var mark channelMark
	if mem.readSlot(mem.channelKey(name), &mark) && mark.LastChannel == channelCLI {
		mem.hints = channelCLI
	}

	return mem.hints
}

func callsMemory(command string) bool {
	calls, parsed := memoryCalls(command, false)

	return parsed && len(calls) > 0
}

// noteChannel keeps the channel a memory call came through as its seat's
// last_channel. A subagent reading on the seat's behalf leaves it: its
// channel is its own, not the seat holder's.
func (mem *memory) noteChannel(ctx context.Context, through channel) {
	mem.hints = through

	if mem.reader != "" {
		return
	}

	name, _ := mem.ownSeat(ctx)
	if name == "" {
		return
	}

	// The slot file writes nothing where the value stands.
	mem.writeSlot(mem.channelKey(name), fixedMark(channelMark{LastChannel: through}))
}
