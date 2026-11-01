package shellwalk_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/shellwalk"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

// echoed is what every echo call of the command hands on, as the walk offers it.
func echoed(t *testing.T, command string) []string {
	t.Helper()

	file, err := shellwalk.Parse(command)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}

	recorder := &test.WalkRecorder{}
	shellwalk.Walk(file, recorder)

	var words []string

	for _, event := range recorder.Events {
		echo, isEcho := strings.CutPrefix(event, "call echo ")
		if isEcho {
			words = append(words, echo)
		}
	}

	return words
}

func TestTheWalkWritesOutTheVariablesTheTextFixes(t *testing.T) {
	cases := []struct {
		name    string
		command string
		echoed  []string
	}{
		{name: "a bare assignment", command: `X=a; echo "$X"`, echoed: []string{"a"}},
		{name: "an unquoted use", command: `X=a/b; echo $X`, echoed: []string{"a/b"}},
		{name: "braces", command: `X=a; echo "${X}/b"`, echoed: []string{"a/b"}},
		{name: "an exported assignment", command: `export X=a; echo "$X"`, echoed: []string{"a"}},
		{name: "a readonly assignment", command: `readonly X=a; echo "$X"`, echoed: []string{"a"}},
		{name: "an assignment opening an and chain", command: `X=a && echo "$X"`, echoed: []string{"a"}},
		{name: "a value joined from fixed variables", command: `A=a; B="$A/b"; C=$B/c; echo "$C"`, echoed: []string{"a/b/c"}},
		{name: "the last assignment before each use", command: `X=a; echo "$X"; X=b; echo "$X"`, echoed: []string{"a", "b"}},
		{name: "quoted text kept as one word", command: `X='a b'; echo "$X"`, echoed: []string{"a b"}},
		{name: "a quoted value holding quoting characters", command: `X='a"$b'; echo "$X"`, echoed: []string{`a"$b`}},
		{name: "an empty value quoted", command: `X=; echo "[$X]"`, echoed: []string{"[]"}},
		{name: "a use before any assignment", command: `echo "$X"; X=a`, echoed: []string{"$X"}},
		{name: "an unquoted value that splits", command: `X='a b'; echo $X`, echoed: []string{"$X"}},
		{name: "an unquoted value that globs", command: `X='*'; echo $X`, echoed: []string{"$X"}},
		{name: "an unquoted value after IFS changes", command: `IFS=/; X=a/b; echo $X`, echoed: []string{"$X"}},
		{name: "an unquoted empty value", command: `X=; echo $X`, echoed: []string{"$X"}},
		{name: "a value holding a tilde", command: `X=~/a; echo "$X"`, echoed: []string{"$X"}},
		{name: "a value holding an escape read only at run time", command: `X=$'\e'; echo "$X"`, echoed: []string{"$X"}},
		{name: "a value holding a backslash", command: `X='a\b'; echo "$X"`, echoed: []string{"$X"}},
		{name: "an assignment behind another command", command: `true && X=a; echo "$X"`, echoed: []string{"$X"}},
		{name: "an assignment in a branch", command: `X=a; if true; then X=b; fi; echo "$X"`, echoed: []string{"$X"}},
		{name: "an assignment in a loop", command: `X=a; for i in 1; do X=b; done; echo "$X"`, echoed: []string{"$X"}},
		{name: "an assignment in a function", command: `X=a; f() { X=b; }; f; echo "$X"`, echoed: []string{"$X"}},
		{name: "a use in a function", command: `X=a; f() { echo "$X"; }; X=b; f`, echoed: []string{"$X"}},
		{name: "an assignment in a subshell", command: `X=a; (X=b); echo "$X"`, echoed: []string{"$X"}},
		{name: "an assignment in a pipeline", command: `X=a; X=b | cat; echo "$X"`, echoed: []string{"$X"}},
		{name: "an assignment for one command", command: `X=a; X=b true; echo "$X"`, echoed: []string{"$X"}},
		{name: "an assignment in the background", command: `X=a; X=b & echo "$X"`, echoed: []string{"$X"}},
		{name: "an assignment with a redirection", command: `X=a; X=b >/dev/null; echo "$X"`, echoed: []string{"$X"}},
		{name: "an array assignment", command: `X=a; X=(b); echo "$X"`, echoed: []string{"$X"}},
		{name: "a value quoting a command substitution", command: `X="a$(pwd)"; echo "$X"`, echoed: []string{"$X"}},
		{name: "a value expanding with an operator", command: `Y=a; X=${Y:-b}; echo "$X"`, echoed: []string{"$X"}},
		{name: "a slice", command: `X=a; : "${Y:0:1}"; echo "$X"`, echoed: []string{"$X"}},
		{name: "a computed subscript", command: `X=a; : "${A[i+1]}"; echo "$X"`, echoed: []string{"$X"}},
		{name: "a plain subscript", command: `X=a; : "${A[0]}"; echo "$X"`, echoed: []string{"a"}},
		{name: "a subscript naming a variable", command: `X=a; : "${A[i]}"; echo "$X"`, echoed: []string{"$X"}},
		{name: "an arithmetic test", command: `X=a; [[ $n -eq 1 ]]; echo "$X"`, echoed: []string{"$X"}},
		{name: "let", command: `X=a; let n=1; echo "$X"`, echoed: []string{"$X"}},
		{name: "a coprocess", command: `X=a; coproc cat; echo "$X"`, echoed: []string{"$X"}},
		{name: "a plain declare", command: `declare X=a; echo "$X"`, echoed: []string{"$X"}},
		{name: "export with an option", command: `export -n X=a; echo "$X"`, echoed: []string{"$X"}},
		{name: "printf given a format built at run time", command: `X=a; printf "$fmt" b; echo "$X"`, echoed: []string{"$X"}},
		{name: "a variable bash names for itself", command: `BASH_ENV=a; echo "$BASH_ENV"`, echoed: []string{"$BASH_ENV"}},
		{name: "an appending assignment", command: `X=a; X+=b; echo "$X"`, echoed: []string{"$X"}},
		{name: "an array element", command: `X=a; X[1]=b; echo "$X"`, echoed: []string{"$X"}},
		{name: "a loop variable", command: `X=a; for X in b; do :; done; echo "$X"`, echoed: []string{"$X"}},
		{name: "read", command: `X=a; read -r X; echo "$X"`, echoed: []string{"$X"}},
		{name: "unset", command: `X=a; unset X; echo "$X"`, echoed: []string{"$X"}},
		{name: "declare with options", command: `declare -x X=a; echo "$X"`, echoed: []string{"$X"}},
		{name: "local", command: `f() { local X=a; }; X=a; echo "$X"`, echoed: []string{"$X"}},
		{name: "a nameref", command: `X=a; declare -n R=X; R=b; echo "$X"`, echoed: []string{"$X"}},
		{name: "eval", command: `X=a; eval 'X=b'; echo "$X"`, echoed: []string{"$X"}},
		{name: "source", command: `X=a; source ./lib.sh; echo "$X"`, echoed: []string{"$X"}},
		{name: "a trap", command: `trap 'X=b' DEBUG; X=a; echo "$X"`, echoed: []string{"$X"}},
		{name: "printf storing its output", command: `X=a; printf -v X b; echo "$X"`, echoed: []string{"$X"}},
		{name: "printf printing", command: `X=a; printf '%s' "$X"; echo "$X"`, echoed: []string{"a"}},
		{name: "a command name built at run time", command: `X=a; $cmd; echo "$X"`, echoed: []string{"$X"}},
		{name: "a default assigned by an expansion", command: `X=a; : "${X:=b}"; echo "$X"`, echoed: []string{"$X"}},
		{name: "arithmetic", command: `X=a; (( Y = 1 )); echo "$X"`, echoed: []string{"$X"}},
		{name: "a descriptor stored in a name", command: `X=a; exec {X}>/dev/null; echo "$X"`, echoed: []string{"$X"}},
		{name: "a command substitution in the value", command: `X=$(pwd); echo "$X"`, echoed: []string{"$X"}},
		{name: "a variable never assigned", command: `echo "$HOME" "$X"`, echoed: []string{"$HOME $X"}},
		{name: "a variable the shell manages", command: `PWD=/a; cd /b; echo "$PWD"`, echoed: []string{"$PWD"}},
		{name: "a joined variable never assigned", command: `X=$Y/a; echo "$X"`, echoed: []string{"$X"}},
		{name: "an expansion with an operator", command: `X=a; echo "${X:-b}" "${X%/*}"`, echoed: []string{"${X:-b} ${X%/*}"}},
		{name: "the shell's name on a command line", command: `echo "$0" "$(dirname "$0")"`, echoed: []string{`$0 $(dirname "$0")`}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := echoed(t, tc.command)
			if !slices.Equal(got, tc.echoed) {
				t.Errorf("a variable should be written out only where the text fixes it at the use, got: %q", got)
			}
		})
	}
}

