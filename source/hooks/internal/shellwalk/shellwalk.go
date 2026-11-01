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

	"mvdan.cc/sh/v3/syntax"

	"github.com/fza/agentic-helpers/source/hooks/internal/alternatives"
)

var Shells = []string{"sh", "bash", "zsh", "dash", "ksh", "mksh", "fish"}

// A launcher runs its arguments as a command. Every alternative of the
// command alternatives table that launches one joins by name; one opening
// with fixed words, as the perl alarm form, is a launcher only where a call
// spells them (see prefixLaunch). noglob, nocorrect and `-` are zsh's
// precommand modifiers, and zsh's `repeat N` runs its command N times: Claude
// Code's Bash tool runs a command in zsh where that is the login shell.
// screen runs its command as words, never through a shell, so it is a
// launcher; make is none: its operands are targets, never commands.
var Launchers = append([]string{
	"sudo", "doas", "command", "builtin", "exec", "env", "nohup", "nice", "ionice",
	"xargs", "parallel", "caffeinate", "stdbuf", "unbuffer", "time", "chronic", "flock",
	"find", "arch", "noglob", "nocorrect", "-", "repeat",
	"xcrun", "sandbox-exec", "npx", "screen",
}, namedAlternatives()...)

// subcommandLaunchers launch a command only through one subcommand, found as
// the first word past the program's own options: `uv run`, `pnpm exec`,
// `launchctl submit`.
var subcommandLaunchers = map[string]string{"uv": "run", "pnpm": "exec", "launchctl": "submit"}

// stdinRunners run the code on their standard input as shell code later: at
// and batch queue it for sh.
var stdinRunners = []string{"at", "batch"}

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

var Options = withAlternatives(map[string]LauncherOptions{
	"env": {Value: []string{"-C", "--chdir", "-u", "--unset"}, Chdir: []string{"-C", "--chdir"}},
	"sudo": {
		Value: []string{
			"-u", "--user", "-g", "--group", "-C", "--close-from", "-D", "--chdir", "-h", "--host", "-p", "--prompt",
			"-r", "--role", "-t", "--type", "-T", "--command-timeout", "-U", "--other-user",
		},
		Chdir: []string{"-D", "--chdir", "-i", "--login"},
	},
	"xargs": {Value: []string{
		"-I", "-J", "-R", "-L", "-d", "--delimiter", "-E", "-a", "--arg-file", "-s", "--max-chars", "-n", "--max-args", "-P", "--max-procs",
	}},
	"nice":         {Value: []string{"-n", "--adjustment"}},
	"flock":        {Value: []string{"-w", "--timeout", "-E", "--conflict-exit-code"}},
	"ionice":       {Value: []string{"-c", "--class", "-n", "--classdata", "-p", "--pid"}},
	"xcrun":        {Value: []string{"--sdk", "--toolchain"}},
	"sandbox-exec": {Value: []string{"-f", "-n", "-p", "-D"}},
	"launchctl":    {Value: []string{"-l", "-o", "-e"}},
	"npx":          {Value: []string{"-p", "--package", "-c", "--call", "-w", "--workspace"}},
	"screen":       {Value: []string{"-c", "-e", "-h", "-p", "-S", "-s", "-t", "-T", "-X", "-Logfile"}},
	"pnpm": {
		Value: []string{
			"-C", "--dir", "--loglevel", "--resume-from", "--filter", "-F", "--filter-prod", "--test-pattern",
			"--changed-files-ignore-pattern", "--workspace-concurrency", "--reporter",
		},
		Chdir: []string{"-C", "--dir"},
	},
	"uv": {
		Value: []string{
			"--extra", "--no-extra", "--group", "--no-group", "--only-group", "--no-editable-package", "--env-file",
			"-w", "--with", "--with-editable", "--with-requirements", "--package", "--python-platform", "--index",
			"--default-index", "-i", "--index-url", "--extra-index-url", "-f", "--find-links", "--index-strategy",
			"--keyring-provider", "-P", "--upgrade-package", "--upgrade-group", "--resolution", "--prerelease",
			"--prerelease-package", "--fork-strategy", "--exclude-newer", "--exclude-newer-package",
			"--no-sources-package", "--reinstall-package", "--link-mode", "-C", "--config-setting",
			"--config-settings-package", "--no-build-isolation-package", "--no-build-package", "--no-binary-package",
			"--cache-dir", "--refresh-package", "-p", "--python", "--color", "--allow-insecure-host", "--directory",
			"--project", "--config-file",
		},
		Chdir: []string{"--directory"},
	},
})

func namedAlternatives() []string {
	var names []string

	for _, launcher := range alternatives.Launchers() {
		if launcher.Prefix == nil && launcher.Subcommand == "" {
			names = append(names, launcher.Program)
		}
	}

	return names
}

