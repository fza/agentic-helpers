package shellguard

import (
	"slices"
	"strings"
)

type programKind int

const (
	operandsAreFiles programKind = iota
	operandsAreText
	firstOperandIsPattern
	firstOperandIsSedScript
)

// readerOptions is the closed list of options a reader may carry and still
// count as showing the project, as getopt reads them. Short options are
// letters: flags take no value; a value letter takes the rest of its group, or
// the next word, as a text value, a file, or the program. Long options are
// matched exactly, never as the abbreviations getopt_long accepts: optional
// ones take a value only through `=`; pairs take the next two words. Any
// other option makes the call count as reading outside the project.
type readerOptions struct {
	flags        string
	values       string
	files        string
	scripts      string
	programFiles string

	longFlags        []string
	longOptional     []string
	longValues       []string
	longFiles        []string
	longScripts      []string
	longProgramFiles []string
	longPairs        []string
	longFilePairs    []string

	program programKind

	// stdinDash is whether a plain operand `-` reads standard input; a file an
	// option takes never does.
	stdinDash bool
}

var grepOptions = readerOptions{
	flags: "EFGPiywxvcLlobnqsHhaIrUuzZTJ", values: "mABCdD", files: "f", scripts: "e", programFiles: "f",
	longFlags: []string{
		"extended-regexp", "fixed-strings", "basic-regexp", "perl-regexp", "ignore-case", "no-ignore-case",
		"word-regexp", "line-regexp", "invert-match", "count", "files-with-matches", "files-without-match",
		"only-matching", "byte-offset", "line-number", "quiet", "silent", "no-messages", "with-filename",
		"no-filename", "text", "recursive", "null", "null-data", "initial-tab", "line-buffered",
	},
	longOptional: []string{"color", "colour"},
	longValues: []string{
		"max-count", "after-context", "before-context", "context", "devices", "directories", "label",
		"binary-files", "include", "exclude", "exclude-dir",
	},
	longFiles: []string{"file", "exclude-from"}, longScripts: []string{"regexp"}, longProgramFiles: []string{"file"},
	program: firstOperandIsPattern, stdinDash: true,
}

// readerTable is every reader and its options. awk and jq are not among them:
// awk's program can read any file (getline, ARGV) and run commands, and jq's
// filter language reads the environment and loads modules in spellings no
// pattern list can close. Left out on
// purpose: grep -R, -S and -p (they follow symlinks out of a named
// directory), sed -i and -f, sort and wc --files0-from and sort
// --compress-program.
var readerTable = map[string]readerOptions{
	"cat": {
		flags: "AbeEnstTuvl", stdinDash: true,
		longFlags: []string{
			"show-all", "number-nonblank", "show-ends", "number", "squeeze-blank", "show-tabs", "show-nonprinting",
		},
	},
	"head": {
		flags: "qvz0123456789", values: "nc", stdinDash: true,
		longFlags: []string{"quiet", "silent", "verbose", "zero-terminated"}, longValues: []string{"lines", "bytes"},
	},
	"tail": {
		flags: "fFqrvz0123456789", values: "ncsb", stdinDash: true,
		longFlags:    []string{"quiet", "silent", "verbose", "zero-terminated", "retry"},
		longOptional: []string{"follow"},
		longValues:   []string{"lines", "bytes", "sleep-interval", "max-unchanged-stats", "pid"},
	},
	"grep":  grepOptions,
	"egrep": grepOptions,
	"fgrep": grepOptions,
	"sed": {
		flags: "nErsuz", scripts: "e", stdinDash: true,
		longFlags: []string{
			"quiet", "silent", "regexp-extended", "separate", "unbuffered", "null-data", "posix", "debug", "sandbox",
		},
		longScripts: []string{"expression"},
		program:     firstOperandIsSedScript,
	},
	"wc": {
		flags: "clmwL", stdinDash: true, longFlags: []string{"bytes", "chars", "lines", "words", "max-line-length"},
	},
	"sort": {
		flags: "bdfgiMhnRrcCmsuVz", values: "ktS", files: "oT", stdinDash: true,
		longFlags: []string{
			"ignore-leading-blanks", "dictionary-order", "ignore-case", "general-numeric-sort", "ignore-nonprinting",
			"month-sort", "human-numeric-sort", "numeric-sort", "random-sort", "reverse", "version-sort", "merge",
			"stable", "unique", "zero-terminated",
		},
		longOptional: []string{"check"},
		longValues:   []string{"key", "field-separator", "buffer-size", "parallel", "batch-size", "sort"},
		longFiles:    []string{"output", "temporary-directory", "random-source"},
	},
	"uniq": {
		flags: "cdDiuz", values: "fsw", stdinDash: true,
		longFlags:  []string{"count", "repeated", "ignore-case", "unique", "zero-terminated"},
		longValues: []string{"skip-fields", "skip-chars", "check-chars"},
	},
	"cut": {
		flags: "nsz", values: "bcdf", stdinDash: true,
		longFlags:  []string{"complement", "only-delimited", "zero-terminated"},
		longValues: []string{"bytes", "characters", "delimiter", "fields", "output-delimiter"},
	},
	"tr": {
		flags: "cdsCtu", longFlags: []string{"complement", "delete", "squeeze-repeats", "truncate-set1"},
		program: operandsAreText,
	},
	"nl": {
		flags: "p", values: "bdfhilnsvw", stdinDash: true,
		longFlags: []string{"no-renumber"},
		longValues: []string{
			"body-numbering", "section-delimiter", "footer-numbering", "header-numbering", "line-increment",
			"join-blank-lines", "number-format", "number-separator", "starting-line-number", "number-width",
		},
	},
}

