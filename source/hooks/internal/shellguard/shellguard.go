// Package shellguard decides whether a Bash call Claude Code is about to run
// may go ahead. It holds removals (`rm`, `rmdir`, `unlink`, `git rm`) and the
// sources of moves (`mv`, `git mv`) to the project and /tmp, bans `mktemp`,
// and asks before `find -delete`; each guard has its own plugin option. A
// shell script the command runs is read and judged as the command's own code.
//
// The command is parsed as bash, so `rm` inside a quoted argument of another
// command (a sed script, a grep pattern, a commit message) is text and passes.
// Code handed to a shell's -c or to eval is parsed again as shell. Inline code
// for any other interpreter cannot be parsed, so naming a removal or mktemp
// there is judged by the nested-code guard.
//
// A call naming a command the machine lacks, or a usage its flavour of a
// command gets wrong, is refused with the command that works (see
// internal/alternatives).
//
// After every tool call, Output withholds each line of output from outside
// the project that looks like a credential.
//
// Unlike every other piece it has no directory to wait for: it judges every
// Bash call of every project.
package shellguard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/fza/agentic-helpers/source/hooks/internal/pieces"
	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/secrets"
	"github.com/fza/agentic-helpers/source/hooks/internal/shellwalk"
)

const self = "agentic-shell-guard"

type Decision int

const (
	Allow Decision = iota
	Ask
	Deny
)

// Verdict / Rewrite names the count an ask the agent can fix by rewriting the
// command is kept under; an ask without one prompts at once.
type Verdict struct {
	Decision Decision
	Reason   string
	Rewrite  string
}

// Env / ProjectDir is $CLAUDE_PROJECT_DIR, the directory the session started
// in. The payload's cwd follows every cd, so it only anchors relative targets
// and never widens the project. Settings are the files the plugin's options
// are read from. StateDir holds, in shell-guard's slot file, each session's
// countdowns that ran out and its refused rewritable asks; Clock times the countdowns, read only by the
// PermissionRequest hook. The PATH read through Getenv says which commands
// are installed, and StateDir, the cache root, keeps each command's flavour. Files
// reads the scripts a command runs; left nil it reads none, and every script
// is asked about.
type Env struct {
	ProjectDir string
	Home       string
	Tmp        string
	Settings   []string
	Getenv     func(string) string
	StateDir   string
	Clock      Clock
	Files      shellwalk.Files
}

type Streams struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// Options is every guard's setting: the outside-project, find -delete and
// secrets guards ask, deny or are off; every other guard denies when on.
type Options struct {
	OutsideProject string
	FindDelete     string
	Secrets        string
	LoneRemoval    bool
	Expansion      bool
	NestedCode     bool
	Mktemp         bool
	Alternatives   bool
	// Scratch is whether the project carries the .tmp/ the mktemp refusal
	// points at.
	Scratch bool
	// ExtraRoots is every directory outside a project its removals and move
	// sources may still reach, by project.
	ExtraRoots []pluginconfig.ExtraRoot
}

func optionsFrom(read pluginconfig.Options) Options {
	extraRoots, _ := pluginconfig.ExtraRoots(read.ShellGuardExtraRoots)

	return Options{
		ExtraRoots:     extraRoots,
		OutsideProject: read.ShellGuardOutsideProject, FindDelete: read.ShellGuardFindDelete, Secrets: read.ShellGuardSecrets,
		LoneRemoval: read.ShellGuardLoneRemoval, Expansion: read.ShellGuardExpansion,
		NestedCode: read.ShellGuardNestedCode, Mktemp: read.ShellGuardMktemp, Alternatives: read.ShellGuardAlternatives,
	}
}

const (
	reasonMktemp        = "mktemp is banned; write scratch files under the project's .tmp/ instead"
	reasonMktempNoTmp   = "mktemp is banned; create the project's .tmp/ (gitignored) and write scratch files under it"
	reasonNotAlone      = "rm runs alone: no command before or after it, no pipe, redirection, subshell or wrapper; run it as its own Bash call with literal targets"
	reasonExpansion     = "refusing rm with a shell variable or command substitution"
	reasonUnparsable    = "cannot parse the command; refusing"
	reasonFindDelete    = "find -delete"
	reasonSecrets       = `the command holds what looks like a credential (%s, line %d); history, logs and files keep it; reference it instead: "$NAME" or a file`
	reasonSecretsCut    = "the hook was cut short before its credential scan finished; nothing says whether the command holds one"
	reasonNotFixed      = "cannot judge: the command name %s is not fixed text"
	reasonUnseen        = "arithmetic on %s reads a value shell-guard cannot see"
	reasonScriptUnread  = "cannot read the script %s (%s)"
	reasonInScript      = "in the script %s: %s"
	unreadUnparsed      = "it does not parse as bash"
	whyNotAlone         = "not alone"
	whyExpansion        = "expansion"
	whyNestedCode       = "nested code"
	labelCannotPlace    = "cannot place: "
	labelOutsideProject = "outside project: "
	labelNoProject      = "no project: "
)

