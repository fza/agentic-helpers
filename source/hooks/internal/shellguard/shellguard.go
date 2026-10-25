// Package shellguard decides whether a Bash call Claude Code is about to run
// may go ahead. It holds removals (`rm`, `rmdir`, `unlink`, `git rm`) to the
// project and /tmp, bans `mktemp`, and asks before `find -delete`; each guard
// has its own plugin option.
//
// The command is parsed as bash, so `rm` inside a quoted argument of another
// command (a sed script, a grep pattern, a commit message) is text and passes.
// Code handed to a shell's -c or to eval is parsed again as shell. Inline code
// for any other interpreter cannot be parsed, so naming a removal or mktemp
// there is judged by the nested-code guard.
//
// Unlike every other piece it has no directory to wait for: it judges every
// Bash call of every project.
package shellguard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
)

const self = "agentic-shell-guard"

type Decision int

const (
	Allow Decision = iota
	Ask
	Deny
)

type Verdict struct {
	Decision Decision
	Reason   string
}

// Env / ProjectDir is $CLAUDE_PROJECT_DIR, the directory the session started
// in. The payload's cwd follows every cd, so it only anchors relative targets
// and never widens the project. Settings are the files the plugin's options
// are read from.
type Env struct {
	ProjectDir string
	Home       string
	Tmp        string
	Settings   []string
}

type Streams struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// Options is every guard's setting: the outside-project and find -delete
// guards ask, deny or are off; every other guard denies when on.
type Options struct {
	OutsideProject string
	FindDelete     string
	LoneRemoval    bool
	Expansion      bool
	NestedCode     bool
	Mktemp         bool
	// Scratch is whether the project carries the .tmp/ the mktemp refusal
	// points at.
	Scratch bool
}

func optionsFrom(read pluginconfig.Options) Options {
	return Options{
		OutsideProject: read.ShellGuardOutsideProject, FindDelete: read.ShellGuardFindDelete,
		LoneRemoval: read.ShellGuardLoneRemoval, Expansion: read.ShellGuardExpansion,
		NestedCode: read.ShellGuardNestedCode, Mktemp: read.ShellGuardMktemp,
	}
}

const (
	reasonMktemp        = "mktemp is banned; write scratch files under the project's .tmp/ instead"
	reasonMktempNoTmp   = "mktemp is banned; create the project's .tmp/ (gitignored) and write scratch files under it"
	reasonNotAlone      = "rm runs alone: no command before or after it, no pipe, redirection, subshell or wrapper; run it as its own Bash call with literal targets"
	reasonExpansion     = "refusing rm with a shell variable or command substitution"
	reasonUnparsable    = "cannot parse the command; refusing"
	reasonFindDelete    = "find -delete"
	whyNotAlone         = "not alone"
	whyExpansion        = "expansion"
	whyNestedCode       = "nested code"
	labelCannotPlace    = "cannot place: "
	labelOutsideProject = "outside project: "
	labelNoProject      = "no project: "
)

var removers = []string{"rm", "rmdir", "unlink"}

var shells = []string{"sh", "bash", "zsh", "dash", "ksh", "mksh", "fish"}

// A launcher runs its arguments as a command: whatever it names is judged as
// if it stood alone.
var launchers = []string{
	"sudo", "doas", "command", "builtin", "exec", "env", "nohup", "nice", "ionice", "timeout",
	"xargs", "parallel", "caffeinate", "stdbuf", "unbuffer", "time", "chronic", "flock",
	"find", "arch",
}

var interpreters = []string{
	"perl", "ruby", "node", "deno", "bun", "php", "osascript", "awk", "gawk", "mawk", "nawk",
	"ssh", "lua", "tclsh", "rscript", "jshell", "swift", "watch", "su", "script",
}

// namesRemoval matches the removal calls of the shell and of the interpreters'
// own libraries, as a word.
var (
	namesRemoval = regexp.MustCompile(`(?i)(^|[^A-Za-z0-9_-])(rm|rmdir|unlink|rmtree|rmSync|rmdirSync|unlinkSync|` +
		`removeSync|remove_dir_all|remove_tree|removedirs|rm_rf|rm_r)([^A-Za-z0-9_-]|$)`)
	namesMktemp = regexp.MustCompile(`(^|[^A-Za-z0-9_-])mktemp([^A-Za-z0-9_-]|$)`)
)

type payload struct {
	Cwd       string `json:"cwd"`
	ToolInput struct {
		Command *string `json:"command"`
	} `json:"tool_input"`
}

