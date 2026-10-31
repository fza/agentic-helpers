package shellwalk

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Files opens the scripts a command runs, by absolute path.
type Files interface {
	Open(name string) (fs.File, error)
}

type OSFiles struct{}

func (OSFiles) Open(name string) (fs.File, error) {
	opened, err := os.Open(name)
	if err != nil {
		return nil, fmt.Errorf("opening the script: %w", err)
	}

	return opened, nil
}

// Scripts is what a walk reads the scripts a command runs through: Files, Home
// for a path opening with `~`, Dir where the command starts ("" where it is
// not known), and Depth, how many scripts deep the walked code already lies.
type Scripts struct {
	Files Files
	Home  string
	Dir   string
	Depth int
}

// At is scripts for code that runs in dir, as deep as this.
func (scripts Scripts) At(dir string) Scripts {
	scripts.Dir = dir

	return scripts
}

// MaxScript is the most of a script the walk reads; MaxDepth how many
// scripts deep it follows one script running another.
const (
	MaxScript = 1 << 20
	MaxDepth  = 8
)

// Why a script goes unread, as Unread reports it.
const (
	UnreadMissing   = "no such file"
	UnreadOpen      = "cannot open it"
	UnreadBuilt     = "its path is built at run time"
	UnreadDirectory = "the directory it runs in is not known"
	UnreadLarge     = "over 1 MiB"
	UnreadWritten   = "the command writes it before running it"
	UnreadDeep      = "scripts run scripts too deep"
)

// scriptShells are the shells whose scripts the walk reads: fish's syntax is
// no shell's the parser reads.
var scriptShells = []string{"sh", "bash", "zsh", "dash", "ksh", "mksh"}

var shellMoves = []string{"cd", "pushd", "popd"}

// binaryProbe is how far into a file a NUL byte marks it as no text, as git
// tells a binary file.
const binaryProbe = 8000

// writtenScript is what the command writes to a path before it may run it:
// the text a here-document gives it, or known false where something else
// writes it.
type writtenScript struct {
	text  string
	known bool
}

// walker carries what a walk follows from one call to the next: the directory
// the shell is in, "" once it cannot tell, and what the command writes.
type walker struct {
	visitor Visitor
	scripts Scripts
	reading bool
	dir     string
	moves   directoryMoves
	written map[string]writtenScript
	// writesUnknown is a write earlier in the command to a path the walk
	// cannot place: any script after it may be what it wrote.
	writesUnknown bool
}

// directoryMoves / followed are the `cd` calls whose move the walk follows;
// lost the statements of the command's own that start where the walk can no
// longer tell, since the chain before them may have stopped short of its cd.
type directoryMoves struct {
	followed map[*syntax.CallExpr]bool
	lost     map[*syntax.Stmt]bool
}

// followedMoves finds the cds a walk follows, each one whatever runs after
// it runs only where it went. A cd that is a statement of the command's own,
// or that opens an `&&` chain of such cds, moves every later statement. A cd
// behind another command in a top-level `&&` chain moves the rest of that
// chain only: the next statement runs whether or not the chain got as far as
// the cd. `cd <dir> || exit [n]` and `|| return [n]` move every later
// statement, since a failed cd ends the shell or function there. A cd in a
// subshell, a pipeline, a function, a branch or before `||` anything else is
// none: on failure what follows runs where the shell was.
func followedMoves(node syntax.Node) directoryMoves {
	moves := directoryMoves{followed: map[*syntax.CallExpr]bool{}, lost: map[*syntax.Stmt]bool{}}

	file, isFile := node.(*syntax.File)
	if !isFile {
		return moves
	}

	for index, statement := range file.Stmts {
		guarded := guardedMove(statement)
		if guarded != nil {
			moves.followed[guarded] = true

			continue
		}

		behind := false

		for _, element := range andChain(statement) {
			call := directoryMove(element)
			if call == nil {
				behind = true

				continue
			}

			moves.followed[call] = true

			if behind && index+1 < len(file.Stmts) {
				moves.lost[file.Stmts[index+1]] = true
			}
		}
	}

	return moves
}