func withAlternatives(options map[string]LauncherOptions) map[string]LauncherOptions {
	for _, launcher := range alternatives.Launchers() {
		if launcher.Prefix == nil && launcher.Subcommand == "" {
			options[launcher.Program] = LauncherOptions{Value: launcher.ValueOptions}
		}
	}

	return options
}

// prefixLaunch is the index of the word a launcher opening with fixed words
// runs, as `perl -e 'alarm 9; exec @ARGV' make` runs make, or -1 where args
// open with no such launcher: any other perl code stays inline code.
func prefixLaunch(args []*syntax.Word) int {
	values := make([]alternatives.Arg, 0, len(args))
	for _, arg := range args {
		value, isLiteral := Literal(arg)
		values = append(values, alternatives.Arg{
			Text: value, Literal: isLiteral && !Patterned(arg), OneWord: OneKnownWord(arg),
		})
	}

	return alternatives.PrefixLaunch(CommandName(args[0]), values)
}

func isLauncher(args []*syntax.Word) bool {
	return slices.Contains(Launchers, CommandName(args[0])) || prefixLaunch(args) >= 0 || subcommandLaunch(args)
}

// subcommandLaunch reports whether args open with a launcher's program and
// its subcommand as fixed text, as `agentic-helpers bound` does.
func subcommandLaunch(args []*syntax.Word) bool {
	return subcommandAt(args) > 0
}

// subcommandAt is the index of a launcher's subcommand in args, or -1: right
// after the program for an alternative, past the program's own options for
// one of subcommandLaunchers (`pnpm -r exec`).
func subcommandAt(args []*syntax.Word) int {
	if len(args) < 2 {
		return -1
	}

	name := CommandName(args[0])
	subcommand, isLiteral := Literal(args[1])

	for _, launcher := range alternatives.Launchers() {
		if launcher.Subcommand != "" && launcher.Program == name && isLiteral && launcher.Subcommand == subcommand {
			return 1
		}
	}

	wanted, known := subcommandLaunchers[name]
	if !known {
		return -1
	}

	for index := 1; index < len(args); index++ {
		value, isLiteral := Literal(args[index])

		switch {
		case !isLiteral:
			return -1
		case slices.Contains(Options[name].Value, value):
			index++
		case strings.HasPrefix(value, "-"):
		case value == wanted:
			return index
		default:
			return -1
		}
	}

	return -1
}

// Certain is the index of every word of a call the shell certainly runs as a
// command: the first, and each one a launcher before it runs.
func Certain(args []*syntax.Word) []int {
	if len(args) == 0 {
		return nil
	}

	starts := []int{0}

	for start := 0; start < len(args) && isLauncher(args[start:]); {
		next := launchedAt(args[start:])
		if next < 0 {
			break
		}

		start += next
		if start < len(args) {
			starts = append(starts, start)
		}
	}

	return starts
}

// TimeLimitRuns is the index of the word a time limit opening args runs
// (timeout and every alternative standing in for it), or -1 where args open
// with none. Such a launcher bounds only how long its command runs.
func TimeLimitRuns(args []*syntax.Word) int {
	if len(args) == 0 {
		return -1
	}

	name := CommandName(args[0])

	for _, launcher := range alternatives.Launchers() {
		if !launcher.TimeLimit || launcher.Program != name {
			continue
		}

		if launcher.Subcommand != "" {
			if subcommandLaunch(args) {
				return launchedAt(args)
			}

			continue
		}

		if launcher.Prefix == nil || prefixLaunch(args) >= 0 {
			return launchedAt(args)
		}
	}

	return -1
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
// runs. Dir is the directory the call runs in, "" where the walk cannot tell.
type Call struct {
	Args     []*syntax.Word
	Launched bool
	Certain  bool
	Dir      string
}

// Visitor receives what a walk finds. Code is code another program runs as
// shell, named by its runner (`bash -c`, `eval`, `trap`, `bash stdin`), with
// the directory it runs in, "" where the walk cannot tell; Inline is code for
// an interpreter no shell parser reads; Piped is a shell reading its code from
// a pipe; Unseen is a variable arithmetic evaluates whose value is only known
// at run time. Script is the text of a script the command runs, named as
// the command names it, with the interpreter its first line names where that
// is no shell ("" for shell code) and what a walk of that text reads scripts
// through; Unread is a script it runs that the walk cannot read, and why.
// Only WalkWith reports scripts.
type Visitor interface {
	Command(call Call)
	Code(runner string, words []*syntax.Word, dir string)
	Inline(name string, words []*syntax.Word)
	Piped(name string)
	Unseen(variable string)
	Script(name string, interpreter string, text string, within Scripts)
	Unread(name string, why string)
}

func Parse(code string) (*syntax.File, error) {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(code), "")
	if err != nil {
		return nil, fmt.Errorf("parsing the command: %w", err)
	}

	return file, nil
}