type answer struct {
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

// Main answers one PreToolUse call. A hook exiting with anything but 0 or 2
// lets the command run, so every failure to read the call, and any panic of
// the parser, exits 2.
func Main(ctx context.Context, env Env, streams Streams) (code int) {
	defer func() {
		failure := recover()
		if failure != nil {
			_, _ = fmt.Fprintln(streams.Err, self+": internal failure; refusing")
			code = 2
		}
	}()

	var call payload

	err := json.NewDecoder(streams.In).Decode(&call)
	if err != nil || call.ToolInput.Command == nil {
		_, _ = fmt.Fprintln(streams.Err, self+": could not read the command; refusing")

		return 2
	}

	read, err := pluginconfig.Read(env.Settings)
	if err != nil {
		read = pluginconfig.Defaults()
	}

	options := optionsFrom(read)

	info, err := os.Stat(filepath.Join(env.ProjectDir, ".tmp"))
	options.Scratch = env.ProjectDir != "" && err == nil && info.IsDir()

	verdict := Judge(ctx, *call.ToolInput.Command, call.Cwd, env, options)
	if verdict.Decision == Allow {
		return 0
	}

	var reply answer

	reply.HookSpecificOutput.HookEventName = "PreToolUse"
	reply.HookSpecificOutput.PermissionDecision = "ask"
	if verdict.Decision == Deny {
		reply.HookSpecificOutput.PermissionDecision = "deny"
	}
	reply.HookSpecificOutput.PermissionDecisionReason = self + ": " + verdict.Reason

	err = json.NewEncoder(streams.Out).Encode(reply)
	if err != nil {
		_, _ = fmt.Fprintln(streams.Err, self+": could not answer; refusing")

		return 2
	}

	return 0
}

// verdicts gathers what each guard found; the strongest decision wins, with
// the first reason given for it.
type verdicts struct {
	decided Verdict
}

func (held *verdicts) add(verdict Verdict) {
	if verdict.Decision > held.decided.Decision {
		held.decided = verdict
	}
}

// mode is the verdict a guard set to ask, deny or off gives.
func mode(setting string, reason string) Verdict {
	switch setting {
	case pluginconfig.ShellGuardDeny:
		return Verdict{Decision: Deny, Reason: reason}
	case pluginconfig.ShellGuardOff:
		return Verdict{Decision: Allow}
	}

	return Verdict{Decision: Ask, Reason: reason}
}

// cannotPlace is the outside-project verdict on a removal a switched-off
// supporting guard no longer denies: its target cannot be placed before it
// runs.
func cannotPlace(options Options, target string, why string) Verdict {
	return mode(options.OutsideProject, fmt.Sprintf("%s%s (%s)", labelCannotPlace, target, why))
}

func Judge(ctx context.Context, command string, workingDir string, env Env, options Options) Verdict {
	file, err := parse(command)
	if err != nil {
		return Verdict{Decision: Deny, Reason: reasonUnparsable}
	}

	call, targetsFrom := loneRemoval(file)
	if call != nil {
		return judgeRemoval(ctx, call.Args[targetsFrom:], workingDir, env, options)
	}

	inspection := inspector{options: options}
	inspection.walk(file)

	return inspection.found.decided
}

func parse(code string) (*syntax.File, error) {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(code), "")
	if err != nil {
		return nil, fmt.Errorf("parsing the command: %w", err)
	}

	return file, nil
}

// loneRemoval returns the call when the whole command is one removal and
// nothing else: one statement, no redirection, no assignment, not negated, not
// in the background.
func loneRemoval(file *syntax.File) (*syntax.CallExpr, int) {
	if len(file.Stmts) != 1 {
		return nil, 0
	}

	statement := file.Stmts[0]
	if statement.Negated || statement.Background || statement.Coprocess || statement.Disown || len(statement.Redirs) > 0 {
		return nil, 0
	}

	call, isCall := statement.Cmd.(*syntax.CallExpr)
	if !isCall || len(call.Assigns) > 0 || len(call.Args) == 0 {
		return nil, 0
	}

	name := commandName(call.Args[0])
	if slices.Contains(removers, name) {
		return call, 1
	}

	if name == "git" && len(call.Args) > 1 {
		subcommand, isLiteral := literal(call.Args[1])
		if isLiteral && subcommand == "rm" {
			return call, 2
		}
	}

	return nil, 0
}

// inspector walks a command. Inside code another program runs as shell
// (inside), a removal or mktemp is only noted: the nested-code guard judges
// it.
type inspector struct {
	options Options
	found   verdicts
	inside  bool
	removes bool
	mktemps bool
}