func directoryMove(statement *syntax.Stmt) *syntax.CallExpr {
	call := plainCall(statement)
	if call == nil || CommandName(call.Args[0]) != "cd" {
		return nil
	}

	_, literal := Literal(call.Args[0])
	if !literal {
		return nil
	}

	return call
}

// guardedMove is the cd of a `cd <dir> || exit [n]` or `|| return [n]`
// statement, or nil. Given more than one operand, exit and return fail and
// end nothing.
func guardedMove(statement *syntax.Stmt) *syntax.CallExpr {
	binary, isBinary := statement.Cmd.(*syntax.BinaryCmd)
	if !isBinary || binary.Op != syntax.OrStmt || !plainStatement(statement) {
		return nil
	}

	call := directoryMove(binary.X)
	if call == nil {
		return nil
	}

	ends := plainCall(binary.Y)
	if ends == nil || len(ends.Args) > 2 {
		return nil
	}

	name, literal := Literal(ends.Args[0])
	if !literal || name != "exit" && name != "return" {
		return nil
	}

	return call
}

func andChain(statement *syntax.Stmt) []*syntax.Stmt {
	binary, isBinary := statement.Cmd.(*syntax.BinaryCmd)
	if !isBinary || binary.Op != syntax.AndStmt || !plainStatement(statement) {
		return []*syntax.Stmt{statement}
	}

	return append(andChain(binary.X), andChain(binary.Y)...)
}

func plainStatement(statement *syntax.Stmt) bool {
	return !statement.Negated && !statement.Background && !statement.Coprocess && !statement.Disown
}

func plainCall(statement *syntax.Stmt) *syntax.CallExpr {
	if !plainStatement(statement) {
		return nil
	}

	call, isCall := statement.Cmd.(*syntax.CallExpr)
	if !isCall || len(call.Args) == 0 || len(call.Assigns) > 0 {
		return nil
	}

	return call
}

// call offers one call and reads every script it runs. A cd the walk follows
// moves the shell for every call after it; any other move of the shell loses
// its place, and a launcher's chdir option loses only the place of what it
// runs.
func (walked *walker) call(expr *syntax.CallExpr) {
	args := expr.Args
	dir := walked.dir
	launchedDir := dir

	if walked.moves.followed[expr] {
		walked.dir = walked.changeDirectory(args[1:])
	} else {
		for _, start := range Certain(args) {
			if !MovesDirectory(spelledWords(args[start:])) {
				continue
			}

			if slices.Contains(shellMoves, CommandName(args[start])) {
				walked.dir = ""
			} else {
				launchedDir = ""
			}
		}
	}

	offerCall(walked.visitor, args, dir, launchedDir)

	if !walked.reading {
		return
	}

	starts := Certain(args)
	run := map[int]bool{}

	for _, start := range starts {
		runsIn := launchedDir
		if start == 0 {
			runsIn = dir
		}

		at := walked.runs(args[start:], runsIn)
		if at >= 0 {
			run[start+at] = true
		}
	}

	walked.names(args, starts, run, dir, launchedDir)
}

// readers only read the files they name, or change no more than their
// metadata: a script one names keeps its text.
var readers = []string{
	"cat", "head", "tail", "less", "more", "wc", "grep", "rg", "file", "stat", "ls", "test", "[", "diff", "cmp",
	"shasum", "md5", "od", "xxd", "chmod", "chown",
}

// names notes every path a call names as an argument as written, its text
// unknown: any command but a reader may write what it names (`cp a x.sh`,
// `sed -i ... x.sh`, `curl -o x.sh`, `dd of=x.sh`), and a list of writers
// would miss the one it does not know. A path it cannot place may be any
// script. The script the call runs itself is no write, and tee's writes are
// the ones records notes.
func (walked *walker) names(args []*syntax.Word, starts []int, run map[int]bool, dir string, launchedDir string) {
	name := CommandName(args[starts[len(starts)-1]])
	if name == "tee" || slices.Contains(readers, name) {
		return
	}

	for index, arg := range args {
		value, literal := Literal(arg)
		if run[index] || !literal || Patterned(arg) {
			continue
		}

		at := dir
		if len(starts) > 1 && index >= starts[1] {
			at = launchedDir
		}

		paths := []string{value}

		_, assigned, hasValue := strings.Cut(value, "=")
		if hasValue {
			paths = append(paths, assigned)
		}

		for _, named := range paths {
			resolved := walked.resolve(named, at)
			if resolved == "" {
				walked.writesUnknown = true

				continue
			}

			walked.written[resolved] = writtenScript{}
		}
	}
}

