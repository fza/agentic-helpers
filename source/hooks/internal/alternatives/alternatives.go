// Package alternatives is shell-guard's built-in table of commands agents
// reach for that fail or misbehave on the machine: missing ones, such as
// `timeout` on macOS, and ones present in another platform's flavour, such as
// BSD `sed`, each with what to run instead.
//
// The table lives in code rather than in the agent-writable `config.yaml`: a
// wrong launcher declaration there, such as `rm` declared a launcher, would
// make shell-guard judge the wrong word. An entry exists only for a usage
// verified to fail or mislead; `readlink -f` and `grep -P` work on macOS and
// have none.
//
// The package reads words as text: shellwalk walks every alternative that
// launches a command from the same table, and shell-guard builds each rewrite
// from the parsed command.
package alternatives

import (
	"regexp"
	"slices"
	"strings"
)

type Kind int

const (
	// Missing is a command absent from the PATH: the first installed
	// alternative stands in for it.
	Missing Kind = iota + 1
	// Dialect is a command present in a flavour other than the one an agent
	// writes for: a usage that flavour gets wrong is refused.
	Dialect
)

// SlotOptions / SlotDuration / SlotCommand are the named slots of an
// alternative's template, filled from the parsed call: the launcher's
// options, its duration, and the command it runs with every word after it. A
// slot standing as a whole word takes the call's own word; SlotDuration inside
// fixed text takes the duration in whole seconds.
const (
	SlotOptions  = "{opts...}"
	SlotDuration = "{dur}"
	SlotCommand  = "{cmd...}"
)

type Alternative struct {
	Program  string
	Template []string
}

// Entry / Launcher holds for a command whose alternatives launch the command
// after them: Template's SlotCommand marks where it starts, and the walk reads
// every alternative of the entry as a launcher. ValueOptions are the
// launcher's options taking the next word as their value. TimeLimit is true
// for a launcher that only bounds how long its command runs, never where or
// with what, so shell-guard judges a removal behind it as standing alone.
// Markers are the strings a dialect's binary carries by flavour: GNU sed
// names "GNU sed" and GNU date "GNU coreutils"; macOS's carry their Apple
// project tag (`@(#)PROGRAM:sed  PROJECT:text_cmds-...`) and BSD's usage
// line.
type Entry struct {
	Name         string
	Kind         Kind
	Alternatives []Alternative
	Launcher     bool
	ValueOptions []string
	TimeLimit    bool
	Usages       []Usage
	Markers      map[string][]string
}

// Usage is one usage a dialect gets wrong: Says what goes wrong, Instead the
// flavour's own form, Matches whether a call's arguments make it.
type Usage struct {
	Says    string
	Instead string
	Matches func(args []Arg) bool
}

// Arg is a word of a call: its text, where Literal says the shell hands it
// on as fixed text, and OneWord whether the shell certainly hands it on as
// exactly one word, never splitting it and never dropping it.
type Arg struct {
	Text    string
	Literal bool
	OneWord bool
}

const (
	FlavourGNU = "GNU"
	FlavourBSD = "BSD"
)

var timeoutTemplate = []string{SlotOptions, SlotDuration, SlotCommand}

var Table = []Entry{
	{
		Name: "timeout", Kind: Missing, Launcher: true, TimeLimit: true,
		ValueOptions: []string{"-s", "--signal", "-k", "--kill-after"},
		Alternatives: []Alternative{
			{Program: "timeout", Template: timeoutTemplate},
			{Program: "gtimeout", Template: timeoutTemplate},
			{Program: "uu-timeout", Template: timeoutTemplate},
			{Program: "perl", Template: []string{"-e", "alarm " + SlotDuration + "; exec @ARGV", SlotCommand}},
		},
	},
	{
		Name: "sed", Kind: Dialect, Alternatives: []Alternative{{Program: "gsed"}}, Usages: sedUsages,
		Markers: map[string][]string{
			FlavourGNU: {"GNU sed"},
			FlavourBSD: {"PROGRAM:sed  PROJECT:text_cmds", "[-i extension]"},
		},
	},
	{
		Name: "date", Kind: Dialect, Alternatives: []Alternative{{Program: "gdate"}}, Usages: dateUsages,
		Markers: map[string][]string{
			FlavourGNU: {"GNU coreutils"},
			FlavourBSD: {"PROGRAM:date  PROJECT:shell_cmds", "usage: date [-jnRu]"},
		},
	},
}

