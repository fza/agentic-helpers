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
// first, lower-cased.
type call struct {
	words []string
	name  string
}

func (called call) text() string {
	return strings.Join(called.words, " ")
}

// changesDirectory reports a call moving the shell, or the command it
// launches, to another directory: `cd`, `pushd`, `popd`, or a launcher's
// chdir option (`env -C`, `sudo -D`).
func (called call) changesDirectory() bool {
	if slices.Contains([]string{"cd", "pushd", "popd"}, called.name) {
		return true
	}

	chdir := map[string]string{"env": "-C", "sudo": "-D"}[called.name]
	if chdir == "" {
		return false
	}

	// The launcher's own options end at the command it runs: the first word
	// that is no option, no option's value and no assignment.
	for index := 1; index < len(called.words); index++ {
		arg := called.words[index]

		switch {
		case strings.HasPrefix(arg, chdir) || arg == "--chdir" || strings.HasPrefix(arg, "--chdir="):
			return true
		case slices.Contains([]string{"-u", "--unset", "-g", "--group", "--user"}, arg):
			index++
		case strings.HasPrefix(arg, "-") || strings.Contains(arg, "="):
		default:
			return false
		}
	}

	return false
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

func (heard *listener) Command(found shellwalk.Call) {
	if !found.Certain {
		return
	}

	words := make([]string, 0, len(found.Args))
	for _, word := range found.Args {
		words = append(words, shellwalk.Spelled(word))
	}

	heard.calls = append(heard.calls, call{words: words, name: strings.ToLower(path.Base(words[0]))})
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

func callsCapture(called call) bool {
	return called.name == "agentic-capture" && !writesNoEntry(called.words[1:])
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
