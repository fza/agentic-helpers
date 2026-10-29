package shellguard

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/fza/agentic-helpers/source/hooks/internal/alternatives"
	"github.com/fza/agentic-helpers/source/hooks/internal/shellwalk"
)

const (
	leadMissing = "%s is not installed here"
	leadDialect = "%s here is %s %s: %s"
	reasonUse   = "%s; use: %s"
	insteadForm = "instead %s"
)

var durationWord = regexp.MustCompile(`^([0-9]+(?:\.[0-9]*)?|\.[0-9]+)([smhd]?)$`)

var durationUnits = map[string]float64{"": 1, "s": 1, "m": 60, "h": 3600, "d": 86400}

// alternativeFinding is one call the machine cannot run as written. replace
// is the call's words from its command on, rewritten; nil where no rewrite is
// exact, and fallback then says what to do instead.
type alternativeFinding struct {
	lead     string
	replace  func(args []*syntax.Word) []*syntax.Word
	fallback string
}

// judgeAlternatives refuses a call naming a command of the alternatives table
// the machine cannot run as written: a missing one where an alternative is
// installed, a dialect usage its flavour gets wrong. Every call the command
// makes is judged, those a launcher runs among them; code handed to another
// shell is not rewritten. The reason names the whole command rewritten, every
// exact rewrite applied, or what to do where the first finding has none.
func judgeAlternatives(ctx context.Context, command string, env Env, options Options) Verdict {
	if !options.Alternatives {
		return Verdict{Decision: Allow}
	}

	file, err := shellwalk.Parse(command)
	if err != nil {
		return Verdict{Decision: Allow}
	}

	machine := env.machine()

	var first *alternativeFinding

	syntax.Walk(file, func(node syntax.Node) bool {
		call, isCall := node.(*syntax.CallExpr)
		if !isCall || len(call.Args) == 0 {
			return true
		}

		starts := shellwalk.Certain(call.Args)
		found := make([]*alternativeFinding, len(starts))

		for index, start := range starts {
			found[index] = judgeCommand(ctx, machine, call.Args[start:])
			if found[index] != nil && first == nil {
				first = found[index]
			}
		}

		for index := len(starts) - 1; index >= 0; index-- {
			if found[index] != nil && found[index].replace != nil {
				start := starts[index]
				call.Args = append(slices.Clone(call.Args[:start]), found[index].replace(call.Args[start:])...)
			}
		}

		return true
	})

	if first == nil {
		return Verdict{Decision: Allow}
	}

	if first.replace == nil {
		return Verdict{Decision: Deny, Reason: first.lead + "; " + first.fallback}
	}

	var printed bytes.Buffer

	err = syntax.NewPrinter().Print(&printed, file)
	if err != nil {
		return Verdict{Decision: Deny, Reason: first.lead + "; " + first.fallback}
	}

	return Verdict{Decision: Deny, Reason: fmt.Sprintf(reasonUse, first.lead, strings.TrimRight(printed.String(), "\n"))}
}

func (env Env) machine() *alternatives.Machine {
	path := ""
	if env.Getenv != nil {
		path = env.Getenv("PATH")
	}

	return &alternatives.Machine{Path: path, CacheFile: alternatives.CacheFile(env.StateDir)}
}

// judgeCommand judges the command args open with, named as fixed text.
func judgeCommand(ctx context.Context, machine *alternatives.Machine, args []*syntax.Word) *alternativeFinding {
	value, isLiteral := shellwalk.Literal(args[0])
	if !isLiteral || shellwalk.Patterned(args[0]) {
		return nil
	}

	entry, known := alternatives.Lookup(shellwalk.CommandName(args[0]))
	if !known {
		return nil
	}

	switch entry.Kind {
	case alternatives.Missing:
		return judgeMissing(machine, entry, value, args)
	case alternatives.Dialect:
		return judgeDialect(ctx, machine, entry, value, args)
	}

	return nil
}

func judgeMissing(machine *alternatives.Machine, entry alternatives.Entry, value string, args []*syntax.Word) *alternativeFinding {
	if strings.Contains(value, "/") {
		return nil
	}

	_, present := machine.Find(entry.Name)
	if present {
		return nil
	}

	alternative, installed := machine.Installed(entry)
	if !installed {
		return nil
	}

	finding := &alternativeFinding{lead: fmt.Sprintf(leadMissing, entry.Name), fallback: "use: " + generic(alternative)}

	_, exact := fill(entry, alternative, args)
	if exact {
		finding.replace = func(current []*syntax.Word) []*syntax.Word {
			words, _ := fill(entry, alternative, current)

			return words
		}
	}

	return finding
}