// Walk visits a statement's here-documents before its command, and a pipe
// before either side, in the order the tree holds them. It reads no script.
func Walk(node syntax.Node, visitor Visitor) {
	walk(node, visitor, Scripts{}, false)
}

// WalkWith walks as Walk does, and reads every shell script the command runs
// through scripts, starting in scripts.Dir.
func WalkWith(node syntax.Node, visitor Visitor, scripts Scripts) {
	walk(node, visitor, scripts, true)
}

func walk(node syntax.Node, visitor Visitor, scripts Scripts, reading bool) {
	values := arithmetic{visitor: visitor, assigned: assignedValues(node), resolved: map[string]bool{}}
	walked := &walker{
		visitor: visitor, scripts: scripts, reading: reading, dir: scripts.Dir,
		moves: followedMoves(node), values: fixedValuesOf(node), written: map[string]writtenScript{},
	}

	syntax.Walk(node, func(visited syntax.Node) bool {
		switch typed := visited.(type) {
		case *syntax.CallExpr:
			if len(typed.Args) > 0 {
				walked.call(typed)
			}
		case *syntax.Stmt:
			if walked.moves.lost[typed] {
				walked.dir = ""
			}

			walked.records(typed)
			fed(visitor, typed, walked.dir)
		case *syntax.BinaryCmd:
			piped(visitor, typed)
		case *syntax.Assign:
			zshCode(visitor, typed)
		case *syntax.ArithmCmd:
			values.judge(typed.X)
		case *syntax.ArithmExp:
			values.judge(typed.X)
		case *syntax.LetClause:
			for _, expression := range typed.Exprs {
				values.judge(expression)
			}
		case *syntax.CStyleLoop:
			for _, expression := range []syntax.ArithmExpr{typed.Init, typed.Cond, typed.Post} {
				if expression != nil {
					values.judge(expression)
				}
			}
		case *syntax.BinaryTest:
			if slices.Contains(arithmeticTests, typed.Op) {
				values.judge(typed.X)
				values.judge(typed.Y)
			}
		}

		return true
	})
}

// zshCodeArrays are zsh's parameters whose elements are code: setting
// `functions[ls]` defines the function ls, setting `aliases[ls]` an alias the
// shell expands when it reads a later line.
var zshCodeArrays = []string{"functions", "aliases", "galiases", "saliases"}

// zshCode hands on the code an assignment to one of zshCodeArrays sets: its
// value, or each element of an array assigned whole.
func zshCode(visitor Visitor, assign *syntax.Assign) {
	if assign.Name == nil || !slices.Contains(zshCodeArrays, assign.Name.Value) {
		return
	}

	if assign.Value != nil {
		visitor.Code(assign.Name.Value, []*syntax.Word{assign.Value}, "")
	}

	if assign.Array == nil {
		return
	}

	for _, element := range assign.Array.Elems {
		if element.Value != nil {
			visitor.Code(assign.Name.Value, []*syntax.Word{element.Value}, "")
		}
	}
}

var arithmeticTests = []syntax.BinTestOperator{
	syntax.TsEql, syntax.TsNeq, syntax.TsLeq, syntax.TsGeq, syntax.TsLss, syntax.TsGtr,
}

var (
	identifier = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	number     = regexp.MustCompile(`^\s*[+-]?(?:0[xX][0-9A-Fa-f]+|[0-9]+(?:#[0-9A-Za-z@_]+)?)?\s*$`)
)

// assignedValues is every value the code gives each name, wherever it
// stands: order and branches are not read, so a name keeps every value it
// may hold. A nil value is one only known at run time: what read, mapfile,
// readarray or printf -v store, and a loop variable over the arguments or
// over a word the shell expands into others.
func assignedValues(node syntax.Node) map[string][]*syntax.Word {
	assigned := map[string][]*syntax.Word{}

	syntax.Walk(node, func(visited syntax.Node) bool {
		switch typed := visited.(type) {
		case *syntax.Assign:
			if typed.Name == nil {
				return true
			}

			if typed.Value != nil {
				assigned[typed.Name.Value] = append(assigned[typed.Name.Value], typed.Value)
			}

			if typed.Array != nil {
				for _, element := range typed.Array.Elems {
					if element.Value != nil {
						assigned[typed.Name.Value] = append(assigned[typed.Name.Value], element.Value)
					}
				}
			}
		case *syntax.CallExpr:
			for _, name := range storedNames(typed.Args) {
				assigned[name] = append(assigned[name], nil)
			}
		case *syntax.WordIter:
			if !typed.InPos.IsValid() {
				assigned[typed.Name.Value] = append(assigned[typed.Name.Value], nil)
			}

			for _, item := range typed.Items {
				if Patterned(item) {
					item = nil
				}

				assigned[typed.Name.Value] = append(assigned[typed.Name.Value], item)
			}
		}

		return true
	})

	return assigned
}