var removers = []string{"rm", "rmdir", "unlink"}

// toolMonitor is Claude Code's Monitor tool, which runs its command in the
// Bash tool's shell; with a websocket in place of a command it runs none.
const toolMonitor = "Monitor"

// namesRemoval matches the removal calls of the shell and of the interpreters'
// own libraries, as a word.
var (
	namesRemoval = regexp.MustCompile(`(?i)(^|[^A-Za-z0-9_-])(rm|rmdir|unlink|rmtree|rmSync|rmdirSync|unlinkSync|` +
		`removeSync|remove_dir_all|remove_dir|remove_file|remove_tree|remove_entry|removedirs|deleteSync|rm_rf|rm_r|rm_f)` +
		`([^A-Za-z0-9_-]|$)`)
	namesMktemp = regexp.MustCompile(`(^|[^A-Za-z0-9_-])mktemp([^A-Za-z0-9_-]|$)`)
)

// payload / PermissionMode is the session's permission mode, which decides
// how an ask is put: `dontAsk` turns it into a deny with the guard's reason,
// since Claude Code would deny the prompt without one.
type payload struct {
	Cwd            string `json:"cwd"`
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	PermissionMode string `json:"permission_mode"`
	ToolName       string `json:"tool_name"`
	ToolInput      struct {
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

	read, err := pluginconfig.ReadWith(env.Settings, env.Getenv)
	if err != nil {
		read = pluginconfig.Defaults()
	}

	if !pieces.ShellGuard.On(read) {
		return 0
	}

	var call payload

	err = json.NewDecoder(streams.In).Decode(&call)
	if err == nil && call.ToolInput.Command == nil && call.ToolName == toolMonitor {
		return 0
	}

	if err != nil || call.ToolInput.Command == nil {
		_, _ = fmt.Fprintln(streams.Err, self+": could not read the command; refusing")

		return 2
	}

	verdict := judgeCall(ctx, env, read, call)
	if verdict.Decision == Allow {
		return 0
	}

	if verdict.Decision == Ask && verdict.Rewrite != "" {
		verdict = refuseFirst(ctx, env, call.SessionID, verdict)
	}

	var reply answer

	reply.HookSpecificOutput.HookEventName = "PreToolUse"
	reply.HookSpecificOutput.PermissionDecision = "ask"
	if verdict.Decision == Deny || call.PermissionMode == modeDontAsk {
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

func judgeCall(ctx context.Context, env Env, read pluginconfig.Options, call payload) Verdict {
	options := optionsFrom(read)
	options.Mktemp = options.Mktemp && pieces.MktempBan.Holds(read)

	info, err := os.Stat(filepath.Join(env.ProjectDir, ".tmp"))
	options.Scratch = env.ProjectDir != "" && err == nil && info.IsDir()

	return Judge(ctx, *call.ToolInput.Command, call.Cwd, env, options)
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
	file, err := shellwalk.Parse(command)
	if err != nil {
		return Verdict{Decision: Deny, Reason: reasonUnparsable}
	}

	var found verdicts

	found.add(judgeCode(ctx, file, shellwalk.Scripts{Files: env.Files, Home: env.Home, Dir: workingDir}, env, options))
	found.add(judgeAlternatives(ctx, command, env, options))
	found.add(holdsCredential(ctx, command, options))

	return found.decided
}

// judgeCode is the verdict of every guard the walk drives on the command.
func judgeCode(ctx context.Context, file *syntax.File, scripts shellwalk.Scripts, env Env, options Options) Verdict {
	call, targetsFrom := loneRemoval(file)
	if call != nil {
		return judgeRemoval(ctx, call.Args[targetsFrom:], scripts.Dir, env, options)
	}

	inspection := inspector{ctx: ctx, env: env, options: options, scripts: scripts}
	inspection.walk(file)

	return inspection.found.decided
}

// holdsCredential is the secrets guard's verdict on the command's whole text:
// a credential typed into a command stays in the shell's history, the
// transcript and any log it reaches. A scan that could not finish counts as a
// finding, but once the context has ended it names no credential: the hook
// itself is being cut short, and the verdict says only that the scan was.
func holdsCredential(ctx context.Context, command string, options Options) Verdict {
	findings := secrets.Scan(ctx, command, "")
	if len(findings) == 0 {
		return Verdict{Decision: Allow}
	}

	if ctx.Err() != nil {
		return mode(options.Secrets, reasonSecretsCut)
	}

	rules, line := secrets.Describe(findings)

	return rewritable(options.Secrets, fmt.Sprintf(reasonSecrets, rules, line), rewriteCredential)
}

// loneRemoval returns the call when the whole command is one removal and
// nothing else: one statement, no redirection, no assignment, not negated, not
// in the background. A time limit before it (timeout, or an alternative
// standing in for it) changes neither where it runs nor its targets, so the
// removal behind one still stands alone.
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

	start := 0
	for {
		next := shellwalk.TimeLimitRuns(call.Args[start:])
		if next < 0 || start+next >= len(call.Args) {
			break
		}

		start += next
	}

	// Every word the time limits take must be fixed text: an option value
	// such as "$(rm -rf ~/x)" stays one word, so the walk skips it as a value,
	// and only the inspector's walk reaches the command it substitutes.
	for _, word := range call.Args[:start] {
		_, isLiteral := shellwalk.Literal(word)
		if !isLiteral || shellwalk.Patterned(word) {
			return nil, 0
		}
	}

	name := shellwalk.CommandName(call.Args[start])
	if slices.Contains(removers, name) {
		return call, start + 1
	}

	if name == "git" && len(call.Args) > start+1 {
		subcommand, isLiteral := shellwalk.Literal(call.Args[start+1])
		if isLiteral && subcommand == "rm" {
			return call, start + 2
		}
	}

	return nil, 0
}

// inspector judges what the walk finds. Inside code another program runs as
// shell (inside), a removal or mktemp is only noted: the nested-code guard
// judges it. scripts is what the walk reads the scripts it runs through; ctx
// is held since the walk's visitor methods take none.
type inspector struct {
	ctx     context.Context
	env     Env
	options Options
	scripts shellwalk.Scripts
	found   verdicts
	inside  bool
	// inScript is a script's own text, whose removals are judged by their
	// targets alone.
	inScript bool
	removes  bool
	mktemps  bool
}

func (inspection *inspector) walk(node syntax.Node) {
	shellwalk.WalkWith(node, inspection, inspection.scripts)
}

// removal is a removal that is not the whole command, its targets from
// args[from:].
func (inspection *inspector) removal(args []*syntax.Word, from int, dir string) {
	if inspection.inside {
		inspection.removes = true

		return
	}

	if inspection.inScript {
		inspection.found.add(judgeRemoval(inspection.ctx, args[min(from, len(args)):], dir, inspection.env, inspection.options))

		return
	}

	if inspection.options.LoneRemoval {
		inspection.found.add(Verdict{Decision: Deny, Reason: reasonNotAlone})

		return
	}

	inspection.found.add(cannotPlace(inspection.options, shellwalk.Text(args[min(from, len(args)):]...), whyNotAlone))
}

// Command judges every call the walk offers, a launcher's every word among
// them: a guard errs on the side of reading a word as the command it runs.
// A name the shell builds at run time, by an expansion, a glob or a brace,
// could be any command, a removal among them, so it takes the outside-project
// verdict when the call passes it an argument, or when the name may split
// into words carrying their own (`CMD='rm -rf /'; $CMD`). A name wholly in
// double quotes passing nothing runs: it stays one word.
// Only the word a launcher certainly runs counts: any other may be an
// option's value.
func (inspection *inspector) Command(call shellwalk.Call) {
	args := call.Args
	name := shellwalk.CommandName(args[0])
	_, literal := shellwalk.Literal(args[0])
	fixed := literal && !shellwalk.Patterned(args[0])

	switch {
	case !fixed && call.Certain && (len(args) > 1 || !shellwalk.OneKnownWord(args[0])):
		reason := fmt.Sprintf(reasonNotFixed, shellwalk.Text(args[0]))
		inspection.found.add(rewritable(inspection.options.OutsideProject, reason, rewriteNotFixed))
	case slices.Contains(removers, name):
		inspection.removal(args, 1, call.Dir)
	case name == "mktemp":
		inspection.mktemp()
	case name == "mv":
		inspection.moved(moveSources(args[1:], true), call.Dir)
	case name == "git":
		dir := call.Dir

		for index, arg := range args[1:] {
			value, isLiteral := shellwalk.Literal(arg)

			switch {
			case !isLiteral:
			case value == "rm":
				inspection.removal(args, index+2, dir)
			case value == "mv":
				inspection.moved(moveSources(args[index+2:], false), dir)
			case slices.Contains(gitPlaces, value) || strings.HasPrefix(value, "--git-dir=") ||
				strings.HasPrefix(value, "--work-tree="):
				dir = ""
			}
		}
	case name == "find":
		for _, arg := range args[1:] {
			value, _ := shellwalk.Literal(arg)
			if value == "-delete" {
				inspection.found.add(mode(inspection.options.FindDelete, reasonFindDelete))
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

// Inline judges code for an interpreter the guard cannot parse: naming a
// removal or mktemp there denies while the nested-code guard is on.
func (inspection *inspector) Inline(name string, args []*syntax.Word) {
	for _, arg := range args {
		code := shellwalk.Text(arg)
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

// Code judges code another program runs as shell. Code the guard cannot read
// as a literal, or cannot parse, denies: it could be anything. With the
// nested-code guard off, a removal in it, or code it cannot read, takes the
// outside-project verdict, and the rest passes.
func (inspection *inspector) Code(runner string, words []*syntax.Word, dir string) {
	parts := make([]string, 0, len(words))
	for _, word := range words {
		value, isLiteral := shellwalk.Literal(word)
		if !isLiteral {
			inspection.unreadable(runner, fmt.Sprintf("cannot read the code handed to %s; refusing", runner))

			return
		}

		parts = append(parts, value)
	}

	code := strings.Join(parts, " ")

	file, err := shellwalk.Parse(code)
	if err != nil {
		inspection.unreadable(runner, fmt.Sprintf("cannot parse the code handed to %s; refusing", runner))

		return
	}

	inner := inspector{
		ctx: inspection.ctx, env: inspection.env, options: inspection.options, scripts: inspection.scripts.At(dir), inside: true,
	}
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

func (inspection *inspector) Piped(name string) {
	inspection.unreadable(name+" stdin", fmt.Sprintf("cannot read the code piped to %s; refusing", name))
}

// Unseen asks about arithmetic on a variable whose value only the run shows:
// its text may hold a subscript that runs a command, as unreadable code may,
// but most such values are numbers, so it asks rather than denies.
func (inspection *inspector) Unseen(variable string) {
	if inspection.options.NestedCode {
		inspection.found.add(Verdict{Decision: Ask, Reason: fmt.Sprintf(reasonUnseen, variable)})

		return
	}

	inspection.found.add(cannotPlace(inspection.options, "arithmetic on "+variable, whyNestedCode))
}

// Script judges a script the command runs by the walk's guards, in the
// directory it runs in: a removal there by its targets alone, the lone-removal
// rule set aside, since a script holds commands beside its removals by
// nature. Nothing found there denies: a deny becomes an ask, as the person
// can read the script the agent cannot change by rewriting the command, and
// that ask prompts at once. Another interpreter's script is judged as its
// inline code.
func (inspection *inspector) Script(name string, interpreter string, text string, within shellwalk.Scripts) {
	inner := inspector{ctx: inspection.ctx, env: inspection.env, options: inspection.options, scripts: within, inScript: true}

	if interpreter != "" {
		inner.Inline(interpreter, []*syntax.Word{{Parts: []syntax.WordPart{&syntax.Lit{Value: text}}}})
	} else {
		file, err := shellwalk.Parse(text)
		if err != nil {
			inspection.Unread(name, unreadUnparsed)

			return
		}

		inner.walk(file)
	}

	verdict := inner.found.decided
	if verdict.Decision == Allow {
		return
	}

	inspection.found.add(Verdict{Decision: Ask, Reason: fmt.Sprintf(reasonInScript, name, verdict.Reason)})
}

// Unread asks about a script the command runs that the walk cannot read: most
// such scripts are harmless, as an activation script named by a variable is,
// so it asks rather than denies.
func (inspection *inspector) Unread(name string, why string) {
	if inspection.options.NestedCode {
		inspection.found.add(Verdict{Decision: Ask, Reason: fmt.Sprintf(reasonScriptUnread, name, why)})

		return
	}

	inspection.found.add(cannotPlace(inspection.options, "script "+name, whyNestedCode))
}

// moved judges each source of a move as a removal operand: a move takes the
// file away from where it was. Its target is never judged.
func (inspection *inspector) moved(sources []*syntax.Word, dir string) {
	if len(sources) == 0 {
		return
	}

	operands := append([]*syntax.Word{{Parts: []syntax.WordPart{&syntax.Lit{Value: "--"}}}}, sources...)
	inspection.found.add(judgeRemoval(inspection.ctx, operands, dir, inspection.env, inspection.options))
}

func (inspection *inspector) unreadable(runner string, reason string) {
	if inspection.options.NestedCode {
		inspection.found.add(Verdict{Decision: Deny, Reason: reason})

		return
	}

	inspection.found.add(cannotPlace(inspection.options, "code handed to "+runner, whyNestedCode))
}