func judgeDialect(
	ctx context.Context, machine *alternatives.Machine, entry alternatives.Entry, value string, args []*syntax.Word,
) *alternativeFinding {
	// A command named by a path is not judged: only a binary the PATH finds
	// by its bare name is read for its flavour, never a file the proposed
	// command picks.
	if strings.Contains(value, "/") {
		return nil
	}

	path, present := machine.Find(value)
	if !present {
		return nil
	}

	words := make([]alternatives.Arg, 0, len(args)-1)
	for _, arg := range args[1:] {
		text, literal := shellwalk.Literal(arg)
		words = append(words, alternatives.Arg{Text: text, Literal: literal && !shellwalk.Patterned(arg)})
	}

	var usage *alternatives.Usage

	for index := range entry.Usages {
		if entry.Usages[index].Matches(words) {
			usage = &entry.Usages[index]

			break
		}
	}

	if usage == nil {
		return nil
	}

	flavour := machine.Flavour(ctx, entry, path)
	if flavour != alternatives.FlavourBSD {
		return nil
	}

	finding := &alternativeFinding{
		lead:     fmt.Sprintf(leadDialect, entry.Name, flavour, entry.Name, usage.Says),
		fallback: fmt.Sprintf(insteadForm, usage.Instead),
	}

	alternative, installed := machine.Installed(entry)
	if installed {
		finding.replace = func(current []*syntax.Word) []*syntax.Word {
			return append([]*syntax.Word{fixedWord(alternative.Program)}, current[1:]...)
		}
	}

	return finding
}

// fill builds an alternative's words from a call of the entry's launcher:
// its options, then its duration, then the command it runs. It is not exact
// where the call has no command, where the template has no slot for the
// call's options, or where a duration set into fixed text is not fixed text.
func fill(entry alternatives.Entry, alternative alternatives.Alternative, args []*syntax.Word) ([]*syntax.Word, bool) {
	index := 1
	for index < len(args) {
		text, isLiteral := shellwalk.Literal(args[index])
		if !isLiteral || !strings.HasPrefix(text, "-") || text == "-" || text == "--" {
			break
		}

		if slices.Contains(entry.ValueOptions, text) {
			index++
		}

		index++
	}

	if index+1 >= len(args) {
		return nil, false
	}

	options, duration, command := args[1:index], args[index], args[index+1:]
	words := []*syntax.Word{fixedWord(alternative.Program)}

	for _, slot := range alternative.Template {
		switch {
		case slot == alternatives.SlotOptions:
			words = append(words, options...)
		case slot == alternatives.SlotDuration:
			words = append(words, duration)
		case slot == alternatives.SlotCommand:
			words = append(words, command...)
		case strings.Contains(slot, alternatives.SlotDuration):
			seconds, known := wholeSeconds(duration)
			if !known {
				return nil, false
			}

			words = append(words, fixedWord(strings.ReplaceAll(slot, alternatives.SlotDuration, seconds)))
		default:
			words = append(words, fixedWord(slot))
		}
	}

	if len(options) > 0 && !slices.Contains(alternative.Template, alternatives.SlotOptions) {
		return nil, false
	}

	return words, true
}

// wholeSeconds reads a duration as timeout spells it, a number with an
// optional s, m, h or d, rounded up to whole seconds: perl's alarm takes no
// fraction, and 0 means no limit to both.
func wholeSeconds(word *syntax.Word) (string, bool) {
	text, isLiteral := shellwalk.Literal(word)
	if !isLiteral {
		return "", false
	}

	match := durationWord.FindStringSubmatch(text)
	if match == nil {
		return "", false
	}

	number, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return "", false
	}

	return strconv.FormatFloat(math.Ceil(number*durationUnits[match[2]]), 'f', 0, 64), true
}

// generic is an alternative's form with its slots named, for a call no
// rewrite fits exactly.
func generic(alternative alternatives.Alternative) string {
	words := []string{alternative.Program}

	for _, slot := range alternative.Template {
		switch {
		case slot == alternatives.SlotOptions:
		case slot == alternatives.SlotDuration:
			words = append(words, "<duration>")
		case slot == alternatives.SlotCommand:
			words = append(words, "<command>")
		default:
			words = append(words, quoted(strings.ReplaceAll(slot, alternatives.SlotDuration, "<seconds>")))
		}
	}

	return strings.Join(words, " ")
}

func fixedWord(text string) *syntax.Word {
	return &syntax.Word{Parts: []syntax.WordPart{&syntax.Lit{Value: quoted(text)}}}
}

func quoted(text string) string {
	result, err := syntax.Quote(text, syntax.LangBash)
	if err != nil {
		return text
	}

	return result
}