// storedNames are the variables a call stores what it reads in: read's and
// mapfile's operands, and the value of printf's -v. readValues are the
// option letters of read and mapfile taking the next word as a value; read's
// -a and zsh's -A take the array's name.
func storedNames(args []*syntax.Word) []string {
	if len(args) == 0 {
		return nil
	}

	readValues := map[string]string{"read": "dinNptu", "mapfile": "dnOsuCc", "readarray": "dnOsuCc"}

	name := CommandName(args[0])
	if name == "printf" {
		target := optionValue(args[1:], "-v")
		if target == nil || !variableName(target) {
			return nil
		}

		value, _ := Literal(target)

		return []string{value}
	}

	letters, stores := readValues[name]
	if !stores {
		return nil
	}

	var names []string

	for index := 1; index < len(args); index++ {
		value, isLiteral := Literal(args[index])

		switch {
		case !isLiteral:
		case strings.HasPrefix(value, "-") && len(value) > 1:
			last := value[len(value)-1:]
			if !strings.ContainsAny(last, letters+"aA") || index+1 == len(args) {
				continue
			}

			index++

			if strings.ContainsAny(last, "aA") && variableName(args[index]) {
				array, _ := Literal(args[index])
				names = append(names, array)
			}
		case variableName(args[index]):
			names = append(names, value)
		}
	}

	return names
}

func variableName(word *syntax.Word) bool {
	value, isLiteral := Literal(word)

	return isLiteral && value != "" && identifier.FindString(value) == value
}

// arithmetic reads the variables arithmetic names. bash and zsh evaluate a
// variable's text as an arithmetic expression in turn, and a subscript in it
// runs its command substitutions: `x='a[$(rm -rf ~)]'; (( x ))` runs rm. So a
// variable the code assigns text other than a number is handed on as code,
// `(( text ))`, and one holding a value only known at run time is reported
// unseen, since no reader can tell its text. A value that is another variable
// is followed to it; one that is an arithmetic expansion is a number. Each
// name is read once per walk.
type arithmetic struct {
	visitor  Visitor
	assigned map[string][]*syntax.Word
	resolved map[string]bool
}

func (values arithmetic) judge(expression syntax.Node) {
	queue := namesIn(expression)

	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]

		if values.resolved[name] {
			continue
		}

		values.resolved[name] = true

		for _, value := range values.assigned[name] {
			queue = append(queue, values.read(name, value)...)
		}
	}
}

// read hands on what a value assigned to name evaluates as, and returns the
// names it leads to.
func (values arithmetic) read(name string, value *syntax.Word) []string {
	if value == nil {
		values.visitor.Unseen(name)

		return nil
	}

	text, isLiteral := Literal(value)

	switch {
	case isLiteral && number.MatchString(text):
		return nil
	case isLiteral:
		code := &syntax.Word{Parts: []syntax.WordPart{&syntax.SglQuoted{Value: "(( " + text + " ))"}}}
		values.visitor.Code("arithmetic on "+name, []*syntax.Word{code}, "")

		return identifier.FindAllString(text, -1)
	}

	parts := value.Parts
	if len(parts) == 1 {
		quoted, isQuoted := parts[0].(*syntax.DblQuoted)
		if isQuoted {
			parts = quoted.Parts
		}
	}

	if len(parts) == 1 {
		parameter, isParameter := parts[0].(*syntax.ParamExp)
		if isParameter && plainParameter(parameter) {
			return []string{parameter.Param.Value}
		}
	}

	if !slices.ContainsFunc(parts, func(part syntax.WordPart) bool {
		_, isArithmetic := part.(*syntax.ArithmExp)

		return !isArithmetic
	}) {
		return nil
	}

	values.visitor.Unseen(name)

	return nil
}

func plainParameter(parameter *syntax.ParamExp) bool {
	return parameter.Param != nil && parameter.Flags == nil && parameter.NestedParam == nil &&
		parameter.Index == nil && parameter.Slice == nil && parameter.Repl == nil && parameter.Exp == nil &&
		len(parameter.Modifiers) == 0 && parameter.Names == 0 && !parameter.Excl && !parameter.Length &&
		!parameter.Width && !parameter.IsSet
}

