package shellwalk

import (
	"regexp"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// fixedValues is what the text itself fixes of its variables. A variable
// resolves at a use only where every assignment to it anywhere in the text is
// a statement of the command's or script's own (bare, `export` or
// `readonly`, or the first of an `&&` or `||` chain), the last of them before
// the use gives it fixed text, and the use lies in no function body. The
// whole text counts, not only what precedes the use: a loop runs its body
// again after a later assignment in it, and a function runs where it is
// called, not where it stands. Any code that may set a variable the parse
// cannot see (eval, source, trap, a builtin storing what it reads, arithmetic,
// a command name built at run time, `declare` and its kin) leaves every
// variable unresolved. Reading the hook's own environment is no answer: it is
// not the shell's at run time.
// splitChanged is whether the text sets IFS anywhere, after which an unquoted
// expansion may split where the default would not.
type fixedValues struct {
	assigned     map[string][]fixedAssignment
	unfixed      map[string]bool
	opaque       bool
	splitChanged bool
	functions    [][2]uint
}

type fixedAssignment struct {
	offset uint
	value  *syntax.Word
}

// valueSetters are the builtins of bash and zsh that may set a variable the
// parse does not see assigned, or run code that may.
var valueSetters = []string{
	"eval", "source", ".", "trap", "read", "mapfile", "readarray", "getopts", "let", "unset", "wait", "compgen",
	"declare", "typeset", "local", "nameref", "integer", "float", "vared", "zparseopts", "zstyle", "zformat",
	"zstat", "strftime", "getln", "sysread", "zregexparse", "autoload", "functions", "zmodload", "emulate",
	"sched", "alias", "fc", "enable",
}

// shellManaged are variables the shell itself changes as it runs, so no
// assignment in the text fixes them.
var shellManaged = []string{
	"IFS", "PWD", "OLDPWD", "HOME", "PATH", "CDPATH", "RANDOM", "SRANDOM", "SECONDS", "LINENO", "REPLY",
	"OPTARG", "OPTIND", "OPTERR", "MAPFILE", "PPID", "UID", "EUID", "SHLVL", "HOSTNAME", "PIPESTATUS",
	"FUNCNAME", "DIRSTACK", "EPOCHSECONDS", "EPOCHREALTIME", "COLUMNS", "LINES", "_",
}

var (
	plainIndex = regexp.MustCompile(`^(?:[0-9]+|@|\*)$`)
	// unquotedSafe is a value an unquoted expansion hands on as the one word
	// it spells: no character the default IFS splits at, no glob.
	unquotedSafe = regexp.MustCompile(`^[A-Za-z0-9._/:@%+=,-]+$`)
)

func fixedValuesOf(node syntax.Node) fixedValues {
	values := fixedValues{assigned: map[string][]fixedAssignment{}, unfixed: map[string]bool{}}
	qualifying := topLevelAssignments(node)

	syntax.Walk(node, func(visited syntax.Node) bool {
		switch typed := visited.(type) {
		case *syntax.Assign:
			values.assign(typed, qualifying[typed])
		case *syntax.DeclClause:
			if !plainDeclaration(typed) {
				values.opaque = true
			}
		case *syntax.CallExpr:
			if setsValues(typed.Args) {
				values.opaque = true
			}
		case *syntax.WordIter:
			values.unfixed[typed.Name.Value] = true
		case *syntax.ParamExp:
			values.expansion(typed)
		case *syntax.ArithmCmd, *syntax.ArithmExp, *syntax.LetClause, *syntax.CStyleLoop, *syntax.CoprocClause:
			values.opaque = true
		case *syntax.BinaryTest:
			if slices.Contains(arithmeticTests, typed.Op) {
				values.opaque = true
			}
		case *syntax.Redirect:
			// `{name}>file` stores a descriptor number in name.
			if typed.N != nil && strings.Trim(typed.N.Value, "0123456789") != "" {
				values.opaque = true
			}
		case *syntax.FuncDecl:
			values.functions = append(values.functions, [2]uint{typed.Body.Pos().Offset(), typed.Body.End().Offset()})
		}

		return true
	})

	values.splitChanged = values.unfixed["IFS"] || len(values.assigned["IFS"]) > 0

	return values
}

func (values *fixedValues) assign(assign *syntax.Assign, qualifying bool) {
	if assign.Name == nil {
		return
	}

	name := assign.Name.Value

	switch {
	case assign.Index != nil:
		values.opaque = true
	case !qualifying:
		values.unfixed[name] = true
	case assign.Naked:
	default:
		values.assigned[name] = append(values.assigned[name], fixedAssignment{offset: assign.Pos().Offset(), value: assign.Value})
	}
}

// expansion notes what a parameter expansion may set: `${X=y}` and `${X:=y}`
// assign X, and a subscript or slice is arithmetic, which may assign anything.
func (values *fixedValues) expansion(parameter *syntax.ParamExp) {
	if parameter.Exp != nil && parameter.Param != nil &&
		(parameter.Exp.Op == syntax.AssignUnset || parameter.Exp.Op == syntax.AssignUnsetOrNull) {
		values.unfixed[parameter.Param.Value] = true
	}

	if parameter.Slice != nil {
		values.opaque = true
	}

	if parameter.Index == nil {
		return
	}

	index, isWord := parameter.Index.(*syntax.Word)
	if !isWord {
		values.opaque = true

		return
	}

	text, literal := Literal(index)
	if !literal || !plainIndex.MatchString(text) {
		values.opaque = true
	}
}

// topLevelAssignments are the assignments that are statements of the text's
// own: an assignment-only statement, or `export` or `readonly` given no
// option, standing alone or first in an `&&` or `||` chain, with no
// redirection.
func topLevelAssignments(node syntax.Node) map[*syntax.Assign]bool {
	qualifying := map[*syntax.Assign]bool{}

	file, isFile := node.(*syntax.File)
	if !isFile {
		return qualifying
	}

	for _, statement := range file.Stmts {
		leading := leadingStatement(statement)
		if !plainStatement(leading) || len(leading.Redirs) > 0 {
			continue
		}

		var assigns []*syntax.Assign

		switch typed := leading.Cmd.(type) {
		case *syntax.CallExpr:
			if len(typed.Args) == 0 {
				assigns = typed.Assigns
			}
		case *syntax.DeclClause:
			if plainDeclaration(typed) {
				assigns = typed.Args
			}
		}

		for _, assign := range assigns {
			if !assign.Append && assign.Index == nil && assign.Array == nil {
				qualifying[assign] = true
			}
		}
	}

	return qualifying
}

// leadingStatement is the statement a chain of `&&` and `||` runs first,
// whatever the rest does.
func leadingStatement(statement *syntax.Stmt) *syntax.Stmt {
	binary, isBinary := statement.Cmd.(*syntax.BinaryCmd)
	if !isBinary || !plainStatement(statement) || binary.Op != syntax.AndStmt && binary.Op != syntax.OrStmt {
		return statement
	}

	return leadingStatement(binary.X)
}

func plainDeclaration(clause *syntax.DeclClause) bool {
	if clause.Variant == nil || clause.Variant.Value != "export" && clause.Variant.Value != "readonly" {
		return false
	}

	return !slices.ContainsFunc(clause.Args, func(assign *syntax.Assign) bool { return assign.Name == nil })
}

// setsValues is whether a call may set a variable unseen: a command name built
// at run time, or one of valueSetters, run as the call or by a launcher of it.
// printf and zsh's print store their output in a variable with -v.
func setsValues(args []*syntax.Word) bool {
	for _, start := range Certain(args) {
		_, literal := Literal(args[start])
		if !literal || Patterned(args[start]) {
			return true
		}

		name := CommandName(args[start])
		if slices.Contains(valueSetters, name) {
			return true
		}

		if (name == "printf" || name == "print") && storesOutput(args[start+1:]) {
			return true
		}
	}

	return false
}

func storesOutput(args []*syntax.Word) bool {
	for _, arg := range args {
		value, literal := Literal(arg)

		switch {
		case !literal:
			return true
		case value == "--" || !strings.HasPrefix(value, "-"):
			return false
		case strings.Contains(value, "v"):
			return true
		}
	}

	return false
}

func (values fixedValues) inFunction(offset uint) bool {
	return slices.ContainsFunc(values.functions, func(span [2]uint) bool { return offset >= span[0] && offset < span[1] })
}

// value is the text name holds at offset, and whether the text fixes it.
func (values fixedValues) value(name string, offset uint) (string, bool) {
	if values.opaque || values.unfixed[name] || slices.Contains(shellManaged, name) ||
		strings.HasPrefix(name, "BASH") || values.inFunction(offset) {
		return "", false
	}

	var last *fixedAssignment

	for index, assignment := range values.assigned[name] {
		if assignment.offset < offset {
			last = &values.assigned[name][index]
		}
	}

	if last == nil {
		return "", false
	}

	if last.value == nil {
		return "", true
	}

	return values.assignedText(last.value, last.offset)
}

// assignedText is the text an assignment's value gives: fixed text, joined
// with variables that resolve by the same rule. An assignment neither splits
// nor globs, so an unquoted variable in it is its text. A `~` or a backslash
// anywhere leaves it unresolved: the shell expands the one and reads the other
// in ways only the run settles.
func (values fixedValues) assignedText(word *syntax.Word, offset uint) (string, bool) {
	var text strings.Builder

	for _, part := range word.Parts {
		switch typed := part.(type) {
		case *syntax.Lit:
			text.WriteString(typed.Value)
		case *syntax.SglQuoted:
			decoded, resolved := ansiC(typed)
			if !resolved {
				return "", false
			}

			text.WriteString(decoded)
		case *syntax.DblQuoted:
			for _, inner := range typed.Parts {
				resolved, fixed := values.quotedPart(inner, offset)
				if !fixed {
					return "", false
				}

				text.WriteString(resolved)
			}
		case *syntax.ParamExp:
			resolved, fixed := values.parameter(typed, offset)
			if !fixed {
				return "", false
			}

			text.WriteString(resolved)
		default:
			return "", false
		}
	}

	if strings.ContainsAny(text.String(), `~\`) {
		return "", false
	}

	return text.String(), true
}

func (values fixedValues) quotedPart(part syntax.WordPart, offset uint) (string, bool) {
	switch typed := part.(type) {
	case *syntax.Lit:
		return typed.Value, true
	case *syntax.ParamExp:
		return values.parameter(typed, offset)
	}

	return "", false
}

func (values fixedValues) parameter(parameter *syntax.ParamExp, offset uint) (string, bool) {
	if !plainParameter(parameter) {
		return "", false
	}

	return values.value(parameter.Param.Value, offset)
}

// words is args with every variable the text fixes written out as the text
// it holds: inside double quotes always, unquoted only where the value stays
// the one word it spells. A word holding nothing to write out is kept as is.
func (values fixedValues) words(args []*syntax.Word) []*syntax.Word {
	resolved := args
	cloned := false

	for index, arg := range args {
		word, changed := values.word(arg)
		if !changed {
			continue
		}

		if !cloned {
			resolved = slices.Clone(args)
			cloned = true
		}

		resolved[index] = word
	}

	return resolved
}

func (values fixedValues) word(word *syntax.Word) (*syntax.Word, bool) {
	parts := word.Parts
	changed := false

	for index, part := range word.Parts {
		var replaced syntax.WordPart

		switch typed := part.(type) {
		case *syntax.ParamExp:
			text, fixed := values.parameter(typed, typed.Pos().Offset())
			if fixed && !values.splitChanged && unquotedSafe.MatchString(text) {
				replaced = &syntax.SglQuoted{Left: typed.Pos(), Right: typed.End(), Value: text}
			}
		case *syntax.DblQuoted:
			quoted, quotedChanged := values.quoted(typed)
			if quotedChanged {
				replaced = quoted
			}
		}

		if replaced == nil {
			continue
		}

		if !changed {
			parts = slices.Clone(word.Parts)
			changed = true
		}

		parts[index] = replaced
	}

	if !changed {
		return word, false
	}

	return &syntax.Word{Parts: parts}, true
}

func (values fixedValues) quoted(quoted *syntax.DblQuoted) (*syntax.DblQuoted, bool) {
	parts := quoted.Parts
	changed := false

	for index, part := range quoted.Parts {
		parameter, isParameter := part.(*syntax.ParamExp)
		if !isParameter {
			continue
		}

		text, fixed := values.parameter(parameter, parameter.Pos().Offset())
		if !fixed {
			continue
		}

		if !changed {
			parts = slices.Clone(quoted.Parts)
			changed = true
		}

		parts[index] = &syntax.Lit{ValuePos: parameter.Pos(), ValueEnd: parameter.End(), Value: text}
	}

	if !changed {
		return quoted, false
	}

	return &syntax.DblQuoted{Left: quoted.Left, Right: quoted.Right, Dollar: quoted.Dollar, Parts: parts}, true
}