func (inspection *inspector) walk(node syntax.Node) {
	syntax.Walk(node, func(visited syntax.Node) bool {
		switch typed := visited.(type) {
		case *syntax.CallExpr:
			if len(typed.Args) > 0 {
				inspection.command(typed.Args)
			}
		case *syntax.Stmt:
			inspection.fed(typed)
		case *syntax.BinaryCmd:
			inspection.piped(typed)
		}

		return true
	})
}

// fed judges the code a here-document or here-string hands a shell or an
// interpreter on its standard input, as code handed to it with -c.
func (inspection *inspector) fed(statement *syntax.Stmt) {
	call, isCall := statement.Cmd.(*syntax.CallExpr)
	if !isCall || len(call.Args) == 0 {
		return
	}

	name := commandName(call.Args[0])

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
		case slices.Contains(shells, name):
			inspection.nested(name+" stdin", []*syntax.Word{body})
		case isInterpreter(name):
			inspection.inline(name, []*syntax.Word{body})
		}
	}
}

// piped judges a shell reading its code from a pipe: code the guard cannot
// read.
func (inspection *inspector) piped(binary *syntax.BinaryCmd) {
	if binary.Op != syntax.Pipe && binary.Op != syntax.PipeAll || binary.Y == nil {
		return
	}

	call, isCall := binary.Y.Cmd.(*syntax.CallExpr)
	if !isCall || len(call.Args) == 0 {
		return
	}

	args := launched(call.Args)
	if args == nil {
		return
	}

	name := commandName(args[0])
	if shellCode(args[1:]) != nil || namesScript(args[1:]) {
		return
	}

	inspection.unreadable(name+" stdin", fmt.Sprintf("cannot read the code piped to %s; refusing", name))
}

// launched is the shell a call runs, through any launchers before it, with its
// arguments, or nil where it runs none.
func launched(args []*syntax.Word) []*syntax.Word {
	for index, arg := range args {
		name := commandName(arg)

		switch {
		case slices.Contains(shells, name):
			return args[index:]
		case index == 0 && !slices.Contains(launchers, name):
			return nil
		}
	}

	return nil
}