// namesIn is every variable an arithmetic expression names, bare or as an
// expansion: `$y` holds the name `y` as text the walk visits too. A command
// substitution inside it is a call the walk reaches on its own, never a name.
func namesIn(expression syntax.Node) []string {
	var names []string

	syntax.Walk(expression, func(visited syntax.Node) bool {
		switch typed := visited.(type) {
		case *syntax.CmdSubst, *syntax.ProcSubst:
			return false
		case *syntax.Lit:
			if typed.Value != "" && identifier.FindString(typed.Value) == typed.Value {
				names = append(names, typed.Value)
			}
		}

		return true
	})

	return names
}

// offerCall offers the call, then every word after a launcher as the call it
// may run. A launcher among those words adds no words of its own: the outer
// launcher already offers every one of them. dir is where the call runs;
// launchedDir where what its launchers run does.
func offerCall(visitor Visitor, args []*syntax.Word, dir string, launchedDir string) {
	offer(visitor, Call{Args: args, Certain: true, Dir: dir})

	if !isLauncher(args) {
		return
	}

	certain := map[int]bool{}
	for _, start := range Certain(args) {
		certain[start] = true
	}

	for index := 1; index < len(args); index++ {
		offer(visitor, Call{Args: args[index:], Launched: true, Certain: certain[index], Dir: launchedDir})
	}
}

func offer(visitor Visitor, call Call) {
	visitor.Command(call)

	args := call.Args
	name := CommandName(args[0])

	switch {
	case name == "eval":
		visitor.Code("eval", args[1:], call.Dir)
	case name == "trap":
		handler := trapHandler(args[1:])
		if handler != nil {
			visitor.Code("trap", []*syntax.Word{handler}, "")
		}
	case slices.Contains(Shells, name):
		code := shellCode(args[1:])
		if code != nil {
			visitor.Code(name+" -c", []*syntax.Word{code}, call.Dir)
		}
	case IsInterpreter(name) && prefixLaunch(args) < 0:
		visitor.Inline(name, args[1:])
	case name == "sched":
		code := schedCode(args[1:])
		if len(code) > 0 {
			visitor.Code("sched", code, "")
		}
	case name == "tmux":
		tmuxCode(visitor, args[1:])
	case name == "npx":
		code := optionValue(args[1:launchedEnd(args)], "-c", "--call")
		if code != nil {
			visitor.Code("npx -c", []*syntax.Word{code}, call.Dir)
		}
	case name == "pnpm" && subcommandLaunch(args):
		// In shell mode pnpm hands its command to sh -c.
		start := launchedAt(args)
		if start > 0 && start < len(args) && optionValue(args[1:start+1], "-c", "--shell-mode") != nil {
			visitor.Code("pnpm exec -c", args[start:], call.Dir)
		}
	}
}

// launchedEnd is the index of the word a launcher runs, or the end of args
// where it runs none.
func launchedEnd(args []*syntax.Word) int {
	start := launchedAt(args)
	if start < 0 || start > len(args) {
		return len(args)
	}

	return start
}

// optionValue is the word after the first of names among args, or nil.
func optionValue(args []*syntax.Word, names ...string) *syntax.Word {
	for index, arg := range args {
		value, isLiteral := Literal(arg)
		if isLiteral && slices.Contains(names, value) && index+1 < len(args) {
			return args[index+1]
		}
	}

	return nil
}

// schedCode is the command zsh's sched runs as a string, as eval would: the
// words past its -o flag and its time. A first word starting with `-` removes
// an item and runs nothing.
func schedCode(args []*syntax.Word) []*syntax.Word {
	for index, arg := range args {
		value, isLiteral := Literal(arg)
		switch {
		case isLiteral && value == "-o":
		case isLiteral && strings.HasPrefix(value, "-"):
			return nil
		default:
			return args[index+1:]
		}
	}

	return nil
}

// tmuxCommand is a tmux command taking a shell command after its options:
// value holds every option letter taking the next word.
type tmuxCommand struct {
	name  string
	alias string
	value string
}

var tmuxCommands = []tmuxCommand{
	{name: "new-session", alias: "new", value: "cefFnstxy"},
	{name: "new-window", alias: "neww", value: "ceFnt"},
	{name: "split-window", alias: "splitw", value: "BceFlmpRsStT"},
	{name: "respawn-pane", alias: "respawnp", value: "cet"},
	{name: "respawn-window", alias: "respawnw", value: "cet"},
	{name: "display-popup", alias: "popup", value: "bcdehsStTwxy"},
	{name: "run-shell", alias: "run", value: "cdt"},
}