func Lookup(name string) (Entry, bool) {
	for _, entry := range Table {
		if entry.Name == name {
			return entry, true
		}
	}

	return Entry{}, false
}

// Launcher is one alternative that launches a command. Prefix is the fixed
// words its template opens with before SlotCommand, nil where it opens with
// SlotOptions and so reads as a launcher by name alone.
type Launcher struct {
	Program      string
	Prefix       []string
	ValueOptions []string
	TimeLimit    bool
}

func Launchers() []Launcher {
	var launchers []Launcher

	for _, entry := range Table {
		if !entry.Launcher {
			continue
		}

		for _, alternative := range entry.Alternatives {
			launcher := Launcher{Program: alternative.Program, ValueOptions: entry.ValueOptions, TimeLimit: entry.TimeLimit}

			at := slices.Index(alternative.Template, SlotCommand)
			if at > 0 && alternative.Template[0] != SlotOptions {
				launcher.Prefix = alternative.Template[:at]
			}

			launchers = append(launchers, launcher)
		}
	}

	return launchers
}

// perlShellCharacters are the characters that make perl's `exec` of a single
// word hand it to /bin/sh rather than run it as a program, whitespace among
// them: one such word is shell code, not a command.
const perlShellCharacters = "$&*(){}[]'\";\\|?<>~`\n \t\r\f\v"

// PrefixLaunch is the index of the word a call of a prefixed launcher runs,
// its program at args[0]: the call must spell the prefix exactly, a duration
// as whole seconds. The word after the prefix must not start with `-`, which
// perl would read as one more option, code with `-e` among them, and must be
// fixed text holding nothing perl's `exec` hands to the shell, whatever
// follows it: a word after it that expands to nothing (`$empty`, `"$@"`, a
// glob matching nothing) leaves it alone at run time, and `exec` of one word
// runs it through /bin/sh. Every word after it must stay exactly one word.
// It is -1 for any other call, which stays inline code.
func PrefixLaunch(program string, args []Arg) int {
	for _, launcher := range Launchers() {
		if launcher.Program != program || launcher.Prefix == nil || len(args) <= len(launcher.Prefix)+1 {
			continue
		}

		matched := true

		for index, fixed := range launcher.Prefix {
			arg := args[index+1]
			if !arg.Literal || !prefixWord(fixed).MatchString(arg.Text) {
				matched = false

				break
			}
		}

		at := len(launcher.Prefix) + 1
		if matched && runsAsProgram(args[at:]) {
			return at
		}
	}

	return -1
}

func runsAsProgram(command []Arg) bool {
	program := command[0]
	if !program.Literal || program.Text == "" || strings.HasPrefix(program.Text, "-") ||
		strings.ContainsAny(program.Text, perlShellCharacters) {
		return false
	}

	for _, arg := range command[1:] {
		if !arg.OneWord {
			return false
		}
	}

	return true
}

func prefixWord(fixed string) *regexp.Regexp {
	pattern := strings.ReplaceAll(regexp.QuoteMeta(fixed), regexp.QuoteMeta(SlotDuration), `[0-9]+`)

	return regexp.MustCompile("^" + pattern + "$")
}

// Shown is the alternative as the SessionStart line names it: its program
// with the fixed words of its template, SlotDuration as N.
func (alternative Alternative) Shown() string {
	words := []string{alternative.Program}

	for _, word := range alternative.Template {
		if word == SlotOptions || word == SlotDuration || word == SlotCommand {
			continue
		}

		shown := strings.ReplaceAll(word, SlotDuration, "N")
		if strings.ContainsAny(shown, " ;@'\"$") {
			shown = "'" + shown + "'"
		}

		words = append(words, shown)
	}

	return strings.Join(words, " ")
}