func spelledWords(args []*syntax.Word) []string {
	words := make([]string, 0, len(args))
	for _, arg := range args {
		words = append(words, Spelled(arg))
	}

	return words
}

// changeDirectory is where a plain cd moves the shell, or "" where the walk
// cannot tell: no fixed operand, `cd -`, zsh's two-operand form, or a
// directory that does not exist, which leaves the shell where it was.
func (walked *walker) changeDirectory(args []*syntax.Word) string {
	var operands []string

	optionsEnded := false

	for _, arg := range args {
		value, literal := Literal(arg)
		if !literal || Patterned(arg) {
			return ""
		}

		switch {
		case !optionsEnded && value == "--":
			optionsEnded = true
		case !optionsEnded && strings.HasPrefix(value, "-") && value != "-":
		default:
			operands = append(operands, value)
		}
	}

	target := "~"

	switch len(operands) {
	case 0:
	case 1:
		target = operands[0]
	default:
		return ""
	}

	if target == "-" {
		return ""
	}

	resolved := walked.resolve(target, walked.dir)
	if resolved == "" || !walked.isDirectory(resolved) {
		return ""
	}

	return resolved
}

func (walked *walker) isDirectory(name string) bool {
	if walked.scripts.Files == nil {
		return false
	}

	opened, err := walked.scripts.Files.Open(name)
	if err != nil {
		return false
	}

	defer func() { _ = opened.Close() }()

	info, err := opened.Stat()

	return err == nil && info.IsDir()
}

// resolve is the absolute path a word names from dir, "" where it cannot be
// known.
func (walked *walker) resolve(value string, dir string) string {
	home := walked.scripts.Home

	switch {
	case value == "~" && home != "":
		return filepath.Clean(home)
	case strings.HasPrefix(value, "~/") && home != "":
		return filepath.Join(home, value[2:])
	case strings.HasPrefix(value, "~"):
		return ""
	case filepath.IsAbs(value):
		return filepath.Clean(value)
	case dir == "":
		return ""
	}

	return filepath.Join(dir, value)
}

// writesFile are the redirections that write the file they name.
var writesFile = []syntax.RedirOperator{
	syntax.RdrOut, syntax.AppOut, syntax.RdrClob, syntax.RdrAll, syntax.AppAll, syntax.RdrAllClob,
	syntax.AppAllClob, syntax.RdrInOut,
}

var appends = []syntax.RedirOperator{syntax.AppOut, syntax.AppAll, syntax.AppAllClob}

// records notes every file a statement writes. `cat` or `tee` given a
// here-document writes its text; any other write leaves the file's text
// unknown, so a script it may be is never read from disk.
func (walked *walker) records(statement *syntax.Stmt) {
	if !walked.reading {
		return
	}

	text, fromHere := hereDocument(statement)
	name := ""

	call, isCall := statement.Cmd.(*syntax.CallExpr)
	if isCall && len(call.Args) > 0 {
		name = CommandName(call.Args[0])
	}

	for _, redirect := range statement.Redirs {
		if !slices.Contains(writesFile, redirect.Op) || redirect.Word == nil {
			continue
		}

		heredocCat := fromHere && name == "cat" && len(call.Args) == 1
		walked.write(redirect.Word, text, heredocCat, slices.Contains(appends, redirect.Op))
	}

	if name != "tee" {
		return
	}

	appending := false

	for _, arg := range call.Args[1:] {
		value, literal := Literal(arg)

		switch {
		case literal && (value == "-a" || value == "--append"):
			appending = true
		case literal && strings.HasPrefix(value, "-"):
		default:
			walked.write(arg, text, fromHere, appending)
		}
	}
}