// namesScript is whether a shell's arguments name a script for it to run: its
// first operand, unless -s sends it to standard input. An option taking a
// value (-o, +o, -O, +O) takes the next word with it.
func namesScript(args []*syntax.Word) bool {
	for index := 0; index < len(args); index++ {
		value, isLiteral := literal(args[index])

		switch {
		case !isLiteral:
			return true
		case value == "-" || value == "--":
			return false
		case value == "-o" || value == "+o" || value == "-O" || value == "+O":
			index++
		case strings.HasPrefix(value, "-") || strings.HasPrefix(value, "+"):
			if strings.Contains(strings.TrimLeft(value, "-+"), "s") {
				return false
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
		value, isLiteral := literal(arg)
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

// removal is a removal that is not the whole command, its targets from
// args[from:].
func (inspection *inspector) removal(args []*syntax.Word, from int) {
	if inspection.inside {
		inspection.removes = true

		return
	}

	if inspection.options.LoneRemoval {
		inspection.found.add(Verdict{Decision: Deny, Reason: reasonNotAlone})

		return
	}

	inspection.found.add(cannotPlace(inspection.options, text(args[min(from, len(args)):]...), whyNotAlone))
}

func (inspection *inspector) command(args []*syntax.Word) {
	name := commandName(args[0])
	options := inspection.options

	switch {
	case slices.Contains(removers, name):
		inspection.removal(args, 1)
	case name == "mktemp":
		inspection.mktemp()
	case name == "git":
		for index, arg := range args[1:] {
			value, isLiteral := literal(arg)
			if isLiteral && value == "rm" {
				inspection.removal(args, index+2)
			}
		}
	case name == "eval":
		inspection.nested("eval", args[1:])
	case name == "trap":
		handler := trapHandler(args[1:])
		if handler != nil {
			inspection.nested("trap", []*syntax.Word{handler})
		}
	case slices.Contains(shells, name):
		code := shellCode(args[1:])
		if code != nil {
			inspection.nested(name+" -c", []*syntax.Word{code})
		}
	case isInterpreter(name):
		inspection.inline(name, args[1:])
	}

	if name == "find" {
		for _, arg := range args[1:] {
			value, _ := literal(arg)
			if value == "-delete" {
				inspection.found.add(mode(options.FindDelete, reasonFindDelete))
			}
		}
	}

	if slices.Contains(launchers, name) {
		for index := 1; index < len(args); index++ {
			if isJudged(commandName(args[index])) {
				inspection.command(args[index:])
			}
		}
	}
}

func (inspection *inspector) mktemp() {
	if inspection.inside {
		inspection.mktemps = true

		return
	}

	if !inspection.options.Mktemp {
		return
	}

	reason := reasonMktemp
	if !inspection.options.Scratch {
		reason = reasonMktempNoTmp
	}

	inspection.found.add(Verdict{Decision: Deny, Reason: reason})
}

// inline judges code for an interpreter the guard cannot parse: naming a
// removal or mktemp there denies while the nested-code guard is on.
func (inspection *inspector) inline(name string, args []*syntax.Word) {
	for _, arg := range args {
		code := text(arg)
		removes := namesRemoval.MatchString(code)

		switch {
		case !removes && !namesMktemp.MatchString(code):
		case inspection.options.NestedCode:
			inspection.found.add(Verdict{
				Decision: Deny, Reason: fmt.Sprintf("inline code for %s names a removal or mktemp; refusing", name),
			})
		case removes:
			inspection.found.add(cannotPlace(inspection.options, "code for "+name, whyNestedCode))
		}
	}
}

// nested judges code another program runs as shell. Code the guard cannot
// read as a literal, or cannot parse, denies: it could be anything. With the
// nested-code guard off, a removal in it, or code it cannot read, takes the
// outside-project verdict, and the rest passes.
func (inspection *inspector) nested(runner string, words []*syntax.Word) {
	parts := make([]string, 0, len(words))
	for _, word := range words {
		value, isLiteral := literal(word)
		if !isLiteral {
			inspection.unreadable(runner, fmt.Sprintf("cannot read the code handed to %s; refusing", runner))

			return
		}

		parts = append(parts, value)
	}

	code := strings.Join(parts, " ")

	file, err := parse(code)
	if err != nil {
		inspection.unreadable(runner, fmt.Sprintf("cannot parse the code handed to %s; refusing", runner))

		return
	}

	inner := inspector{options: inspection.options, inside: true}
	inner.walk(file)
	inspection.found.add(inner.found.decided)

	switch {
	case !inner.removes && !inner.mktemps:
	case inspection.options.NestedCode:
		inspection.found.add(Verdict{
			Decision: Deny, Reason: fmt.Sprintf("code handed to %s holds a removal or mktemp; refusing", runner),
		})
	case inner.removes:
		inspection.found.add(cannotPlace(inspection.options, "code handed to "+runner, whyNestedCode))
	}
}

func (inspection *inspector) unreadable(runner string, reason string) {
	if inspection.options.NestedCode {
		inspection.found.add(Verdict{Decision: Deny, Reason: reason})

		return
	}

	inspection.found.add(cannotPlace(inspection.options, "code handed to "+runner, whyNestedCode))
}

func shellCode(args []*syntax.Word) *syntax.Word {
	for index, arg := range args {
		value, _ := literal(arg)
		if strings.HasPrefix(value, "-") && !strings.HasPrefix(value, "--") && strings.Contains(value, "c") {
			if index+1 < len(args) {
				return args[index+1]
			}

			return nil
		}
	}

	return nil
}

func isJudged(name string) bool {
	return slices.Contains(removers, name) || name == "mktemp" || name == "git" || name == "eval" || name == "trap" ||
		slices.Contains(shells, name) || isInterpreter(name) || slices.Contains(launchers, name)
}

func isInterpreter(name string) bool {
	return slices.Contains(interpreters, name) || strings.HasPrefix(name, "python")
}

// commandName is the base name of the word naming a command, lower-cased: a
// case-insensitive filesystem, as macOS keeps by default, runs `RM` as `rm`.
func commandName(word *syntax.Word) string {
	value, isLiteral := literal(word)
	if !isLiteral {
		return ""
	}

	return strings.ToLower(path.Base(value))
}

// literal returns the word's value when it carries no expansion: plain text,
// single quotes, and double quotes around plain text.
func literal(word *syntax.Word) (string, bool) {
	var value strings.Builder

	for _, part := range word.Parts {
		switch typed := part.(type) {
		case *syntax.Lit:
			value.WriteString(unescape(typed.Value))
		case *syntax.SglQuoted:
			if typed.Dollar {
				return "", false
			}

			value.WriteString(typed.Value)
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

// text is the words as the command spells them, space-separated.
func text(words ...*syntax.Word) string {
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
