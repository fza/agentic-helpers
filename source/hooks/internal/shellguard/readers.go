package shellguard

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/fza/agentic-helpers/source/hooks/internal/shellwalk"
)

// readers are the commands whose output, every argument a literal inside the
// project, is the project's own text. awk is not among them: its program can
// read any file (getline, ARGV) and run commands, and shellwalk hands it on
// as inline code. sed and jq are, with their scripts held to what reads no
// other file and runs nothing.
var readers = []string{"cat", "head", "tail", "grep", "egrep", "fgrep", "sed", "wc", "sort", "uniq", "cut", "tr", "nl", "jq"}

// readerUnsafe are option words that name a file of file names or a program
// to run, whatever reader takes them.
var readerUnsafe = []string{"files0-from", "compress-program"}

var (
	jqUnsafe        = regexp.MustCompile(`\$ENV\b|\benv\b|\bimport\b|\binclude\b|\$__prog_args`)
	sedFlags        = "nErsuz"
	sedLongFlags    = []string{"--quiet", "--silent", "--regexp-extended", "--separate", "--unbuffered", "--null-data", "--posix", "--debug", "--sandbox"}
	jqFlags         = "nrjascCMSeR"
	jqUnsafeOptions = []string{"-f", "--from-file", "-L", "--library-path", "--args", "--jsonargs"}
	// grepValueFlags are grep's one-letter options taking the next word as
	// their value when last in a group.
	grepValueFlags = "efmABCdD"
)

// projectRoot is the project the session started in, through its symlinks,
// and none where it is unset, `/` or the home directory.
func projectRoot(env Env) string {
	root := physical(env.ProjectDir)
	if root == "/" || root == physical(env.Home) {
		return ""
	}

	return root
}

// insideProject is whether target, absolute or relative to the working
// directory, lies in the project or is it, once every symlink it passes is
// resolved.
func insideProject(target string, workingDir string, home string, root string) bool {
	if root == "" || target == "" {
		return false
	}

	resolved := resolve(target, physical(workingDir), home)
	if resolved == "" {
		return false
	}

	actual, err := filepath.EvalSymlinks(resolved)
	if err == nil {
		resolved = actual
	}

	return resolved == root || within(resolved, root)
}

// ReadsOnlyProject is whether a Bash command only shows project files: every
// command a known reader, every word literal, no glob, every path it may name
// inside the project, no directory change, no assignment, no nested code, and
// redirections only to project files, /dev/null or another descriptor. Every
// word but a reader's own program counts as a path, a count among them: a
// word that names no file resolves inside the project anyway, and one that
// looks like an outside path only costs a redaction.
func ReadsOnlyProject(command string, workingDir string, env Env) bool {
	root := projectRoot(env)
	if root == "" || !insideProject(workingDir, workingDir, env.Home, root) {
		return false
	}

	file, err := shellwalk.Parse(command)
	if err != nil {
		return false
	}

	check := projectReads{root: root, workingDir: workingDir, home: env.Home, only: true}
	syntax.Walk(file, check.node)
	shellwalk.Walk(file, &check)

	return check.only
}

type projectReads struct {
	root       string
	workingDir string
	home       string
	only       bool
}

func (check *projectReads) inside(target string) bool {
	return target == "" || insideProject(target, check.workingDir, check.home, check.root)
}

func (check *projectReads) node(node syntax.Node) bool {
	switch typed := node.(type) {
	case *syntax.Stmt:
		for _, redirect := range typed.Redirs {
			check.redirect(redirect)
		}

		switch typed.Cmd.(type) {
		case nil, *syntax.CallExpr, *syntax.BinaryCmd, *syntax.Subshell, *syntax.Block:
		default:
			check.only = false
		}
	case *syntax.CallExpr:
		if len(typed.Assigns) > 0 {
			check.only = false
		}
	}

	return check.only
}

func (check *projectReads) redirect(redirect *syntax.Redirect) {
	if redirect.Hdoc != nil {
		_, isLiteral := shellwalk.Literal(redirect.Hdoc)
		check.only = check.only && isLiteral
	}

	if redirect.Word == nil {
		return
	}

	target, isLiteral := shellwalk.Literal(redirect.Word)
	switch {
	case !isLiteral || shellwalk.Patterned(redirect.Word):
		check.only = false
	case redirect.Op == syntax.Hdoc || redirect.Op == syntax.DashHdoc || redirect.Op == syntax.WordHdoc:
	case redirect.Op == syntax.DplIn || redirect.Op == syntax.DplOut:
		check.only = check.only && strings.Trim(target, "0123456789-") == ""
	case target == "/dev/null":
	default:
		check.only = check.only && check.inside(target)
	}
}

func (check *projectReads) Command(call shellwalk.Call) {
	words := make([]string, 0, len(call.Args))

	for _, arg := range call.Args {
		value, isLiteral := shellwalk.Literal(arg)
		if !isLiteral || shellwalk.Patterned(arg) {
			check.only = false

			return
		}

		words = append(words, value)
	}

	// Only a reader passes. No reader changes directory or runs code, so cd,
	// pushd, popd, every launcher's chdir option, a shell, eval, trap and every
	// interpreter end the check here, and the walk's Code, Inline and Piped
	// never find anything this has not already refused.
	if !slices.Contains(readers, words[0]) {
		check.only = false

		return
	}

	programs, readsNothing := programWords(words[0], words[1:])
	if !readsNothing {
		check.only = false

		return
	}

	for index, word := range words[1:] {
		if slices.ContainsFunc(readerUnsafe, func(option string) bool { return strings.Contains(word, option) }) {
			check.only = false

			return
		}

		target := word
		if strings.HasPrefix(word, "-") {
			_, value, found := strings.Cut(word, "=")
			if !found {
				continue
			}

			target = value
		}

		if !programs[index] && !check.inside(target) {
			check.only = false

			return
		}
	}
}