// tmuxCode hands on the shell command each tmux command in the call runs:
// tmux's own -c, and the words past the options of every command above,
// named by its full name, its alias, or any prefix of the name, as tmux
// accepts. Commands chain with a `;` word.
func tmuxCode(visitor Visitor, args []*syntax.Word) {
	start, values := shortOptions(args, "cfLST")

	code := values['c']
	if code != nil {
		visitor.Code("tmux -c", []*syntax.Word{code}, "")
	}

	segment := []*syntax.Word{}

	for _, arg := range args[start:] {
		value, isLiteral := Literal(arg)
		if !isLiteral || value != ";" {
			segment = append(segment, arg)

			continue
		}

		tmuxSegment(visitor, segment)

		segment = []*syntax.Word{}
	}

	tmuxSegment(visitor, segment)
}

func tmuxSegment(visitor Visitor, segment []*syntax.Word) {
	if len(segment) == 0 {
		return
	}

	name, isLiteral := Literal(segment[0])
	if !isLiteral || name == "" {
		return
	}

	for _, command := range tmuxCommands {
		if name != command.alias && !strings.HasPrefix(command.name, name) {
			continue
		}

		start, _ := shortOptions(segment[1:], command.value)
		if 1+start < len(segment) {
			visitor.Code("tmux "+name, segment[1+start:], "")
		}

		return
	}
}

// shortOptions reads args as getopt does: short options, grouped or not,
// those in value taking the rest of their word or the next word, up to the
// first operand or `--`. It returns the operand's index and each value given.
func shortOptions(args []*syntax.Word, value string) (int, map[byte]*syntax.Word) {
	values := map[byte]*syntax.Word{}

	for index := 0; index < len(args); index++ {
		word, isLiteral := Literal(args[index])
		if !isLiteral || word == "-" || !strings.HasPrefix(word, "-") {
			return index, values
		}

		if word == "--" {
			return index + 1, values
		}

	letters:
		for at := 1; at < len(word); at++ {
			if !strings.ContainsRune(value, rune(word[at])) {
				continue
			}

			switch {
			case at+1 < len(word):
				values[word[at]] = &syntax.Word{Parts: []syntax.WordPart{&syntax.SglQuoted{Value: word[at+1:]}}}
			case index+1 < len(args):
				index++
				values[word[at]] = args[index]
			}

			break letters
		}
	}

	return len(args), values
}

// launchedAt is the index of the word the launcher opening args runs: past
// its options and their values, `NAME=value` assignments and counts, or past
// find's -exec. It
// is -1 where the launcher runs nothing, as `command -v` only looks a name up,
// and may lie past the last word. A word with an expansion is the command
// itself, except to find, whose expressions take any word.
func launchedAt(args []*syntax.Word) int {
	prefixed := prefixLaunch(args)
	if prefixed >= 0 {
		return prefixed
	}

	launcher := CommandName(args[0])

	first := 1
	if at := subcommandAt(args); at > 0 {
		first = at + 1
	}

	for index := first; index < len(args); index++ {
		value, _ := Literal(args[index])

		switch {
		case launcher == "find":
			if slices.Contains([]string{"-exec", "-execdir", "-ok", "-okdir"}, value) {
				return index + 1
			}
		case launcher == "command" && (value == "-v" || value == "-V"),
			launcher == "xcrun" && (value == "-f" || value == "--find"):
			return -1
		case value == "--":
			return index + 1
		case slices.Contains(Options[launcher].Value, value) || groupedValue(launcher, value):
			index++

			// An unquoted expansion may split into the value and a command of its
			// own (`sudo -u $user` with user='root rm -rf /'): the value word is
			// then the command, as nothing tells which word runs.
			if index < len(args) && !OneKnownWord(args[index]) {
				return index
			}
		case strings.HasPrefix(value, "-") || assignment.MatchString(value) || count.MatchString(value):
		default:
			return index
		}
	}

	return -1
}

var shortGroup = regexp.MustCompile(`^-[A-Za-z0-9]{2,}$`)

// groupedValue is whether a word groups short options the last of which
// takes the next word as its value, as `screen -dmS name` does.
func groupedValue(launcher string, value string) bool {
	return shortGroup.MatchString(value) && slices.Contains(Options[launcher].Value, "-"+value[len(value)-1:])
}

// OneKnownWord is whether the shell hands the word on as one word: fixed text
// with no glob or brace, or text wholly inside double quotes whose expansions
// use no `@` or `*`, which split even there, and no `!` indirection.
func OneKnownWord(word *syntax.Word) bool {
	if Patterned(word) {
		return false
	}

	_, isLiteral := Literal(word)
	if isLiteral {
		return true
	}

	if len(word.Parts) != 1 {
		return false
	}

	quoted, isQuoted := word.Parts[0].(*syntax.DblQuoted)
	if !isQuoted {
		return false
	}

	for _, inner := range quoted.Parts {
		_, isLit := inner.(*syntax.Lit)
		if isLit {
			continue
		}

		var printed strings.Builder

		printPart(&printed, inner)

		if strings.ContainsAny(printed.String(), "@*!") {
			return false
		}
	}

	return true
}

