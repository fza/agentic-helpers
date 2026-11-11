package shellguard

import (
	"context"
	"fmt"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/fza/agentic-helpers/source/hooks/internal/credfile"
	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/shellwalk"
)

// grepPrograms are the Bash programs whose output lines carry the file they
// came from as a `<file>:` prefix once they search a tree or several files.
var grepPrograms = []string{"grep", "egrep", "fgrep"}

// lineNumber is the line number grep -n and the Grep tool put after the file,
// `:` on a matching line and `-` on a context line.
var lineNumber = regexp.MustCompile(`^([0-9]+)[:\-]`)

// runsGrep is whether a Bash command may run grep: a literal word of any call
// names it, so `xargs grep` and `find -exec grep` count. A word naming it as
// text (`echo grep`) costs only a search of the output for prefixes.
func runsGrep(command string) bool {
	file, err := shellwalk.Parse(command)
	if err != nil {
		return false
	}

	found := false

	syntax.Walk(file, func(node syntax.Node) bool {
		call, isCall := node.(*syntax.CallExpr)
		if !isCall {
			return !found
		}

		for _, arg := range call.Args {
			name, isLiteral := shellwalk.Literal(arg)
			found = found || isLiteral && slices.Contains(grepPrograms, path.Base(name))
		}

		return !found
	})

	return found
}

// prefixedFile is a file a grep output prefix names: every line a span of it
// covers, with the span's rule; a credential store's span covers every line.
type prefixedFile struct {
	spanText map[string]string
}

// fileLines withholds, in every string of a decoded Grep or grep response,
// each line whose `<file>:` prefix names a file holding a credential on that
// line: a line one of the file's spans covers, its number dropped. Inside the
// project as outside it: a search over a tree shows a file the agent never
// asked to read.
type fileLines struct {
	ctx        context.Context
	env        Env
	skips      []pluginconfig.SecretReadSkip
	workingDir string
	files      map[string]*prefixedFile
	withheld   int
}

func withholdFileLines(ctx context.Context, env Env, skips []pluginconfig.SecretReadSkip, workingDir string, response any) (any, int) {
	pass := fileLines{ctx: ctx, env: env, skips: skips, workingDir: workingDir, files: map[string]*prefixedFile{}}

	withheld := mapStrings(response, pass.text)

	return withheld, pass.withheld
}

func (pass *fileLines) text(text string) string {
	if !strings.Contains(text, ":") {
		return text
	}

	lines := strings.Split(text, "\n")

	for index, line := range lines {
		file, rest := pass.prefix(line)
		if file == nil {
			continue
		}

		rule := file.holds(rest)
		if rule == "" {
			continue
		}

		lines[index] = fmt.Sprintf(markerWithheld, rule)
		pass.withheld++
	}

	return strings.Join(lines, "\n")
}

// holds is the rule of the span holding the text of a prefixed line, its line
// number dropped where it carries one, or "" where none does. A line is
// judged by its text, not its number: grep numbers lines as the file holds
// them, and a line a span covers is withheld wherever it appears in the file,
// which costs at most a repeated line.
func (file *prefixedFile) holds(rest string) string {
	match := lineNumber.FindString(rest)
	if match != "" {
		rule := file.spanText[strings.TrimSpace(rest[len(match):])]
		if rule != "" {
			return rule
		}
	}

	return file.spanText[strings.TrimSpace(rest)]
}

// prefix is the file a line's prefix names and the rest of the line, nil
// where no prefix ending at a `:` or `-` names a regular file. The first that
// does wins: grep writes the name as found, and a name holding a separator
// is rare.
func (pass *fileLines) prefix(line string) (*prefixedFile, string) {
	for index := 1; index < len(line); index++ {
		if line[index] != ':' && line[index] != '-' {
			continue
		}

		named := line[:index]

		file, known := pass.files[named]
		if !known {
			file = pass.load(named)
			pass.files[named] = file
		}

		if file != nil {
			return file, line[index+1:]
		}
	}

	return nil, ""
}

// load finds the lines a prefixed file's spans cover, nil where it names no
// regular file, none for one the skip list names. Its first
// credfile.ScanLimit bytes are scanned.
func (pass *fileLines) load(named string) *prefixedFile {
	actual := credentialPath(named, pass.workingDir, pass.env.Home)
	if actual == "" {
		return nil
	}

	info, err := os.Stat(actual)
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}

	file := &prefixedFile{spanText: map[string]string{}}
	if secretReadSkipped(pass.env, pass.skips, actual) {
		return file
	}

	part, err := scanned(actual, info.Size(), 1, 0)
	if err != nil {
		return file
	}

	lines := part.lines()
	for _, span := range credfile.Spans(pass.ctx, actual, part.text, 1) {
		for number := span.First; number <= span.Last && number <= len(lines); number++ {
			text := strings.TrimSpace(lines[number-1])
			if text != "" && file.spanText[text] == "" {
				file.spanText[text] = span.Rule
			}
		}
	}

	return file
}