func (check *projectReads) Code(string, []*syntax.Word) {}

func (check *projectReads) Inline(string, []*syntax.Word) {}

func (check *projectReads) Piped(string) {}

// programWords is the reader's arguments that are its own program or a text
// value, not a path: a grep pattern, a sed script, a jq filter and its
// `--arg` values. It is false where that program may read another file or run
// something: grep -R follows symlinks out of a named directory, sed's r, w
// and e commands, jq's env, import and include, a program read from a file.
func programWords(name string, args []string) (map[int]bool, bool) {
	switch name {
	case "grep", "egrep", "fgrep":
		return grepProgram(args)
	case "sed":
		return sedProgram(args)
	case "jq":
		return jqProgram(args)
	}

	return nil, true
}

// shortGroup is whether arg is a group of one-letter options, `-nE`.
func shortGroup(arg string) bool {
	return strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && len(arg) > 1
}

func grepProgram(args []string) (map[int]bool, bool) {
	programs := map[int]bool{}
	patternGiven := false
	firstOperand := -1

	for index := 0; index < len(args); index++ {
		arg := args[index]

		switch {
		case arg == "--":
			if firstOperand < 0 && index+1 < len(args) {
				firstOperand = index + 1
			}

			index = len(args)
		case arg == "--dereference-recursive":
			return nil, false
		case arg == "--regexp" || arg == "--file":
			patternGiven = true
			programs[index+1] = arg == "--regexp"
			index++
		case strings.HasPrefix(arg, "--regexp=") || strings.HasPrefix(arg, "--file="):
			patternGiven = true
			programs[index] = strings.HasPrefix(arg, "--regexp=")
		case shortGroup(arg):
			if strings.Contains(arg, "R") {
				return nil, false
			}

			letter := arg[len(arg)-1]
			if strings.IndexByte(grepValueFlags, letter) >= 0 {
				patternGiven = patternGiven || letter == 'e' || letter == 'f'
				programs[index+1] = letter == 'e'
				index++
			}
		case strings.HasPrefix(arg, "-"):
		case firstOperand < 0:
			firstOperand = index
		}
	}

	if !patternGiven && firstOperand >= 0 {
		programs[firstOperand] = true
	}

	return programs, true
}

func sedProgram(args []string) (map[int]bool, bool) {
	programs := map[int]bool{}
	scriptGiven := false
	firstOperand := -1

	for index := 0; index < len(args); index++ {
		arg := args[index]

		switch {
		case arg == "--":
			if firstOperand < 0 && index+1 < len(args) {
				firstOperand = index + 1
			}

			index = len(args)
		case arg == "--expression" || shortGroup(arg) && strings.HasSuffix(arg, "e") && strings.Trim(arg[1:len(arg)-1], sedFlags) == "":
			if index+1 >= len(args) || !sedScriptReadsNothing(args[index+1]) {
				return nil, false
			}

			scriptGiven = true
			programs[index+1] = true
			index++
		case strings.HasPrefix(arg, "--expression="):
			if !sedScriptReadsNothing(strings.TrimPrefix(arg, "--expression=")) {
				return nil, false
			}

			scriptGiven = true
			programs[index] = true
		case strings.HasPrefix(arg, "--"):
			if !slices.Contains(sedLongFlags, arg) {
				return nil, false
			}
		case shortGroup(arg):
			if strings.Trim(arg[1:], sedFlags) != "" {
				return nil, false
			}
		case firstOperand < 0:
			firstOperand = index
		}
	}

	if scriptGiven {
		return programs, true
	}

	if firstOperand < 0 || !sedScriptReadsNothing(args[firstOperand]) {
		return nil, false
	}

	programs[firstOperand] = true

	return programs, true
}

func jqProgram(args []string) (map[int]bool, bool) {
	programs := map[int]bool{}
	filter := -1

	for index := 0; index < len(args); index++ {
		arg := args[index]

		switch {
		case arg == "--":
			if filter < 0 && index+1 < len(args) {
				filter = index + 1
			}

			index = len(args)
		case slices.Contains(jqUnsafeOptions, arg) || strings.HasPrefix(arg, "--from-file=") || strings.HasPrefix(arg, "--library-path="):
			return nil, false
		case arg == "--arg" || arg == "--argjson":
			programs[index+1], programs[index+2] = true, true
			index += 2
		case arg == "--slurpfile" || arg == "--rawfile":
			programs[index+1] = true
			index += 2
		case arg == "--indent":
			index++
		case strings.HasPrefix(arg, "--"):
		case shortGroup(arg):
			if strings.Trim(arg[1:], jqFlags) != "" {
				return nil, false
			}
		case filter < 0:
			filter = index
		}
	}

	if filter < 0 {
		return programs, true
	}

	if jqUnsafe.MatchString(args[filter]) {
		return nil, false
	}

	programs[filter] = true

	return programs, true
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