type readerArgs struct {
	files        []string
	scripts      []string
	operands     []string
	programGiven bool
}

// readerFiles is every file a reader's arguments name, the operands its
// program leaves and the files its options take, or false where an option is
// off its list or its program may read another file or run something.
func readerFiles(options readerOptions, args []string) ([]string, bool) {
	parsed, known := parseReader(options, args)
	if !known {
		return nil, false
	}

	operands := parsed.operands

	switch options.program {
	case operandsAreText:
		operands = nil
	case firstOperandIsPattern, firstOperandIsSedScript:
		if !parsed.programGiven && len(operands) > 0 {
			parsed.scripts = append(parsed.scripts, operands[0])
			operands = operands[1:]
		}
	}

	for _, script := range parsed.scripts {
		switch options.program {
		case firstOperandIsSedScript:
			if !sedScriptReadsNothing(script) {
				return nil, false
			}
		}
	}

	if options.program == firstOperandIsSedScript && len(parsed.scripts) == 0 {
		return nil, false
	}

	files := parsed.files
	for _, operand := range operands {
		if operand != "-" || !options.stdinDash {
			files = append(files, operand)
		}
	}

	return files, true
}

func parseReader(options readerOptions, args []string) (readerArgs, bool) {
	var parsed readerArgs

	for index := 0; index < len(args); index++ {
		arg := args[index]

		switch {
		// GNU reads an option after an operand as an option, BSD as a file:
		// a word that is either counts as outside.
		case len(parsed.operands) > 0 && strings.HasPrefix(arg, "-") && arg != "-":
			return readerArgs{}, false
		case arg == "--":
			parsed.operands = append(parsed.operands, args[index+1:]...)
			index = len(args)
		case strings.HasPrefix(arg, "--"):
			next, known := parsed.long(options, args, index)
			if !known {
				return readerArgs{}, false
			}

			index = next
		case strings.HasPrefix(arg, "-") && len(arg) > 1:
			next, known := parsed.short(options, args, index)
			if !known {
				return readerArgs{}, false
			}

			index = next
		default:
			parsed.operands = append(parsed.operands, arg)
		}
	}

	return parsed, true
}

// long reads the long option at args[index] and returns the index of the last
// word it took.
func (parsed *readerArgs) long(options readerOptions, args []string, index int) (int, bool) {
	name, value, hasValue := strings.Cut(args[index][2:], "=")

	switch {
	case slices.Contains(options.longFlags, name):
		return index, !hasValue
	case slices.Contains(options.longOptional, name):
		return index, true
	case slices.Contains(options.longPairs, name) || slices.Contains(options.longFilePairs, name):
		if hasValue || index+2 >= len(args) {
			return index, false
		}

		if slices.Contains(options.longFilePairs, name) {
			parsed.files = append(parsed.files, args[index+2])
		}

		return index + 2, true
	}

	isValue := slices.Contains(options.longValues, name)
	isFile := slices.Contains(options.longFiles, name)
	isScript := slices.Contains(options.longScripts, name)

	if !isValue && !isFile && !isScript {
		return index, false
	}

	if !hasValue {
		if index+1 >= len(args) {
			return index, false
		}

		index++
		value = args[index]
	}

	parsed.take(value, isFile, isScript, slices.Contains(options.longProgramFiles, name))

	return index, true
}

