package memory

import (
	"context"
	"slices"
	"sort"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/shellwalk"
)

// agentFlag is the hidden flag the PreToolUse hook sets on a full subagent's
// `agentic-memory` call, naming the agent whose seat the call works on. A
// command carries no other trace of the agent calling it.
const agentFlag = "--agent"

// memoryCall is one `agentic-memory` call a Bash command makes: its verb, and
// where the words after the program name stand in the command, for a call the
// command itself makes rather than code another program runs.
type memoryCall struct {
	verb   string
	nested bool
	// at is the offset just past the program name; through is the offset past
	// an `--agent` the call already carries, at where it carries none.
	at, through int
	flagged     bool
}

type memoryListener struct {
	calls  []memoryCall
	code   []string
	nested bool
}

// Command keeps every call of agentic-memory the walk offers: a launched one
// counts where the launcher may run it, as `agentic-helpers bound 30s
// agentic-memory new` does.
func (heard *memoryListener) Command(found shellwalk.Call) {
	args := found.Args
	if shellwalk.CommandName(args[0]) != self || (!found.Certain && len(args) < 2) {
		return
	}

	held := memoryCall{nested: heard.nested, at: int(args[0].End().Offset())}
	held.through = held.at
	rest := args[1:]

	first, _ := wordAt(rest, 0)

	switch {
	case first == agentFlag && len(rest) > 1:
		held.flagged, held.through = true, int(rest[1].End().Offset())
		rest = rest[2:]
	case strings.HasPrefix(first, agentFlag+"="):
		held.flagged, held.through = true, int(rest[0].End().Offset())
		rest = rest[1:]
	}

	held.verb, _ = wordAt(rest, 0)
	heard.calls = append(heard.calls, held)
}

func wordAt(words []*syntax.Word, index int) (string, bool) {
	if index >= len(words) {
		return "", false
	}

	return shellwalk.Literal(words[index])
}

func (heard *memoryListener) Code(_ string, words []*syntax.Word, _ string) {
	parts := make([]string, 0, len(words))
	for _, word := range words {
		parts = append(parts, shellwalk.Spelled(word))
	}

	heard.code = append(heard.code, strings.Join(parts, " "))
}

// Inline is code for an interpreter: one naming agentic-memory runs it where
// no flag can reach.
func (heard *memoryListener) Inline(_ string, words []*syntax.Word) {
	for _, word := range words {
		if strings.Contains(shellwalk.Spelled(word), self) {
			heard.calls = append(heard.calls, memoryCall{nested: true})

			return
		}
	}
}

func (*memoryListener) Piped(string) {}

func (*memoryListener) Unseen(string) {}

// Script and Unread never come: the plain walk reads no script.
func (*memoryListener) Script(string, string, string, shellwalk.Scripts) {}

func (*memoryListener) Unread(string, string) {}

// memoryCalls is every agentic-memory call the command makes, those of code
// a shell inside it runs marked nested; false where the command does not
// parse.
func memoryCalls(command string, nested bool) ([]memoryCall, bool) {
	file, err := shellwalk.Parse(command)
	if err != nil {
		return nil, false
	}

	heard := &memoryListener{nested: nested}
	shellwalk.Walk(file, heard)

	found := heard.calls

	for _, inner := range heard.code {
		calls, parsed := memoryCalls(inner, true)
		if !parsed {
			calls = []memoryCall{{nested: true}}
		}

		found = append(found, calls...)
	}

	return found, true
}

// guardShell answers the PreToolUse hook on Bash: an `--agent` the main
// thread types is refused; a subagent's agentic-memory call its level does not
// allow is refused, and a full one's every call gains `--agent` naming it,
// any it carried replaced. A call a script file makes is out of reach: it
// works on the session's own seat. Main answered a command naming no
// agentic-memory before the store was looked up.
func (mem *memory) guardShell(ctx context.Context, payload hookio.Payload) int {
	command := payload.ToolInput.Command
	calls, parsed := memoryCalls(command, false)

	if payload.AgentID == "" {
		if slices.ContainsFunc(calls, func(held memoryCall) bool { return held.flagged }) {
			_ = hookio.Deny(mem.streams.Out, self+": --agent is set by the hook for a subagent at level full, "+
				"never typed; run "+self+" without it")
		}

		return 0
	}

	if !parsed {
		_ = hookio.Deny(mem.streams.Out, self+": the command does not parse, so its "+self+" calls cannot be "+
			"judged for the subagent; write it so that it parses")

		return 0
	}

	if len(calls) == 0 {
		return 0
	}

	record := mem.subagent(ctx, payload)

	for _, held := range calls {
		refusal := subagentRefusal(held.verb, record.level())
		if refusal == "" && held.nested && record.level() == levelFull && !slices.Contains(subagentReads, held.verb) {
			refusal = self + ": run " + self + " directly, not inside code another program runs: only a direct " +
				"call can be routed to the subagent's seat"
		}

		if refusal != "" {
			_ = hookio.Deny(mem.streams.Out, refusal)

			return 0
		}
	}

	// Below full a typed --agent is replaced too: the run then names the
	// calling subagent, which the command line refuses, never a sibling's seat.
	full := record.level() == levelFull
	if !full && !slices.ContainsFunc(calls, func(held memoryCall) bool { return held.flagged }) {
		return 0
	}

	if slices.ContainsFunc(calls, func(held memoryCall) bool { return held.nested && held.flagged }) {
		_ = hookio.Deny(mem.streams.Out, self+": --agent is set by the hook, never typed; run "+self+" without it")

		return 0
	}

	updated, err := payload.ToolInput.WithField("command", flagCalls(command, calls, payload.AgentID, full))
	if err != nil {
		return 0
	}

	_ = hookio.Rewrite(mem.streams.Out, updated)

	return 0
}

// flagCalls puts `--agent <id>` after the program name of every call the
// command makes itself, in place of any `--agent` it carried; where every is
// false, only of those carrying one.
func flagCalls(command string, calls []memoryCall, agentID string, every bool) string {
	var direct []memoryCall

	for _, held := range calls {
		if !held.nested && (every || held.flagged) && !slices.ContainsFunc(direct, func(other memoryCall) bool { return other.at == held.at }) {
			direct = append(direct, held)
		}
	}

	sort.Slice(direct, func(a, b int) bool { return direct[a].at > direct[b].at })

	for _, held := range direct {
		command = command[:held.at] + " " + agentFlag + " " + agentID + command[held.through:]
	}

	return command
}
