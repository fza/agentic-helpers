package shellwalk

import (
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

const (
	bashShell = "bash"
	shShell   = "sh"
	dashShell = "dash"
	zshShell  = "zsh"
)

// Each table below holds what the shell did when run, not what a manual says of
// it: the two disagree often enough, and the shells disagree with each other,
// that one rule for all of them leaves code unjudged somewhere. Run on macOS's
// bash, sh (bash again), zsh, dash and ksh93, on mksh 59c and on fish 4.9.
var (
	// longValues are the long options taking the next word as their value. Any
	// other takes none: zsh sets every option it has by name (`zsh --exec x.sh`),
	// so taking a word by default leaves the script unread. dash, ksh and mksh
	// refuse every long option and run nothing. A list of flags was rejected as
	// the list that misses one.
	longValues = map[string][]string{
		bashShell: {"--rcfile", "--init-file"},
		shShell:   {"--rcfile", "--init-file"},
		zshShell:  {"--emulate"},
	}

	// valueLetters are the short options taking a value, with + as with -. -C is
	// noclobber here and init code only on fish, and zsh's -O is a flag.
	valueLetters = map[string]string{
		bashShell: "oO",
		shShell:   "oO",
		dashShell: "o",
		zshShell:  "o",
		"ksh":     "oR",
		"mksh":    "oT",
	}

	// attachesValues are the shells reading any letters after a value letter as
	// its value (`zsh -ono_rcs x.sh`), so those letters are no options. bash and
	// dash go on to the next word whatever follows the letter.
	attachesValues = []string{zshShell, "ksh", "mksh"}

	// plusEnds are the shells where a lone + ends the options as - does. The rest
	// read it as no option at all and go on: `bash + -c code` runs the code.
	plusEnds = []string{zshShell, "ksh", "mksh"}

	// plusCode are the shells where +c hands code as -c does, plusInput where +s
	// reads standard input as -s does. To the others these are other options.
	plusCode  = []string{bashShell, shShell, zshShell, dashShell}
	plusInput = []string{bashShell, shShell}

	// inputBesideCode are the shells that run standard input after the code of
	// `-c -s`. sh counts for where it is dash.
	inputBesideCode = []string{dashShell, shShell}
)

// fishLongValues are the long options of fish taking a value, each with its
// short letter. fish takes any unique prefix of a long name, so a name is
// matched by prefix over these alone, which also covers --debug and --profile.
// A prefix also fitting a flag is one fish refuses, running nothing, so what it
// is taken for here changes no verdict.
var fishLongValues = []struct {
	name   string
	letter rune
}{
	{"command", 'c'}, {"init-command", 'C'}, {"debug-output", 'o'}, {"debug-stack-frames", 'D'},
	{"features", 'f'}, {"profile-startup", 'p'},
}

// fishValueLetters are fish's short options taking a value, which is whatever
// follows the letter in its word, else the next word.
const fishValueLetters = "cCdDfop"

// shellArguments is what a shell makes of its arguments. operand is the index
// of the first word past the options, or -1; readsInput says -s made standard
// input the script; code is the words it runs as code in place of a script;
// initCode the words fish runs before either.
type shellArguments struct {
	operand    int
	readsInput bool
	code       []*syntax.Word
	initCode   []*syntax.Word
}

// readShellArguments reads a shell's arguments the way it does. Options stop at
// the first operand: a -c past it is the script's own argument. A word built at
// run time has no literal value, so it is that operand.
func readShellArguments(shell string, args []*syntax.Word) shellArguments {
	if shell == fishShell {
		return readFishArguments(args)
	}

	parsed := shellArguments{operand: -1}
	carriesCode := false

	for index := 0; index < len(args); index++ {
		value, _ := Literal(args[index])

		switch {
		case value == "--" || value == "-" || endsAsPlus(shell, value):
			parsed.operand = wordAt(args, index+1)
		case longOption(value):
			if slices.Contains(longValues[shell], value) {
				index++
			}

			continue
		case strings.HasPrefix(value, "-") || strings.HasPrefix(value, "+"):
			group := readOptionGroup(shell, value)
			parsed.readsInput = parsed.readsInput || group.readsInput
			carriesCode = carriesCode || group.code

			if group.takesNext {
				index++
			}

			if !group.ends {
				continue
			}

			parsed.operand = wordAt(args, index+1)
		default:
			parsed.operand = index
		}

		break
	}

	if carriesCode && parsed.operand >= 0 {
		parsed.code = []*syntax.Word{args[parsed.operand]}
	}

	return parsed
}

func wordAt(args []*syntax.Word, index int) int {
	if index < len(args) {
		return index
	}

	return -1
}

func endsAsPlus(shell string, value string) bool {
	return value == "+" && slices.Contains(plusEnds, shell) || value == "+-" && shell == zshShell
}

// longOption is whether a shell's word is a long option: `--name`, or zsh's
// `+-name` clearing it.
func longOption(value string) bool {
	return strings.HasPrefix(value, "--") || strings.HasPrefix(value, "+-")
}

type optionGroup struct {
	readsInput bool
	code       bool
	takesNext  bool
	ends       bool
}

// readOptionGroup reads the letters of one word of options in order. zsh's b
// ends the options, the letters beside it still applying.
func readOptionGroup(shell string, value string) optionGroup {
	var group optionGroup

	minus := strings.HasPrefix(value, "-")
	letters := value[1:]

	for at, letter := range letters {
		switch {
		case strings.ContainsRune(valueLetters[shell], letter):
			if slices.Contains(attachesValues, shell) && at+1 < len(letters) {
				return group
			}

			group.takesNext = true
		case letter == 's':
			group.readsInput = minus || slices.Contains(plusInput, shell)
		case letter == 'c':
			group.code = minus || slices.Contains(plusCode, shell)
		case letter == 'b' && shell == zshShell:
			group.ends = true
		}
	}

	return group
}

// readFishArguments reads fish's arguments: every option's value is the next
// word or its own word's rest, every -c and -C counts, -- ends the options, and
// - is a file of that name like any other first operand.
func readFishArguments(args []*syntax.Word) shellArguments {
	parsed := shellArguments{operand: -1}

	for index := 0; index < len(args); index++ {
		value, _ := Literal(args[index])

		switch {
		case value == "--":
			parsed.operand = wordAt(args, index+1)
		case strings.HasPrefix(value, "--"):
			index = parsed.readFishLong(args, index, value[2:])

			continue
		case strings.HasPrefix(value, "-") && value != "-":
			index = parsed.readFishGroup(args, index, value[1:])

			continue
		default:
			parsed.operand = index
		}

		break
	}

	return parsed
}

func (parsed *shellArguments) readFishLong(args []*syntax.Word, index int, option string) int {
	name, attached, hasValue := strings.Cut(option, "=")

	for _, known := range fishLongValues {
		if !strings.HasPrefix(known.name, name) {
			continue
		}

		if hasValue {
			parsed.keepFishValue(known.letter, attachedWord(attached))

			return index
		}

		return parsed.takeFishValue(args, index, known.letter)
	}

	return index
}

func (parsed *shellArguments) readFishGroup(args []*syntax.Word, index int, letters string) int {
	for at, letter := range letters {
		if !strings.ContainsRune(fishValueLetters, letter) {
			continue
		}

		if at+1 < len(letters) {
			parsed.keepFishValue(letter, attachedWord(letters[at+1:]))

			return index
		}

		return parsed.takeFishValue(args, index, letter)
	}

	return index
}

// takeFishValue keeps the word after index as an option's value and is the
// index of that word.
func (parsed *shellArguments) takeFishValue(args []*syntax.Word, index int, letter rune) int {
	if index+1 < len(args) {
		parsed.keepFishValue(letter, args[index+1])
	}

	return index + 1
}

func (parsed *shellArguments) keepFishValue(letter rune, word *syntax.Word) {
	switch letter {
	case 'c':
		parsed.code = append(parsed.code, word)
	case 'C':
		parsed.initCode = append(parsed.initCode, word)
	}
}

// attachedWord is a value read out of a longer word, as a word of its own. Its
// text is fixed: a word holding an expansion is no literal, so it never gets here.
func attachedWord(value string) *syntax.Word {
	return &syntax.Word{Parts: []syntax.WordPart{&syntax.SglQuoted{Value: value}}}
}

// runCode is every word of code the shell runs, in the order it runs them.
func (parsed shellArguments) runCode() []*syntax.Word {
	return slices.Concat(parsed.initCode, parsed.code)
}

// scriptOperand is the index of the script a shell's arguments name, or -1
// where it names none: -s reads standard input, -c runs code, and the operand
// may name the input itself.
func (parsed shellArguments) scriptOperand(args []*syntax.Word) int {
	if parsed.operand < 0 || parsed.readsInput || len(parsed.code) > 0 || standardOperand(args[parsed.operand]) {
		return -1
	}

	return parsed.operand
}

// replacesInput is whether the shell runs code it is handed and so none from
// standard input. fish's -C runs before the input is read.
func (parsed shellArguments) replacesInput(shell string) bool {
	return len(parsed.code) > 0 && !(parsed.readsInput && slices.Contains(inputBesideCode, shell))
}