// short reads the group of one-letter options at args[index], getopt's way:
// a letter taking a value takes the rest of the group, or the next word.
func (parsed *readerArgs) short(options readerOptions, args []string, index int) (int, bool) {
	group := args[index]

	for position := 1; position < len(group); position++ {
		letter := group[position]

		if strings.IndexByte(options.flags, letter) >= 0 {
			continue
		}

		isValue := strings.IndexByte(options.values, letter) >= 0
		isFile := strings.IndexByte(options.files, letter) >= 0
		isScript := strings.IndexByte(options.scripts, letter) >= 0

		if !isValue && !isFile && !isScript {
			return index, false
		}

		value := group[position+1:]
		if value == "" {
			if index+1 >= len(args) {
				return index, false
			}

			index++
			value = args[index]
		}

		parsed.take(value, isFile, isScript, strings.IndexByte(options.programFiles, letter) >= 0)

		return index, true
	}

	return index, true
}

func (parsed *readerArgs) take(value string, isFile bool, isScript bool, isProgramFile bool) {
	switch {
	case isFile:
		parsed.files = append(parsed.files, value)
	case isScript:
		parsed.scripts = append(parsed.scripts, value)
	}

	parsed.programGiven = parsed.programGiven || isScript || isProgramFile
}

// sedScriptReadsNothing parses a sed script far enough to hold it to
// commands that print, delete, substitute or move between lines: addresses,
// `!`, braces, `s` and `y` with any delimiter, and the one-letter commands
// that take no file. `r`, `R`, `w`, `W`, `e`, an `s` flag `w` or `e`, and
// any command it does not know make it false.
func sedScriptReadsNothing(script string) bool {
	position := 0

	skip := func(set string) {
		for position < len(script) && strings.ContainsRune(set, rune(script[position])) {
			position++
		}
	}

	// delimited moves past text up to an unescaped delimiter, and is false
	// where the script ends first or a line break comes.
	delimited := func(delimiter byte) bool {
		for position < len(script) {
			character := script[position]
			position++

			switch {
			case character == '\\':
				position++
			case character == '\n':
				return false
			case character == delimiter:
				return true
			}
		}

		return false
	}

	address := func() bool {
		switch {
		case position >= len(script):
		case script[position] == '$':
			position++
		case script[position] >= '0' && script[position] <= '9':
			skip("0123456789~")
		case script[position] == '/':
			position++
			if !delimited('/') {
				return false
			}

			skip("IM")
		case script[position] == '\\' && position+1 < len(script):
			delimiter := script[position+1]
			position += 2
			if !delimited(delimiter) {
				return false
			}

			skip("IM")
		}

		return true
	}

	for {
		skip(" \t\n;")
		if position >= len(script) {
			return true
		}

		if !address() {
			return false
		}

		if position < len(script) && script[position] == ',' {
			position++
			skip("+~")
			if !address() {
				return false
			}
		}

		skip(" \t!")
		if position >= len(script) {
			return false
		}

		command := script[position]
		position++

		switch {
		case command == '{':
			continue
		case strings.IndexByte("pdDnNgGhHxPz=}", command) >= 0:
		case command == 'q' || command == 'Q' || command == 'l':
			skip(" \t0123456789")
		case command == 's' || command == 'y':
			if position >= len(script) || script[position] == '\\' || script[position] == '\n' {
				return false
			}

			delimiter := script[position]
			position++
			if !delimited(delimiter) || !delimited(delimiter) {
				return false
			}

			if command == 's' {
				skip("gpiImM0123456789")
			}
		default:
			return false
		}

		skip(" \t")
		if position < len(script) && strings.IndexByte(";\n}", script[position]) < 0 {
			return false
		}
	}
}