func TestCodeElsewhereRunsAsNoScript(t *testing.T) {
	within := shellwalk.Scripts{Dir: "/a", Program: "scripts/x.sh", Bash: true}.At("/b")

	if within.Program != "" || within.Bash || within.Dir != "/b" {
		t.Errorf("code run in another directory should keep no script's $0, got: %+v", within)
	}
}

func TestACdIntoTheScriptsFolderIsFollowed(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolving the test root: %v", err)
	}

	test.FixtureTree(t, "shellguard/resolution", root)

	cases := []struct {
		name    string
		command string
		dir     string
	}{
		{name: "dirname of the script", command: "sh scripts/folder-dirname.sh", dir: root},
		{name: "dirname of BASH_SOURCE in a script bash runs", command: "bash scripts/folder-bash-source.sh", dir: root},
		{name: "dirname of BASH_SOURCE in an executed bash script", command: "./scripts/folder-bash-source.sh", dir: root},
		{name: "a suffix cut of the script's path", command: "sh scripts/folder-cut.sh", dir: root},
		{name: "cd and pwd in a command substitution", command: "sh scripts/folder-pwd.sh", dir: root},
		{name: "a guarded cd", command: "sh scripts/folder-guarded.sh", dir: root},
		{name: "a script named from its own folder", command: "cd scripts && sh ./folder-dirname.sh", dir: root},
		{name: "a script named by its bare name", command: "cd scripts && sh folder-dirname.sh", dir: root},
		{name: "a script named by its absolute path", command: "sh " + filepath.Join(root, "scripts", "folder-pwd.sh"), dir: root},
		{name: "an unquoted dirname of the script", command: "sh scripts/folder-unquoted.sh", dir: root},
		{name: "an unquoted dirname after IFS changes", command: "sh scripts/folder-unquoted-split.sh"},
		{name: "an unquoted dirname that splits", command: `sh "spaced folder/folder-unquoted.sh"`},
		{name: "a script named from a sibling folder", command: `cd "spaced folder" && sh ../scripts/folder-dirname.sh`, dir: root},
		{name: "a folder joined with a variable", command: "sh scripts/folder-built.sh"},
		{name: "BASH_SOURCE the script assigns", command: "bash scripts/folder-bash-source-assigned.sh"},
		{name: "BASH_SOURCE of the caller", command: "bash scripts/folder-bash-source-caller.sh"},
		{name: "a suffix cut of a path holding no slash", command: "cd scripts && sh folder-cut.sh"},
		{name: "cd and something else than pwd", command: "sh scripts/folder-not-pwd.sh"},
		{name: "cd or pwd", command: "sh scripts/folder-or-pwd.sh"},
		{name: "basename of the script", command: "sh scripts/folder-basename.sh"},
		{name: "dirname of an argument", command: "sh scripts/folder-argument.sh"},
		{name: "a suffix cut of an argument", command: "sh scripts/folder-argument-cut.sh"},
		{name: "a longest suffix cut", command: "sh scripts/folder-long-cut.sh"},
		{name: "a suffix cut of another pattern", command: "sh scripts/folder-other-cut.sh"},
		{name: "dirname of an unquoted $0", command: "sh scripts/folder-unquoted-name.sh"},
		{name: "a substitution running two commands", command: "sh scripts/folder-two-commands.sh"},
		{name: "dirname given two operands", command: "sh scripts/folder-dirname-two.sh"},
		{name: "dirname of $0 cut", command: "sh scripts/folder-dirname-cut.sh"},
		{name: "another command than cd before pwd", command: "bash scripts/folder-not-cd.sh"},
		{name: "dirname printing elsewhere", command: "sh scripts/folder-silenced.sh"},
		{name: "pwd printing elsewhere", command: "sh scripts/folder-pwd-silenced.sh"},
		{name: "pwd given an option", command: "sh scripts/folder-pwd-operand.sh"},
		{name: "cd given two operands", command: "sh scripts/folder-cd-two.sh"},
		{name: "BASH_SOURCE in a script sh runs", command: "sh scripts/folder-bash-source.sh"},
		{name: "a sourced script", command: "source scripts/folder-dirname.sh"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file, err := shellwalk.Parse(tc.command)
			if err != nil {
				t.Fatalf("parsing: %v", err)
			}

			places := &test.RemovalPlaces{}
			shellwalk.WalkWith(file, places, shellwalk.Scripts{Files: test.TreeFiles{Roots: []string{root}}, Dir: root})

			if !slices.Equal(places.Dirs, []string{tc.dir}) {
				t.Errorf("the script's removal should run where its cd took it, got %q", places.Dirs)
			}
		})
	}
}
