package shellwalk_test

import (
	"slices"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"

	"github.com/fza/agentic-helpers/source/hooks/internal/shellwalk"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestTheWalkOffersEveryCommandItRuns(t *testing.T) {
	cases := []struct {
		name    string
		command string
		events  []string
	}{
		{name: "a plain call", command: "sdd info", events: []string{"call sdd info"}},
		{name: "a subshell and a substitution", command: "(ls) && echo \"$(pwd)\"", events: []string{"call ls", "call echo $(pwd)", "call pwd"}},
		{name: "a keyword is no call", command: "time sdd info", events: []string{"call sdd info"}},
		{
			name: "env past its assignments", command: "env X=1 sdd info",
			events: []string{"call env X=1 sdd info", "launched X=1 sdd info", "launched sdd info (certain)", "launched info"},
		},
		{
			name: "xargs past its options and counts", command: "xargs -n 1 sdd",
			events: []string{"call xargs -n 1 sdd", "launched -n 1 sdd", "launched 1 sdd", "launched sdd (certain)"},
		},
		{
			name: "a launcher a launcher runs", command: "nohup env sdd",
			events: []string{"call nohup env sdd", "launched env sdd (certain)", "launched sdd (certain)"},
		},
		{
			name: "past the end of options", command: "sudo -- -x",
			events: []string{"call sudo -- -x", "launched -- -x", "launched -x (certain)"},
		},
		{
			name: "nothing past the end of options", command: "sudo -- ",
			events: []string{"call sudo --", "launched --"},
		},
		{
			name: "find's -exec", command: `find . -exec rm {} \;`,
			events: []string{"call find . -exec rm {} ;", "launched . -exec rm {} ;", "launched -exec rm {} ;", "launched rm {} ; (certain)", "launched {} ;", "launched ;"},
		},
		{
			name: "find past an expansion", command: `find "$dir" -exec rm {} \;`,
			events: []string{`call find $dir -exec rm {} ;`, "launched $dir -exec rm {} ;", "launched -exec rm {} ;", "launched rm {} ; (certain)", "launched {} ;", "launched ;"},
		},
		{
			name: "an expansion as the command", command: `env "$tool" x`,
			events: []string{"call env $tool x", "launched $tool x (certain)", "launched x"},
		},
		{
			name: "find with nothing to run", command: "find . -name x",
			events: []string{"call find . -name x", "launched . -name x", "launched -name x", "launched x"},
		},
		{
			name: "past an option's value", command: "env -C /work sdd",
			events: []string{"call env -C /work sdd", "launched -C /work sdd", "launched /work sdd", "launched sdd (certain)"},
		},
		{
			name: "an option's value is another launcher's own", command: "sudo -u bob sdd",
			events: []string{"call sudo -u bob sdd", "launched -u bob sdd", "launched bob sdd", "launched sdd (certain)"},
		},
		{
			name: "a lookup runs nothing", command: "command -v sdd",
			events: []string{"call command -v sdd", "launched -v sdd", "launched sdd"},
		},
		{name: "a shell's -c", command: "bash -c 'sdd new'", events: []string{"call bash -c sdd new", "code bash -c: sdd new"}},
		{name: "eval", command: "eval sdd new", events: []string{"call eval sdd new", "code eval: sdd new"}},
		{name: "a trap handler", command: "trap 'rm x' EXIT", events: []string{"call trap rm x EXIT", "code trap: rm x"}},
		{name: "a heredoc fed to a shell", command: "bash <<'EOF'\nsdd new\nEOF", events: []string{"code bash stdin: sdd new\n", "call bash"}},
		{name: "a heredoc fed to an interpreter", command: "python3 <<'EOF'\nx\nEOF", events: []string{"inline python3: x\n", "call python3", "inline python3: "}},
		{name: "a heredoc fed to anything else", command: "cat <<'EOF'\nsdd new\nEOF", events: []string{"call cat"}},
		{name: "code piped to a shell", command: "echo x | sh", events: []string{"piped sh", "call echo x", "call sh"}},
		{name: "a script piped into", command: "echo x | sh run.sh", events: []string{"call echo x", "call sh run.sh"}},
		{name: "inline code", command: "perl -e 'unlink 1'", events: []string{"call perl -e unlink 1", "inline perl: -e unlink 1"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file, err := shellwalk.Parse(tc.command)
			if err != nil {
				t.Fatalf("parsing: %v", err)
			}

			recorder := &test.WalkRecorder{}
			shellwalk.Walk(file, recorder)

			if !slices.Equal(recorder.Events, tc.events) {
				t.Errorf("the walk should offer each command it runs, in order, got: %q", recorder.Events)
			}
		})
	}
}

func TestSpelledRemovesQuotingAndKeepsExpansions(t *testing.T) {
	cases := []struct {
		word string
		want string
	}{
		{word: "plain", want: "plain"},
		{word: `'single quoted'`, want: "single quoted"},
		{word: `$'s\x64d'`, want: "sdd"},
		{word: `$'s\cdd'`, want: `$'s\cdd'`},
		{word: `"s\dd"`, want: "sdd"},
		{word: `"$HOME/bin/sdd"`, want: "$HOME/bin/sdd"},
		{word: `a\ b`, want: "a b"},
		{word: `"x ${HOME}"`, want: "x ${HOME}"},
	}

	for _, tc := range cases {
		t.Run(tc.word, func(t *testing.T) {
			file, err := shellwalk.Parse("echo " + tc.word)
			if err != nil {
				t.Fatalf("parsing: %v", err)
			}

			recorder := &test.WalkRecorder{}
			shellwalk.Walk(file, recorder)

			if !slices.Equal(recorder.Events, []string{"call echo " + tc.want}) {
				t.Errorf("the word should read with its quoting removed, got: %q", recorder.Events)
			}
		})
	}
}

func TestWhatMovesToAnotherDirectory(t *testing.T) {
	cases := []struct {
		words []string
		moves bool
	}{
		{words: []string{"cd", "sub"}, moves: true},
		{words: []string{"pushd", "sub"}, moves: true},
		{words: []string{"popd"}, moves: true},
		{words: []string{"/usr/bin/env", "-C", "sub", "sdd"}, moves: true},
		{words: []string{"env", "--chdir=sub", "sdd"}, moves: true},
		{words: []string{"env", "--chdir", "sub", "sdd"}, moves: true},
		{words: []string{"env", "-iC", "sub", "sdd"}, moves: true},
		{words: []string{"sudo", "-D", "sub", "sdd"}, moves: true},
		{words: []string{"sudo", "-iu", "bob", "sdd"}, moves: true},
		{words: []string{"sudo", "--login", "sdd"}, moves: true},
		{words: []string{"env", "-u", "HOME", "sdd"}},
		{words: []string{"sudo", "-u", "bob", "sdd"}},
		{words: []string{"nice", "-n", "5", "sdd"}},
		{words: []string{"git", "-C", "sub", "status"}},
		{words: []string{"sudo", "--list", "sdd"}},
		{words: []string{"sdd", "new"}},
		{},
	}

	for _, tc := range cases {
		t.Run(strings.Join(tc.words, " "), func(t *testing.T) {
			if shellwalk.MovesDirectory(tc.words) != tc.moves {
				t.Error("a call should move only where it or its launcher's option changes directory")
			}
		})
	}
}

func TestLiteralRefusesWhatItCannotRead(t *testing.T) {
	cases := []struct {
		word      string
		value     string
		isLiteral bool
	}{
		{word: `"r\m"`, value: "rm", isLiteral: true},
		{word: `'a b'`, value: "a b", isLiteral: true},
		{word: `$'rm'`, value: "rm", isLiteral: true},
		{word: `$'\x72m'`, value: "rm", isLiteral: true},
		{word: `$'\x72\x6D'`, value: "rm", isLiteral: true},
		{word: `$'\x414'`, value: "A4", isLiteral: true},
		{word: `$'a\nb\tc\rd'`, value: "a\nb\tc\rd", isLiteral: true},
		{word: `$'it\'s \"q\" \\'`, value: `it's "q" \`, isLiteral: true},
		{word: `"/"$'r'm`, value: "/rm", isLiteral: true},
		{word: `$'\162m'`},
		{word: `$'\18'`},
		{word: `$'\9'`},
		{word: `$'r\0m'`},
		{word: `$'\400'`},
		{word: `$'\x4'`},
		{word: `$'\x4g'`},
		{word: `$'rm\x00x'`},
		{word: `$'r\cm'`},
		{word: `$'\e'`},
		{word: `$'\q'`},
		{word: "$'\\" + "u0072m'"},
		{word: "$'\\" + "U00000072m'"},
		{word: `'r\cm'`, value: `r\cm`, isLiteral: true},
		{word: `$HOME`},
		{word: `"$HOME"`},
	}

	for _, tc := range cases {
		t.Run(tc.word, func(t *testing.T) {
			file, err := shellwalk.Parse("echo " + tc.word)
			if err != nil {
				t.Fatalf("parsing: %v", err)
			}

			call, isCall := file.Stmts[0].Cmd.(*syntax.CallExpr)
			if !isCall {
				t.Fatal("the command should parse as a call")
			}

			value, isLiteral := shellwalk.Literal(call.Args[1])
			if value != tc.value || isLiteral != tc.isLiteral {
				t.Errorf("a word should read as a literal only without expansions, got: %q %v", value, isLiteral)
			}
		})
	}
}

func TestPatternedFindsWhatTheShellExpands(t *testing.T) {
	cases := []struct {
		word      string
		patterned bool
	}{
		{word: "run*.log", patterned: true},
		{word: "ru?.log", patterned: true},
		{word: "run[.]log", patterned: true},
		{word: "run[.log", patterned: true},
		{word: "run.{log,txt}", patterned: true},
		{word: "run{1..3}.log", patterned: true},
		{word: "@(run).log", patterned: true},
		{word: `"run"*`, patterned: true},
		{word: "run.log"},
		{word: `'run*.{log,txt}'`},
		{word: `"run?[x].{a,b}"`},
		{word: `run\*.\{log,txt\}`},
		{word: `$'run*'`},
		{word: "run{.log"},
		{word: "run{log}"},
		{word: `"$dir"/run.log`},
	}

	for _, tc := range cases {
		t.Run(tc.word, func(t *testing.T) {
			file, err := shellwalk.Parse("echo " + tc.word)
			if err != nil {
				t.Fatalf("parsing: %v", err)
			}

			call, isCall := file.Stmts[0].Cmd.(*syntax.CallExpr)
			if !isCall {
				t.Fatal("the command should parse as a call")
			}

			if shellwalk.Patterned(call.Args[1]) != tc.patterned {
				t.Error("a word should be patterned only where an unquoted glob or brace leaves it to the shell")
			}
		})
	}
}
