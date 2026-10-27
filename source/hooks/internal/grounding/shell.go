package grounding

import (
	"path"
	"regexp"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/fza/agentic-helpers/source/hooks/internal/shellwalk"
)

// spoken is one stretch of shell code as the parser reads it: the command, or
// code a program inside it runs as shell (nested). Code that does not parse is
// kept as its text, unparsed.
type spoken struct {
	calls    []call
	discards bool
	nested   bool
	unparsed string
}

// call is one command's words, quoting removed; name is the base name of the
// first, lower-cased. launched is a call a launcher before it runs, whose
// directory and environment the launcher may change; certain is false for a
// word after a launcher that may be an option's value rather than the command.
// patterned marks each word the shell may expand into others, a glob or a
// brace, which the words hold only as spelled; args are the words as parsed.
type call struct {
	words     []string
	patterned []bool
	args      []*syntax.Word
	name      string
	launched  bool
	certain   bool
}

func (called call) text() string {
	return strings.Join(called.words, " ")
}

// changesDirectory reports a call the shell runs that moves it, or what it
// launches, elsewhere. An uncertain word after a launcher never moves the
// shell: a launched `cd` changes nothing for the commands after it.
func (called call) changesDirectory() bool {
	return called.certain && shellwalk.MovesDirectory(called.words)
}

// namesTool finds `sdd` or `agentic-capture` as a word in code that does not
// parse: a path may lead to it, and nothing path-like may follow.
var namesTool = regexp.MustCompile(`(?:^|[^A-Za-z0-9_.-])(?:sdd|agentic-capture)(?:[^A-Za-z0-9_./-]|$)`)

// spokenIn reads the command and every piece of code a shell inside it would
// run. A quoted argument, a heredoc body and a word the launcher's options
// leave unclear are never a call.
func spokenIn(code string, nested bool) []spoken {
	file, err := shellwalk.Parse(code)
	if err != nil {
		return []spoken{{nested: nested, unparsed: code}}
	}

	heard := &listener{}
	shellwalk.Walk(file, heard)

	found := []spoken{{calls: heard.calls, discards: discardsStream(file), nested: nested}}
	for _, inner := range heard.code {
		found = append(found, spokenIn(inner, true)...)
	}

	return found
}

type listener struct {
	calls []call
	code  []string
}

// Command keeps every call the walk offers. One a launcher may run, past
// options the walk does not know, stays uncertain: no read is credited or
// checked through it, but a write through it still owes what any write owes.
func (heard *listener) Command(found shellwalk.Call) {
	words := make([]string, 0, len(found.Args))
	patterned := make([]bool, 0, len(found.Args))

	for _, word := range found.Args {
		words = append(words, shellwalk.Spelled(word))
		patterned = append(patterned, shellwalk.Patterned(word))
	}

	heard.calls = append(heard.calls, call{
		words: words, patterned: patterned, args: found.Args, name: strings.ToLower(path.Base(words[0])),
		launched: found.Launched, certain: found.Certain,
	})
}

func (heard *listener) Code(_ string, words []*syntax.Word) {
	parts := make([]string, 0, len(words))
	for _, word := range words {
		parts = append(parts, shellwalk.Spelled(word))
	}

	heard.code = append(heard.code, strings.Join(parts, " "))
}

func (*listener) Inline(string, []*syntax.Word) {}

func (*listener) Piped(string) {}

// discardsStream reports a redirection sending output to /dev/null anywhere
// the shell reads, never in a heredoc body or a quoted argument.
func discardsStream(file *syntax.File) bool {
	found := false

	syntax.Walk(file, func(node syntax.Node) bool {
		redirect, isRedirect := node.(*syntax.Redirect)
		if !isRedirect || redirect.Word == nil {
			return !found
		}

		target, isLiteral := shellwalk.Literal(redirect.Word)
		if isLiteral && target == "/dev/null" && slices.Contains(toStream, redirect.Op) {
			found = true
		}

		return !found
	})

	return found
}

var toStream = []syntax.RedirOperator{
	syntax.RdrOut, syntax.AppOut, syntax.DplOut, syntax.RdrClob, syntax.RdrAll, syntax.AppAll,
	syntax.RdrAllClob, syntax.AppAllClob,
}

// callsCapture reports a call of `agentic-capture`. An uncertain one counts
// where it is handed an argument: `xargs grep -l agentic-capture` names the
// tool as a search term, `sudo -iD dir agentic-capture d.md` runs it.
func callsCapture(called call) bool {
	return called.name == "agentic-capture" && (called.certain || len(called.words) > 1) && !writesNoEntry(called.words[1:])
}

func callsNew(called call) bool {
	return called.name == "sdd" && len(called.words) > 1 && called.words[1] == "new" && !writesNoEntry(called.words[2:])
}

// writesNoEntry reports a flag that makes the call write nothing: a dry run,
// or the usage a `--help` or `-h` prints.
func writesNoEntry(args []string) bool {
	for _, arg := range args {
		if arg == "--dry-run" || strings.HasPrefix(arg, "--dry-run=") || arg == "--help" || arg == "-h" {
			return true
		}
	}

	return false
}

// callsAs reports whether the call runs the command spelled as words: its
// first word by any path, the rest as written. It returns the words past it.
func callsAs(called call, words []string) ([]string, bool) {
	if len(words) == 0 || len(called.words) < len(words) {
		return nil, false
	}

	if called.name != strings.ToLower(path.Base(words[0])) || !slices.Equal(called.words[1:len(words)], words[1:]) {
		return nil, false
	}

	return called.words[len(words):], true
}

// opensSubcommand reports a first argument naming a subcommand: `sdd search`
// rather than `sdd --version`.
func opensSubcommand(rest []string) bool {
	return len(rest) > 0 && rest[0] != "" && rest[0][0] >= 'a' && rest[0][0] <= 'z'
}