// fed hands on the code a here-document or here-string gives a shell or an
// interpreter on its standard input, as code handed to it with -c.
func fed(visitor Visitor, statement *syntax.Stmt, dir string) {
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
		case slices.Contains(Shells, name) || slices.Contains(stdinRunners, name):
			visitor.Code(name+" stdin", []*syntax.Word{body}, dir)
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
	if isCall {
		pipedCall(visitor, call)

		return
	}

	// A compound command hands the pipe on to every call inside it that does
	// not redirect its own input.
	syntax.Walk(binary.Y, func(node syntax.Node) bool {
		statement, isStatement := node.(*syntax.Stmt)
		if !isStatement {
			return true
		}

		if redirectsInput(statement) {
			return false
		}

		inner, isInnerCall := statement.Cmd.(*syntax.CallExpr)
		if isInnerCall {
			pipedCall(visitor, inner)
		}

		return true
	})
}

func pipedCall(visitor Visitor, call *syntax.CallExpr) {
	if len(call.Args) == 0 {
		return
	}

	args := launchedShell(call.Args)
	if args == nil {
		return
	}

	name := CommandName(args[0])

	if slices.Contains(stdinRunners, name) {
		if !atReadsElsewhere(args[1:]) {
			visitor.Piped(name)
		}

		return
	}

	if replacesInput(args[1:]) || namesScript(args[1:]) {
		return
	}

	visitor.Piped(name)
}

// atReadsElsewhere is whether at or batch queues no job from its standard
// input: -f names a job file, and -l, -c, -r and -d list, print or remove
// jobs. Its options end at the first operand, as BSD at reads them, so a
// later -f is a time word; -q and -t take a value.
func atReadsElsewhere(args []*syntax.Word) bool {
	for index := 0; index < len(args); index++ {
		value, isLiteral := Literal(args[index])
		if !isLiteral || !strings.HasPrefix(value, "-") || value == "-" {
			return false
		}

		if value == "--" {
			return false
		}

		for at := 1; at < len(value); at++ {
			if strings.ContainsRune("flcrd", rune(value[at])) {
				return true
			}

			if strings.ContainsRune("qt", rune(value[at])) {
				if at+1 == len(value) {
					index++
				}

				break
			}
		}
	}

	return false
}

// redirectsInput is whether a statement's input is certainly no longer the
// pipe: it redirects descriptor 0, and only to here-documents or here-strings,
// whose code the walk judges itself. A file never counts: any name may be a
// link to /dev/stdin, made earlier in the same command or before it, and a
// duplicated descriptor (`0<&3`) may be the pipe. Every redirection counts,
// not only the last, since zsh's MULTIOS feeds all of them to the command.
func redirectsInput(statement *syntax.Stmt) bool {
	replaced := false

	for _, redirect := range statement.Redirs {
		hereDocument := slices.Contains([]syntax.RedirOperator{syntax.Hdoc, syntax.DashHdoc, syntax.WordHdoc}, redirect.Op)

		// Unnumbered, only an input operator reaches descriptor 0; numbered 0,
		// any operator does, `0>&3` duplicating as `0<&3` does.
		targetsInput := hereDocument || slices.Contains([]syntax.RedirOperator{syntax.RdrIn, syntax.RdrInOut, syntax.DplIn}, redirect.Op)
		if redirect.N != nil {
			// `00<&3` is descriptor 0 too. A named descriptor (`{fd}<<< x`) is a
			// new one the shell allocates, never the input: skipping it leaves
			// the shell reading the pipe.
			descriptor, err := strconv.Atoi(redirect.N.Value)
			targetsInput = err == nil && descriptor == 0
		}

		if !targetsInput {
			continue
		}

		if !hereDocument {
			return false
		}

		replaced = true
	}

	return replaced
}

// launchedShell is the shell, or the runner of stdinRunners, a call runs,
// through any launchers before it, with its arguments, or nil where it runs
// none.
func launchedShell(args []*syntax.Word) []*syntax.Word {
	for index, arg := range args {
		name := CommandName(arg)

		switch {
		case slices.Contains(Shells, name) || slices.Contains(stdinRunners, name):
			return args[index:]
		case index == 0 && !isLauncher(args):
			return nil
		}
	}

	return nil
}

