package shellwalk

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// folderOperands is args with every word naming the folder of the script the
// walk reads written out as that folder's path: `$(dirname "$0")`,
// `$(dirname "${BASH_SOURCE[0]}")` where bash runs the script, `${0%/*}` and
// `$(cd "$(dirname "$0")" && pwd)`, each optionally followed by fixed text.
// Code the walk reads as no script's, a command line or a sourced file, has
// no such folder: there `$0` is the shell's own name.
func (walked *walker) folderOperands(args []*syntax.Word, dir string) []*syntax.Word {
	if walked.scripts.Program == "" {
		return args
	}

	resolved := args
	cloned := false

	for index, arg := range args {
		word := walked.folderWord(arg, dir)
		if word == nil {
			continue
		}

		if !cloned {
			resolved = append([]*syntax.Word{}, args...)
			cloned = true
		}

		resolved[index] = word
	}

	return resolved
}

// folderWord is word with the folder it opens with written out as fixed
// text, or nil where it opens with none. What follows the folder stays as
// written, so the cd judges it as it judges any operand: fixed text is
// followed, anything else loses the place.
func (walked *walker) folderWord(word *syntax.Word, dir string) *syntax.Word {
	if len(word.Parts) == 0 {
		return nil
	}

	first := word.Parts[0]
	rest := word.Parts[1:]

	quoted, isQuoted := first.(*syntax.DblQuoted)
	if isQuoted {
		if len(quoted.Parts) == 0 {
			return nil
		}

		first = quoted.Parts[0]
		rest = append([]syntax.WordPart{&syntax.DblQuoted{Parts: quoted.Parts[1:]}}, rest...)
	}

	folder, isFolder := walked.folder(first, dir)
	if !isFolder || !isQuoted && (walked.values.splitChanged || !unquotedSafe.MatchString(folder)) {
		return nil
	}

	// cd fails on a path running through anything but a folder, `x.sh/..`
	// among them, and leaves the shell where it was.
	resolved := walked.resolve(folder, dir)
	if resolved == "" || !walked.isDirectory(resolved) {
		return nil
	}

	parts := append([]syntax.WordPart{&syntax.SglQuoted{Left: first.Pos(), Right: first.End(), Value: folder}}, rest...)

	return &syntax.Word{Parts: parts}
}

// folder is the folder a word part names, as text the shell's cd reads where
// it is relative: `${0%/*}` cuts the script's path at its last slash, and
// leaves a path holding none as it is.
func (walked *walker) folder(part syntax.WordPart, dir string) (string, bool) {
	switch typed := part.(type) {
	case *syntax.ParamExp:
		if !programCut(typed) {
			return "", false
		}

		program := walked.scripts.Program

		cut := strings.LastIndex(program, "/")
		if cut < 0 {
			return program, true
		}

		return program[:cut], true
	case *syntax.CmdSubst:
		return walked.substitutedFolder(typed, dir)
	}

	return "", false
}

// substitutedFolder reads `$(dirname <script>)` and
// `$(cd "$(dirname <script>)" && pwd)`, the second as an absolute path. The
// second prints nothing where its cd fails, which folderWord rules out by
// finding the folder there.
func (walked *walker) substitutedFolder(substitution *syntax.CmdSubst, dir string) (string, bool) {
	statement := onlyStatement(substitution)
	if statement == nil {
		return "", false
	}

	folder, isDirname := walked.dirname(statement)
	if isDirname {
		return folder, true
	}

	binary, isBinary := statement.Cmd.(*syntax.BinaryCmd)
	if !isBinary || binary.Op != syntax.AndStmt {
		return "", false
	}

	enter, print := plainCall(binary.X), plainCall(binary.Y)
	if enter == nil || print == nil || len(binary.Y.Redirs) > 0 || len(enter.Args) != 2 || len(print.Args) != 1 {
		return "", false
	}

	name, literal := Literal(enter.Args[0])
	printer, printLiteral := Literal(print.Args[0])

	if !literal || name != "cd" || !printLiteral || printer != "pwd" {
		return "", false
	}

	inner := innerSubstitution(enter.Args[1])
	if inner == nil {
		return "", false
	}

	innerStatement := onlyStatement(inner)
	if innerStatement == nil {
		return "", false
	}

	folder, isDirname = walked.dirname(innerStatement)
	if !isDirname {
		return "", false
	}

	resolved := walked.resolve(folder, dir)

	return resolved, resolved != ""
}

