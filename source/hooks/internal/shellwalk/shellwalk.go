// Package shellwalk walks a Bash command's syntax tree, as mvdan.cc/sh parses
// it, for the commands it runs: every simple command, every command a launcher
// (env, xargs, sudo, find, ...) runs, and every piece of code another program
// runs as shell (a shell's -c, eval, trap, a here-document fed to a shell).
// shell-guard and the grounding gate both judge a command through it, so the
// launcher and nesting rules cannot drift apart between them.
//
// The walk parses nothing nested itself: it hands the visitor the words of
// code another program runs, and the visitor decides how to read them.
package shellwalk

import (
	"bytes"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

var Shells = []string{"sh", "bash", "zsh", "dash", "ksh", "mksh", "fish"}

// A launcher runs its arguments as a command.
var Launchers = []string{
	"sudo", "doas", "command", "builtin", "exec", "env", "nohup", "nice", "ionice", "timeout",
	"xargs", "parallel", "caffeinate", "stdbuf", "unbuffer", "time", "chronic", "flock",
	"find", "arch",
}

var interpreters = []string{
	"perl", "ruby", "node", "deno", "bun", "php", "osascript", "awk", "gawk", "mawk", "nawk",
	"ssh", "lua", "tclsh", "rscript", "jshell", "swift", "watch", "su", "script",
}

// longFlags are the shells' long options that take no value.
var longFlags = []string{
	"--norc", "--noprofile", "--login", "--posix", "--noediting", "--verbose", "--restricted", "--debugger",
	"--dump-strings", "--no-config", "--private", "--interactive", "--no-execute", "--emulate-sh",
}

// LauncherOptions / Value / Chdir are the options of a launcher the walk
// knows: Value those taking the next word as their value, so that word is no
// command (`env -C dir sdd` runs sdd); Chdir those running the command in
// another directory (`env -C`, `sudo -D`, and `sudo -i`, a login shell in the
// target user's home). It is the one table both the walk and its callers read.
type LauncherOptions struct {
	Value []string
	Chdir []string
}

var Options = map[string]LauncherOptions{
	"env": {Value: []string{"-C", "--chdir", "-u", "--unset"}, Chdir: []string{"-C", "--chdir"}},
	"sudo": {
		Value: []string{
			"-u", "--user", "-g", "--group", "-C", "--close-from", "-D", "--chdir", "-h", "--host", "-p", "--prompt",
			"-r", "--role", "-t", "--type", "-T", "--command-timeout", "-U", "--other-user",
		},
		Chdir: []string{"-D", "--chdir", "-i", "--login"},
	},
	"xargs": {Value: []string{
		"-I", "-L", "-d", "--delimiter", "-E", "-a", "--arg-file", "-s", "--max-chars", "-n", "--max-args", "-P", "--max-procs",
	}},
	"nice":    {Value: []string{"-n", "--adjustment"}},
	"timeout": {Value: []string{"-s", "--signal", "-k", "--kill-after"}},
	"flock":   {Value: []string{"-w", "--timeout", "-E", "--conflict-exit-code"}},
	"ionice":  {Value: []string{"-c", "--class", "-n", "--classdata", "-p", "--pid"}},
}

// MovesDirectory reports whether a call runs in another directory than the
// shell's: `cd`, `pushd` and `popd` move the shell itself, and a launcher's
// chdir option moves what it runs. It reads every word, a short option grouped
// with others (`-iD`) and a long option carrying its value (`--chdir=dir`)
// included, so an option it misplaces still counts.
func MovesDirectory(words []string) bool {
	if len(words) == 0 {
		return false
	}

	name := strings.ToLower(path.Base(words[0]))
	if slices.Contains([]string{"cd", "pushd", "popd"}, name) {
		return true
	}

	for _, word := range words[1:] {
		for _, option := range Options[name].Chdir {
			long := strings.HasPrefix(option, "--")
			grouped := !long && strings.HasPrefix(word, "-") && !strings.HasPrefix(word, "--") &&
				strings.Contains(word[1:], option[1:])

			if word == option || long && strings.HasPrefix(word, option+"=") || grouped {
				return true
			}
		}
	}

	return false
}

var (
	assignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	count      = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?[smhd]?$`)
)

// Call is one command the shell runs, from its command word. A launcher's
// options decide which of its words starts the command it runs, so every word
// after a launcher is offered as a launched call; Certain marks the one past
// the launcher's options, assignments and counts, and every call no launcher
// runs.
type Call struct {
	Args     []*syntax.Word
	Launched bool
	Certain  bool
}

// Visitor receives what a walk finds. Code is code another program runs as
// shell, named by its runner (`bash -c`, `eval`, `trap`, `bash stdin`); Inline
// is code for an interpreter no shell parser reads; Piped is a shell reading
// its code from a pipe.
type Visitor interface {
	Command(call Call)
	Code(runner string, words []*syntax.Word)
	Inline(name string, words []*syntax.Word)
	Piped(name string)
}

func Parse(code string) (*syntax.File, error) {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(code), "")
	if err != nil {
		return nil, fmt.Errorf("parsing the command: %w", err)
	}

	return file, nil
}

// Walk visits a statement's here-documents before its command, and a pipe
// before either side, in the order the tree holds them.
func Walk(node syntax.Node, visitor Visitor) {
	syntax.Walk(node, func(visited syntax.Node) bool {
		switch typed := visited.(type) {
		case *syntax.CallExpr:
			if len(typed.Args) > 0 {
				offerCall(visitor, typed.Args)
			}
		case *syntax.Stmt:
			fed(visitor, typed)
		case *syntax.BinaryCmd:
			piped(visitor, typed)
		}

		return true
	})
}

// offerCall offers the call, then every word after a launcher as the call it
// may run. A launcher among those words adds no words of its own: the outer
// launcher already offers every one of them.
func offerCall(visitor Visitor, args []*syntax.Word) {
	offer(visitor, Call{Args: args, Certain: true})

	if !slices.Contains(Launchers, CommandName(args[0])) {
		return
	}

	certain := map[int]bool{}

	for start := 0; start < len(args) && slices.Contains(Launchers, CommandName(args[start])); {
		next := launchedAt(args[start:])
		if next < 0 {
			break
		}

		start += next
		certain[start] = true
	}

	for index := 1; index < len(args); index++ {
		offer(visitor, Call{Args: args[index:], Launched: true, Certain: certain[index]})
	}
}

func offer(visitor Visitor, call Call) {
	visitor.Command(call)

	args := call.Args
	name := CommandName(args[0])

	switch {
	case name == "eval":
		visitor.Code("eval", args[1:])
	case name == "trap":
		handler := trapHandler(args[1:])
		if handler != nil {
			visitor.Code("trap", []*syntax.Word{handler})
		}
	case slices.Contains(Shells, name):
		code := shellCode(args[1:])
		if code != nil {
			visitor.Code(name+" -c", []*syntax.Word{code})
		}
	case IsInterpreter(name):
		visitor.Inline(name, args[1:])
	}
}

// launchedAt is the index of the word the launcher opening args runs: past
// its options and their values, `NAME=value` assignments and counts, or past
// find's -exec. It
// is -1 where the launcher runs nothing, as `command -v` only looks a name up,
// and may lie past the last word. A word with an expansion is the command
// itself, except to find, whose expressions take any word.
func launchedAt(args []*syntax.Word) int {
	launcher := CommandName(args[0])

	for index := 1; index < len(args); index++ {
		value, _ := Literal(args[index])

		switch {
		case launcher == "find":
			if slices.Contains([]string{"-exec", "-execdir", "-ok", "-okdir"}, value) {
				return index + 1
			}
		case launcher == "command" && (value == "-v" || value == "-V"):
			return -1
		case value == "--":
			return index + 1
		case slices.Contains(Options[launcher].Value, value):
			index++
		case strings.HasPrefix(value, "-") || assignment.MatchString(value) || count.MatchString(value):
		default:
			return index
		}
	}

	return -1
}

// fed hands on the code a here-document or here-string gives a shell or an
// interpreter on its standard input, as code handed to it with -c.
func fed(visitor Visitor, statement *syntax.Stmt) {
	call, isCall := statement.Cmd.(*syntax.CallExpr)
	if !isCall || len(call.Args) == 0 {
		return
	}

	name := CommandName(call.Args[0])

	for _, redirect := range statement.Redirs {
		if redirect.Op != syntax.Hdoc && redirect.Op != syntax.DashHdoc && redirect.Op != syntax.WordHdoc {
			continue
		}

		body := redirect.Hdoc
		if body == nil {
			body = redirect.Word
		}

		switch {
		case body == nil:
		case slices.Contains(Shells, name):
			visitor.Code(name+" stdin", []*syntax.Word{body})
		case IsInterpreter(name):
			visitor.Inline(name, []*syntax.Word{body})
		}
	}
}

// piped reports a shell reading its code from a pipe: code no walk can read.
func piped(visitor Visitor, binary *syntax.BinaryCmd) {
	if binary.Op != syntax.Pipe && binary.Op != syntax.PipeAll || binary.Y == nil {
		return
	}

	call, isCall := binary.Y.Cmd.(*syntax.CallExpr)
	if !isCall || len(call.Args) == 0 {
		return
	}

	args := launchedShell(call.Args)
	if args == nil {
		return
	}

	if replacesInput(args[1:]) || namesScript(args[1:]) {
		return
	}

	visitor.Piped(CommandName(args[0]))
}

// launchedShell is the shell a call runs, through any launchers before it,
// with its arguments, or nil where it runs none.
func launchedShell(args []*syntax.Word) []*syntax.Word {
	for index, arg := range args {
		name := CommandName(arg)

		switch {
		case slices.Contains(Shells, name):
			return args[index:]
		case index == 0 && !slices.Contains(Launchers, name):
			return nil
		}
	}

	return nil
}

// namesScript is whether a shell's arguments name a script for it to run: its
// first operand, unless -s sends it to standard input. An option taking a
// value (-o, -O, grouped as -eo, and every long option but the shells' known
// flags) takes the next word with it.
func namesScript(args []*syntax.Word) bool {
	for index := 0; index < len(args); index++ {
		value, isLiteral := Literal(args[index])

		switch {
		case !isLiteral:
			return true
		case value == "-" || value == "--":
			return false
		case slices.Contains(longFlags, value):
		case strings.HasPrefix(value, "--"):
			// A long option it does not know may take a value: the next word goes with it.
			index++
		case strings.HasPrefix(value, "-") || strings.HasPrefix(value, "+"):
			flags := strings.TrimLeft(value, "-+")
			if strings.Contains(flags, "s") {
				return false
			}

			// A grouped option taking a value, as -eo pipefail or fish's -C init code,
			// takes the next word.
			if strings.ContainsAny(flags, "oOC") {
				index++
			}
		default:
			return true
		}
	}

	return false
}

// trapHandler is the code trap sets, past its options, or nil where it sets none.
func trapHandler(args []*syntax.Word) *syntax.Word {
	for index, arg := range args {
		value, isLiteral := Literal(arg)
		if isLiteral && (value == "--" || value == "-p" || value == "-l" || value == "-P") {
			continue
		}

		if index+1 < len(args) || !isLiteral {
			return arg
		}

		return nil
	}

	return nil
}

// replacesInput is whether a shell is handed its code with -c or --command,
// and so reads none from its standard input; fish's -C init code runs before
// the input is read.
func replacesInput(args []*syntax.Word) bool {
	for _, arg := range args {
		value, _ := Literal(arg)
		if value == "--command" || (strings.HasPrefix(value, "-") && !strings.HasPrefix(value, "--") && strings.Contains(value, "c")) {
			return true
		}
	}

	return false
}

// shellCode is the word a shell runs as code: the one after an option
// carrying c, or C, fish's init command, or the long --command and
// --init-command.
func shellCode(args []*syntax.Word) *syntax.Word {
	for index, arg := range args {
		value, _ := Literal(arg)

		short := strings.HasPrefix(value, "-") && !strings.HasPrefix(value, "--") && strings.ContainsAny(value, "cC")
		if short || value == "--command" || value == "--init-command" {
			if index+1 < len(args) {
				return args[index+1]
			}

			return nil
		}
	}

	return nil
}

func IsInterpreter(name string) bool {
	return slices.Contains(interpreters, name) || strings.HasPrefix(name, "python")
}

// CommandName is the base name of the word naming a command, lower-cased: a
// case-insensitive filesystem, as macOS keeps by default, runs `RM` as `rm`.
func CommandName(word *syntax.Word) string {
	value, isLiteral := Literal(word)
	if !isLiteral {
		return ""
	}

	return strings.ToLower(path.Base(value))
}

// Literal returns the word's value when it carries no expansion: plain text,
// single quotes, `$'...'` whose escapes resolve to a fixed string, and double
// quotes around plain text. A backslash inside double quotes is dropped before
// any character, which reads `"r\m"` as `rm` where bash keeps the backslash:
// the side a guard errs on.
func Literal(word *syntax.Word) (string, bool) {
	var value strings.Builder

	for _, part := range word.Parts {
		switch typed := part.(type) {
		case *syntax.Lit:
			value.WriteString(unescape(typed.Value))
		case *syntax.SglQuoted:
			decoded, resolved := ansiC(typed)
			if !resolved {
				return "", false
			}

			value.WriteString(decoded)
		case *syntax.DblQuoted:
			for _, inner := range typed.Parts {
				innerLit, isLit := inner.(*syntax.Lit)
				if !isLit {
					return "", false
				}

				value.WriteString(unescape(innerLit.Value))
			}
		default:
			return "", false
		}
	}

	return value.String(), true
}

// Spelled is the word with its quoting removed, as Literal reads it, and every
// expansion kept as the command spells it: `"$HOME/bin/sdd"` reads
// `$HOME/bin/sdd`, and code handed to `bash -c "sdd new $title"` reads as the
// shell would parse it. A `$'...'` whose escapes do not resolve stays spelled
// as an expansion too.
func Spelled(word *syntax.Word) string {
	var value strings.Builder

	for _, part := range word.Parts {
		spellPart(&value, part)
	}

	return value.String()
}

func spellPart(value *strings.Builder, part syntax.WordPart) {
	switch typed := part.(type) {
	case *syntax.Lit:
		value.WriteString(unescape(typed.Value))
	case *syntax.SglQuoted:
		decoded, resolved := ansiC(typed)
		if !resolved {
			printPart(value, part)

			return
		}

		value.WriteString(decoded)
	case *syntax.DblQuoted:
		for _, inner := range typed.Parts {
			spellPart(value, inner)
		}
	default:
		printPart(value, part)
	}
}

func printPart(value *strings.Builder, part syntax.WordPart) {
	var buffer bytes.Buffer

	err := syntax.NewPrinter().Print(&buffer, part)
	if err == nil {
		value.WriteString(buffer.String())
	}
}

// ansiC is a single-quoted part's value, a `$'...'` one decoded as bash
// decodes it. It reports false where an escape has no reading mvdan.cc/sh
// gives as bash does: `\c`, a control character bash builds from what follows,
// an octal escape past one byte, and a Unicode escape naming no character.
func ansiC(part *syntax.SglQuoted) (string, bool) {
	if !part.Dollar {
		return part.Value, true
	}

	if !fixedEscapes(part.Value) {
		return "", false
	}

	decoded, err := expand.Literal(nil, &syntax.Word{Parts: []syntax.WordPart{part}})
	if err != nil {
		return "", false
	}

	return decoded, true
}

func fixedEscapes(raw string) bool {
	for index := 0; index < len(raw)-1; index++ {
		if raw[index] != '\\' {
			continue
		}

		index++

		switch escape := raw[index]; {
		case escape == 'c':
			return false
		case escape >= '0' && escape <= '7':
			value, _ := strconv.ParseUint(leading(raw[index:], 3, "01234567"), 8, 32)
			if value > 0o377 {
				return false
			}
		case escape == 'u' || escape == 'U':
			width := 4
			if escape == 'U' {
				width = 8
			}

			value, _ := strconv.ParseUint(leading(raw[index+1:], width, "0123456789abcdefABCDEF"), 16, 32)
			if !utf8.ValidRune(rune(value)) {
				return false
			}
		}
	}

	return true
}

// leading is the longest run, up to limit, of text's opening characters that
// are in set.
func leading(text string, limit int, set string) string {
	end := 0
	for end < len(text) && end < limit && strings.IndexByte(set, text[end]) >= 0 {
		end++
	}

	return text[:end]
}

func unescape(value string) string {
	var result strings.Builder

	escaped := false
	for _, character := range value {
		if character == '\\' && !escaped {
			escaped = true

			continue
		}

		escaped = false
		result.WriteRune(character)
	}

	return result.String()
}

// Text is the words as the command spells them, space-separated.
func Text(words ...*syntax.Word) string {
	printed := make([]string, 0, len(words))

	for _, word := range words {
		var buffer bytes.Buffer

		err := syntax.NewPrinter().Print(&buffer, word)
		if err == nil {
			printed = append(printed, buffer.String())
		}
	}

	return strings.Join(printed, " ")
}