// namesScript is whether a shell's arguments name a script for it to run: its
// first operand, unless -s sends it to standard input or the operand names
// the input itself. An option taking a
// value (-o, -O, grouped as -eo, and every long option but the shells' known
// flags) takes the next word with it.
func namesScript(args []*syntax.Word) bool {
	for index := 0; index < len(args); index++ {
		value, isLiteral := Literal(args[index])

		switch {
		case !isLiteral:
			return true
		case value == "--" || StandardInput(value):
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

var standardInput = regexp.MustCompile(`^(?:-|/dev/stdin|/dev/fd/[0-9]+|/proc/[^/]+/fd/[0-9]+)$`)

// StandardInput is whether a shell's operand names its standard input instead
// of a script: `-`, /dev/stdin, or a descriptor under /dev/fd or
// /proc/<pid>/fd, which a redirection may point at the input as well. The
// path is read cleaned, so `//dev/./stdin` names it too.
func StandardInput(value string) bool {
	return value == "-" || standardInput.MatchString(path.Clean(value))
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

	// zsh expands `=git` to git's path.
	if len(value) > 1 {
		value = strings.TrimPrefix(value, "=")
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

// Patterned reports a word the shell may turn into other words before the
// command sees it: an unquoted, unescaped `*` or `?`, an unquoted, unescaped
// `[` with a `]` after it in the same word, an extended glob, or a brace
// expansion. Bash keeps a `[` no `]` closes as text, so the test command `[`
// is fixed text; any later `]`, quoted or escaped included, counts as closing
// it, the side a guard errs on.
// Literal and Spelled read such a word as the text it spells.
func Patterned(word *syntax.Word) bool {
	for index, part := range word.Parts {
		switch typed := part.(type) {
		case *syntax.ExtGlob:
			return true
		case *syntax.Lit:
			if holdsGlob(typed.Value, closesLater(word.Parts[index+1:])) {
				return true
			}
		}
	}

	// SplitBraces reports any `{` as split, so what counts is whether it left
	// a brace expansion behind.
	split := &syntax.Word{Parts: slices.Clone(word.Parts)}
	syntax.SplitBraces(split)

	return slices.ContainsFunc(split.Parts, func(part syntax.WordPart) bool {
		_, isBrace := part.(*syntax.BraceExp)

		return isBrace
	})
}

// holdsGlob reads one unquoted part of a word; closedAfter is whether a later
// part holds a `]`.
func holdsGlob(value string, closedAfter bool) bool {
	for index := 0; index < len(value); index++ {
		switch value[index] {
		case '\\':
			index++
		case '*', '?':
			return true
		case '[':
			if closedAfter || strings.Contains(value[index+1:], "]") {
				return true
			}
		}
	}

	return false
}

// closesLater is whether a later part of the word may close a bracket: a `]` in
// its text, or any part whose value is only known at run time, since an
// expansion can produce the `]` itself.
func closesLater(parts []syntax.WordPart) bool {
	var printed strings.Builder

	for _, part := range parts {
		if !fixedPart(part) {
			return true
		}

		printPart(&printed, part)
	}

	return strings.Contains(printed.String(), "]")
}

func fixedPart(part syntax.WordPart) bool {
	switch typed := part.(type) {
	case *syntax.Lit, *syntax.SglQuoted:
		return true
	case *syntax.DblQuoted:
		for _, inner := range typed.Parts {
			if _, literal := inner.(*syntax.Lit); !literal {
				return false
			}
		}

		return true
	default:
		return false
	}
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

// ansiEscapes are the `$'...'` escapes read as a fixed string, each one
// character in bash whatever follows it.
var ansiEscapes = map[byte]byte{'n': '\n', 't': '\t', 'r': '\r', '\\': '\\', '\'': '\'', '"': '"'}

// ansiC is a single-quoted part's value, a `$'...'` one decoded as bash
// decodes it. Only a small set of escapes is read: those above, and `\x` with
// exactly two hex digits naming no NUL. Every other escape reports false, so
// the word stays unreadable: bash's octal, `\u`, `\U` and `\c` readings, a
// one-digit `\x` and a NUL truncating the word each differ in ways a guard
// reading one string where bash runs another would miss.
func ansiC(part *syntax.SglQuoted) (string, bool) {
	if !part.Dollar {
		return part.Value, true
	}

	raw := part.Value

	var decoded strings.Builder

	for index := 0; index < len(raw); index++ {
		character := raw[index]

		switch {
		case character != '\\':
			decoded.WriteByte(character)
		case index+1 >= len(raw):
			return "", false
		default:
			index++

			escaped, known := ansiEscapes[raw[index]]
			if known {
				decoded.WriteByte(escaped)

				continue
			}

			if raw[index] != 'x' || index+3 > len(raw) {
				return "", false
			}

			value, err := strconv.ParseUint(raw[index+1:index+3], 16, 8)
			if err != nil || value == 0 {
				return "", false
			}

			decoded.WriteByte(byte(value))
			index += 2
		}
	}

	return decoded.String(), true
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