func (walked *walker) write(target *syntax.Word, text string, known bool, appending bool) {
	value, literal := Literal(target)
	if !literal || Patterned(target) {
		walked.writesUnknown = true

		return
	}

	resolved := walked.resolve(value, walked.dir)
	if resolved == "" {
		walked.writesUnknown = true

		return
	}

	if appending && known {
		before, held := walked.written[resolved]

		switch {
		case held && before.known:
			text = before.text + text
		case held:
			known = false
		default:
			current, why := walked.readFile(resolved)
			switch why {
			case "":
				text = current + text
			case UnreadMissing:
			default:
				known = false
			}
		}
	}

	walked.written[resolved] = writtenScript{text: text, known: known}
}

// hereDocument is the text a statement's here-document or here-string feeds
// its command, as the command reads it.
func hereDocument(statement *syntax.Stmt) (string, bool) {
	for _, redirect := range statement.Redirs {
		switch redirect.Op {
		case syntax.Hdoc, syntax.DashHdoc:
			if redirect.Hdoc == nil {
				return "", true
			}

			return hereText(redirect.Word, redirect.Hdoc)
		case syntax.WordHdoc:
			value, literal := Literal(redirect.Word)

			return value + "\n", literal
		}
	}

	return "", false
}

// hereText reads a here-document's body: kept as written under a quoted
// delimiter, and with bash's escapes resolved under a bare one, where any
// expansion leaves the text unknown.
func hereText(delimiter *syntax.Word, body *syntax.Word) (string, bool) {
	quoted := slices.ContainsFunc(delimiter.Parts, func(part syntax.WordPart) bool {
		literal, isLit := part.(*syntax.Lit)

		return !isLit || strings.Contains(literal.Value, `\`)
	})

	var text strings.Builder

	for _, part := range body.Parts {
		literal, isLit := part.(*syntax.Lit)
		if !isLit {
			return "", false
		}

		text.WriteString(literal.Value)
	}

	if quoted {
		return text.String(), true
	}

	return strings.NewReplacer(`\\`, `\`, `\$`, `$`, "\\`", "`", "\\\n", "").Replace(text.String()), true
}

// runs reads the script a call runs, if it runs one: the operand of a shell
// given no code, what source or `.` reads, or a command named by a path.
// A path naming a command the walk knows by name is that command. It is the
// index of the script's word in args, or -1 where the call runs none.
func (walked *walker) runs(args []*syntax.Word, dir string) int {
	name := CommandName(args[0])

	switch {
	case slices.Contains(scriptShells, name):
		if shellCode(args[1:]) != nil {
			return -1
		}

		at := scriptOperand(args[1:])
		if at < 0 {
			return -1
		}

		walked.script(args[1+at], dir, false)

		return 1 + at
	case name == "source" || name == ".":
		at := -1

		if len(args) > 1 {
			walked.script(args[1], dir, false)

			at = 1
		}

		walked.afterSource(args)

		return at
	default:
		value, literal := Literal(args[0])
		if !literal || Patterned(args[0]) || !strings.Contains(value, "/") || knownCommand(name) {
			return -1
		}

		walked.script(args[0], dir, true)

		return 0
	}
}

func knownCommand(name string) bool {
	return slices.Contains(Shells, name) || slices.Contains(Launchers, name) || IsInterpreter(name)
}

// afterSource loses the shell's place unless the script sourced is read and
// moves nowhere: a sourced script runs in the shell itself.
func (walked *walker) afterSource(args []*syntax.Word) {
	if len(args) < 2 {
		return
	}

	text, why := walked.textOf(args[1], walked.dir)
	if why != "" {
		walked.dir = ""

		return
	}

	file, err := Parse(text)
	if err != nil {
		walked.dir = ""

		return
	}

	syntax.Walk(file, func(node syntax.Node) bool {
		call, isCall := node.(*syntax.CallExpr)
		if isCall && len(call.Args) > 0 && MovesDirectory(spelledWords(call.Args)) {
			walked.dir = ""
		}

		return walked.dir != ""
	})
}

// script reports the script word names: its text, with the interpreter named on
// its first line where that is no shell, or why it goes unread. executed is a script
// run by its path, whose first line picks what reads it; a file that is no
// text is no script.
func (walked *walker) script(word *syntax.Word, dir string, executed bool) {
	name := Spelled(word)

	text, why := walked.textOf(word, dir)
	if why != "" {
		walked.visitor.Unread(name, why)

		return
	}

	if binary(text) {
		return
	}

	inner := walked.scripts.At(dir)
	inner.Depth++

	interpreter, hasShebang := shebang(text)

	switch {
	case !executed || !hasShebang || slices.Contains(scriptShells, interpreter):
		walked.visitor.Script(name, "", text, inner)
	case IsInterpreter(interpreter):
		walked.visitor.Script(name, interpreter, text, inner)
	}
}

// textOf is the text of the script word names, or why it cannot be read.
func (walked *walker) textOf(word *syntax.Word, dir string) (string, string) {
	value, literal := Literal(word)
	if !literal || Patterned(word) {
		return "", UnreadBuilt
	}

	if walked.scripts.Depth >= MaxDepth {
		return "", UnreadDeep
	}

	resolved := walked.resolve(value, dir)
	if resolved == "" {
		return "", UnreadDirectory
	}

	written, held := walked.written[resolved]

	switch {
	case held && written.known:
		return written.text, ""
	case held || walked.writesUnknown:
		return "", UnreadWritten
	}

	return walked.readFile(resolved)
}

func (walked *walker) readFile(name string) (string, string) {
	if walked.scripts.Files == nil {
		return "", UnreadOpen
	}

	opened, err := walked.scripts.Files.Open(name)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", UnreadMissing
	case err != nil:
		return "", UnreadOpen
	}

	defer func() { _ = opened.Close() }()

	info, err := opened.Stat()
	if err != nil || info.IsDir() {
		return "", UnreadOpen
	}

	// The probe comes first, so a binary named by its path costs a few
	// kilobytes read, never the whole limit.
	reader := io.LimitReader(opened, MaxScript+1)
	head := make([]byte, binaryProbe)

	read, err := io.ReadFull(reader, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", UnreadOpen
	}

	head = head[:read]
	if binary(string(head)) {
		return string(head), ""
	}

	rest, err := io.ReadAll(reader)
	if err != nil {
		return "", UnreadOpen
	}

	content := append(head, rest...)
	if len(content) > MaxScript {
		return "", UnreadLarge
	}

	return string(content), ""
}

func binary(text string) bool {
	return bytes.IndexByte([]byte(text[:min(len(text), binaryProbe)]), 0) >= 0
}

// shebang is the base name of the program a script's first line names, past
// env and its options and assignments, and whether the line opens with `#!`.
func shebang(text string) (string, bool) {
	line, isShebang := strings.CutPrefix(text, "#!")
	if !isShebang {
		return "", false
	}

	line, _, _ = strings.Cut(line, "\n")

	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", true
	}

	program := strings.ToLower(path.Base(fields[0]))
	if program != "env" {
		return program, true
	}

	for _, field := range fields[1:] {
		if strings.HasPrefix(field, "-") || assignment.MatchString(field) {
			continue
		}

		return strings.ToLower(path.Base(field)), true
	}

	return "", true
}

// scriptOperand is the index of the script a shell's arguments name, or -1
// where it names none: -s reads standard input, and the operand may name the
// input itself. An option taking a value takes the next word with it, as
// namesScript reads them.
func scriptOperand(args []*syntax.Word) int {
	for index := 0; index < len(args); index++ {
		value, isLiteral := Literal(args[index])

		switch {
		case !isLiteral:
			return index
		case value == "--":
			if index+1 < len(args) && !standardOperand(args[index+1]) {
				return index + 1
			}

			return -1
		case StandardInput(value):
			return -1
		case slices.Contains(longFlags, value):
		case strings.HasPrefix(value, "--"):
			index++
		case strings.HasPrefix(value, "-") || strings.HasPrefix(value, "+"):
			flags := strings.TrimLeft(value, "-+")
			if strings.Contains(flags, "s") {
				return -1
			}

			if strings.ContainsAny(flags, "oOC") {
				index++
			}
		default:
			return index
		}
	}

	return -1
}

func standardOperand(word *syntax.Word) bool {
	value, isLiteral := Literal(word)

	return isLiteral && StandardInput(value)
}
