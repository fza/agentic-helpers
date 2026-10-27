package shellguard

import (
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/fza/agentic-helpers/source/hooks/internal/shellwalk"
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
// resolved. A path that does not resolve, a dangling link or a file that does
// not exist among them, counts as outside: a wrong outside costs only a
// withheld line.
func insideProject(target string, workingDir string, home string, root string) bool {
	if root == "" || target == "" {
		return false
	}

	resolved := resolve(target, physical(workingDir), home)
	if resolved == "" {
		return false
	}

	actual, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		return false
	}

	return actual == root || within(actual, root)
}

// ReadsOnlyProject is whether a Bash command only shows project files: every
// command a known reader carrying only options on its list, every word
// literal, no glob, every file it names inside the project, no assignment,
// and redirections only to project files, /dev/null or another descriptor.
func ReadsOnlyProject(command string, workingDir string, env Env) bool {
	root := projectRoot(env)
	if !insideProject(workingDir, workingDir, env.Home, root) {
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
	return insideProject(target, check.workingDir, check.home, check.root)
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

// Command passes only a reader, named bare, carrying only the options on its
// list, every file it names inside the project. No reader changes directory
// or runs code, so cd, pushd, popd, every launcher's chdir option, a shell,
// eval, trap and every interpreter end the check here, and the walk's Code,
// Inline and Piped never find anything this has not already refused.
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

	options, isReader := readerTable[words[0]]
	if !isReader {
		check.only = false

		return
	}

	files, known := readerFiles(options, words[1:])
	if !known {
		check.only = false

		return
	}

	for _, file := range files {
		if !check.inside(file) {
			check.only = false

			return
		}
	}
}

func (check *projectReads) Code(string, []*syntax.Word) {}

func (check *projectReads) Inline(string, []*syntax.Word) {}

func (check *projectReads) Piped(string) {}