func onlyStatement(substitution *syntax.CmdSubst) *syntax.Stmt {
	if len(substitution.Stmts) != 1 || len(substitution.Stmts[0].Redirs) > 0 || !plainStatement(substitution.Stmts[0]) {
		return nil
	}

	return substitution.Stmts[0]
}

// innerSubstitution is the command substitution a word holds alone inside
// double quotes, or nil.
func innerSubstitution(word *syntax.Word) *syntax.CmdSubst {
	if len(word.Parts) != 1 {
		return nil
	}

	quoted, isQuoted := word.Parts[0].(*syntax.DblQuoted)
	if !isQuoted || len(quoted.Parts) != 1 {
		return nil
	}

	substitution, isSubstitution := quoted.Parts[0].(*syntax.CmdSubst)
	if !isSubstitution {
		return nil
	}

	return substitution
}

// dirname is what `dirname "$0"` or `dirname "${BASH_SOURCE[0]}"` prints for
// the script, as POSIX dirname derives it.
func (walked *walker) dirname(statement *syntax.Stmt) (string, bool) {
	call := plainCall(statement)
	if call == nil || len(call.Args) != 2 {
		return "", false
	}

	name, literal := Literal(call.Args[0])
	if !literal || name != "dirname" || !walked.namesProgram(call.Args[1]) {
		return "", false
	}

	return posixDirname(walked.scripts.Program), true
}

// namesProgram is whether a word is `"$0"`, or `"${BASH_SOURCE[0]}"` in a
// script bash runs and nothing in it may assign: other shells leave
// BASH_SOURCE unset.
func (walked *walker) namesProgram(word *syntax.Word) bool {
	if len(word.Parts) != 1 {
		return false
	}

	quoted, isQuoted := word.Parts[0].(*syntax.DblQuoted)
	if !isQuoted || len(quoted.Parts) != 1 {
		return false
	}

	parameter, isParameter := quoted.Parts[0].(*syntax.ParamExp)
	if !isParameter || parameter.Param == nil {
		return false
	}

	if parameter.Param.Value == "0" {
		return plainParameter(parameter)
	}

	values := walked.values
	if !walked.scripts.Bash || parameter.Param.Value != "BASH_SOURCE" || values.opaque ||
		values.unfixed["BASH_SOURCE"] || len(values.assigned["BASH_SOURCE"]) > 0 {
		return false
	}

	index, isWord := parameter.Index.(*syntax.Word)
	if !isWord {
		return false
	}

	value, literal := Literal(index)
	unindexed := *parameter
	unindexed.Index = nil

	return literal && value == "0" && plainParameter(&unindexed)
}

// programCut is whether a parameter expansion is `${0%/*}`.
func programCut(parameter *syntax.ParamExp) bool {
	if parameter.Param == nil || parameter.Param.Value != "0" || parameter.Exp == nil ||
		parameter.Exp.Op != syntax.RemSmallSuffix || parameter.Exp.Word == nil {
		return false
	}

	pattern, literal := Literal(parameter.Exp.Word)
	unexpanded := *parameter
	unexpanded.Exp = nil

	return literal && pattern == "/*" && plainParameter(&unexpanded)
}

func posixDirname(name string) string {
	trimmed := strings.TrimRight(name, "/")
	if trimmed == "" {
		if name == "" {
			return "."
		}

		return "/"
	}

	cut := strings.LastIndex(trimmed, "/")
	if cut < 0 {
		return "."
	}

	parent := strings.TrimRight(trimmed[:cut], "/")
	if parent == "" {
		return "/"
	}

	return parent
}
