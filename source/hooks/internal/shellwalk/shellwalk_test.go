package shellwalk_test

import (
	"path/filepath"
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
			name: "an option's quoted value", command: `nice -n "$n" sdd`,
			events: []string{"call nice -n $n sdd", "launched -n $n sdd", "launched $n sdd", "launched sdd (certain)"},
		},
		{
			name: "an option's value that may split", command: "nice -n $n sdd",
			events: []string{"call nice -n $n sdd", "launched -n $n sdd", "launched $n sdd (certain)", "launched sdd"},
		},
		{
			name: "an option's quoted value that splits", command: `sudo -u "${a[@]}" sdd`,
			events: []string{"call sudo -u ${a[@]} sdd", "launched -u ${a[@]} sdd", "launched ${a[@]} sdd (certain)", "launched sdd"},
		},
		{
			name: "an option's value that is a brace", command: "sudo -u {root,rm} sdd",
			events: []string{"call sudo -u {root,rm} sdd", "launched -u {root,rm} sdd", "launched {root,rm} sdd (certain)", "launched sdd"},
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
		{name: "code piped to a shell in a group", command: "echo x | { sh; }", events: []string{"piped sh", "call echo x", "call sh"}},
		{name: "code piped to a shell reading a file that may be a link", command: "echo x | { sh < run.sh; }", events: []string{"piped sh", "call echo x", "call sh"}},
		{name: "code piped past a shell fed a here-string", command: "echo x | { sh <<< 'echo hi'; }", events: []string{"call echo x", "code sh stdin: echo hi", "call sh"}},
		{name: "code piped to a shell fed a here-string and a duplicate", command: "echo x | { sh <<< 'echo hi' 0<&3; }", events: []string{"piped sh", "call echo x", "code sh stdin: echo hi", "call sh"}},
		{name: "code piped to a shell fed a here-string and a duplicate by an output operator", command: "echo x | { sh <<< 'echo hi' 0>&3; }", events: []string{"piped sh", "call echo x", "code sh stdin: echo hi", "call sh"}},
		{name: "code piped to a shell fed a here-string and a duplicate on a padded descriptor", command: "echo x | { sh 3<&0 <<< 'echo hi' 00<&3; }", events: []string{"piped sh", "call echo x", "code sh stdin: echo hi", "call sh"}},
		{name: "code piped to a shell fed a here-string on a named descriptor", command: "echo x | { sh {fd}<<< 'echo hi'; }", events: []string{"piped sh", "call echo x", "code sh stdin: echo hi", "call sh"}},
		{name: "code piped past a shell fed a here-string with its output redirected", command: "echo x | { sh <<< 'echo hi' &> log; }", events: []string{"call echo x", "code sh stdin: echo hi", "call sh"}},
		{name: "code piped past a shell fed a here-string reading a file on another descriptor", command: "echo x | { sh <<< 'echo hi' 3< extra; }", events: []string{"call echo x", "code sh stdin: echo hi", "call sh"}},
		{name: "code piped past a shell fed a here-document on another descriptor", command: "echo x | { sh <<< 'echo hi' 3<<< 'y'; }", events: []string{"call echo x", "code sh stdin: echo hi", "code sh stdin: y", "call sh"}},
		{name: "code piped to a shell fed a here-string and the input", command: "echo x | { sh < /dev/stdin <<< 'echo hi'; }", events: []string{"piped sh", "call echo x", "code sh stdin: echo hi", "call sh"}},
		{name: "code piped to a shell redirected to the input again", command: "echo x | { sh < /dev/stdin; }", events: []string{"piped sh", "call echo x", "call sh"}},
		{name: "code piped to a shell redirected to an uncleaned /dev/stdin", command: "echo x | { sh < //dev/./stdin; }", events: []string{"piped sh", "call echo x", "call sh"}},
		{name: "code piped to a shell redirected to descriptor 0", command: "echo x | { sh < /dev/fd/0; }", events: []string{"piped sh", "call echo x", "call sh"}},
		{name: "code piped to a shell redirected to its own descriptor 0", command: "echo x | { sh < /proc/self/fd/0; }", events: []string{"piped sh", "call echo x", "call sh"}},
		{name: "code piped to a shell redirected to a duplicate", command: "echo x | { sh 0<&0; }", events: []string{"piped sh", "call echo x", "call sh"}},
		{name: "code piped to a shell redirected to a built path", command: `echo x | { sh < "$f"; }`, events: []string{"piped sh", "call echo x", "call sh"}},
		{name: "code piped to a shell whose last redirection is a duplicate", command: "echo x | { sh < run.sh 0<&3; }", events: []string{"piped sh", "call echo x", "call sh"}},
		{name: "code piped to a shell reading its input by path", command: "echo x | sh //dev/./stdin", events: []string{"piped sh", "call echo x", "call sh //dev/./stdin"}},
		{
			name: "xargs past BSD's replace count", command: "xargs -R x sdd",
			events: []string{"call xargs -R x sdd", "launched -R x sdd", "launched x sdd", "launched sdd (certain)"},
		},
		{
			name: "xargs past BSD's replace string", command: "xargs -J x sdd",
			events: []string{"call xargs -J x sdd", "launched -J x sdd", "launched x sdd", "launched sdd (certain)"},
		},
		{name: "zsh's noglob", command: "noglob sdd", events: []string{"call noglob sdd", "launched sdd (certain)"}},
		{name: "zsh's repeat past its count", command: "repeat 2 sdd", events: []string{"call repeat 2 sdd", "launched 2 sdd", "launched sdd (certain)"}},
		{name: "zsh's equals expansion", command: "=nohup sdd", events: []string{"call =nohup sdd", "launched sdd (certain)"}},
		{name: "inline code", command: "perl -e 'unlink 1'", events: []string{"call perl -e unlink 1", "inline perl: -e unlink 1"}},
		{
			name: "gtimeout past its options and duration", command: "gtimeout -k 5 9 sdd info",
			events: []string{"call gtimeout -k 5 9 sdd info", "launched -k 5 9 sdd info", "launched 5 9 sdd info", "launched 9 sdd info", "launched sdd info (certain)", "launched info"},
		},
		{
			name: "uu-timeout past its duration", command: "uu-timeout 9 sdd",
			events: []string{"call uu-timeout 9 sdd", "launched 9 sdd", "launched sdd (certain)"},
		},
		{
			name: "the perl alarm form as a launcher", command: "perl -e 'alarm 9; exec @ARGV' sdd info",
			events: []string{"call perl -e alarm 9; exec @ARGV sdd info", "launched -e alarm 9; exec @ARGV sdd info", "launched alarm 9; exec @ARGV sdd info", "launched sdd info (certain)", "launched info"},
		},
		{
			name: "the perl alarm form under a launcher", command: "nohup perl -e 'alarm 9; exec @ARGV' sdd",
			events: []string{"call nohup perl -e alarm 9; exec @ARGV sdd", "launched perl -e alarm 9; exec @ARGV sdd (certain)", "launched -e alarm 9; exec @ARGV sdd", "launched alarm 9; exec @ARGV sdd", "launched sdd (certain)"},
		},
		{
			name: "perl options after the alarm form stay inline", command: "perl -e 'alarm 9; exec @ARGV' -e 'print 1' sdd",
			events: []string{"call perl -e alarm 9; exec @ARGV -e print 1 sdd", "inline perl: -e alarm 9; exec @ARGV -e print 1 sdd"},
		},
		{
			name: "one word perl hands to the shell stays inline", command: "perl -e 'alarm 9; exec @ARGV' 'sdd info'",
			events: []string{"call perl -e alarm 9; exec @ARGV sdd info", "inline perl: -e alarm 9; exec @ARGV sdd info"},
		},
		{
			name: "a word that may vanish after the alarm form stays inline", command: "perl -e 'alarm 9; exec @ARGV' sdd $empty",
			events: []string{"call perl -e alarm 9; exec @ARGV sdd $empty", "inline perl: -e alarm 9; exec @ARGV sdd $empty"},
		},
		{
			name: "a double-quoted expansion after the alarm form launches", command: `perl -e 'alarm 9; exec @ARGV' sdd "$x"`,
			events: []string{`call perl -e alarm 9; exec @ARGV sdd $x`, `launched -e alarm 9; exec @ARGV sdd $x`, `launched alarm 9; exec @ARGV sdd $x`, `launched sdd $x (certain)`, `launched $x`},
		},
		{
			name: "other perl code stays inline", command: "perl -e 'alarm 9; system @ARGV' sdd",
			events: []string{"call perl -e alarm 9; system @ARGV sdd", "inline perl: -e alarm 9; system @ARGV sdd"},
		},
		{name: "code piped to a shell under the perl alarm form", command: "echo x | perl -e 'alarm 9; exec @ARGV' sh", events: []string{"piped sh", "call echo x", "call perl -e alarm 9; exec @ARGV sh", "launched -e alarm 9; exec @ARGV sh", "launched alarm 9; exec @ARGV sh", "launched sh (certain)"}},
		{name: "a zsh function body set by assignment", command: "functions[ls]='rm x'; ls", events: []string{"code functions: rm x", "call ls"}},
		{name: "a zsh alias set by assignment", command: "aliases[ll]='rm x'", events: []string{"code aliases: rm x"}},
		{name: "zsh function bodies set as an array", command: "functions=(f 'rm x')", events: []string{"code functions: f", "code functions: rm x"}},
		{name: "a zsh function body set by typeset", command: "typeset functions[f]='rm x'", events: []string{"code functions: rm x"}},
		{name: "another array's element is no code", command: "names[ls]='rm x'", events: nil},
		{name: "arithmetic on a variable holding text", command: "x='a[$(rm x)]'; (( x ))", events: []string{"code arithmetic on x: (( a[$(rm x)] ))"}},
		{name: "arithmetic expansion on a variable holding text", command: "x='a[$(rm x)]'; echo $(( x + 1 ))", events: []string{"call echo $((x + 1))", "code arithmetic on x: (( a[$(rm x)] ))"}},
		{name: "arithmetic through a variable naming another", command: "y=x; x='a[$(rm x)]'; (( $y ))", events: []string{"code arithmetic on y: (( x ))", "code arithmetic on x: (( a[$(rm x)] ))"}},
		{name: "arithmetic through a variable expanding another", command: `y="$x"; x='a[$(rm x)]'; (( y ))`, events: []string{"code arithmetic on x: (( a[$(rm x)] ))"}},
		{name: "arithmetic on a variable a command sets", command: "n=$(wc -l); (( n > 1 ))", events: []string{"call wc -l", "unseen n"}},
		{name: "arithmetic on a variable read sets", command: "read -r -p 'n? ' n; (( n > 1 ))", events: []string{"call read -r -p n?  n", "unseen n"}},
		{name: "arithmetic on an array read sets", command: "read -a arr; (( arr ))", events: []string{"call read -a arr", "unseen arr"}},
		{name: "read's option value is no variable", command: "x=1; read -d x n; (( x ))", events: []string{"call read -d x n"}},
		{name: "arithmetic on an array mapfile sets", command: "mapfile -t -u 3 lines; (( lines ))", events: []string{"call mapfile -t -u 3 lines", "unseen lines"}},
		{name: "arithmetic on a variable printf sets", command: `printf -v n %s "$1"; (( n ))`, events: []string{"call printf -v n %s $1", "unseen n"}},
		{name: "arithmetic on a loop variable over a substitution", command: "for i in $(seq 3); do (( i > 1 )); done", events: []string{"call seq 3", "unseen i"}},
		{name: "arithmetic on a loop variable over the arguments", command: "for i; do (( i > 1 )); done", events: []string{"unseen i"}},
		{name: "arithmetic on a loop variable over a brace", command: "for i in {1..3}; do (( i > 1 )); done", events: []string{"unseen i"}},
		{name: "arithmetic on a loop variable over numbers", command: "for i in 1 2; do (( i > 1 )); done", events: nil},
		{name: "a loop variable holding text", command: "for i in 'a[$(rm x)]'; do (( i )); done", events: []string{"code arithmetic on i: (( a[$(rm x)] ))"}},
		{name: "arithmetic in a test on a variable holding text", command: "x='a[$(rm x)]'; [[ x -eq 1 ]]", events: []string{"code arithmetic on x: (( a[$(rm x)] ))"}},
		{name: "let on a variable holding text", command: "x='a[$(rm x)]'; let x", events: []string{"code arithmetic on x: (( a[$(rm x)] ))"}},
		{name: "a C-style loop on a variable holding text", command: "x='a[$(rm x)]'; for (( ; x; )); do :; done", events: []string{"code arithmetic on x: (( a[$(rm x)] ))", "call :"}},
		{name: "arithmetic on variables naming each other", command: "x=y; y=x; (( x ))", events: []string{"code arithmetic on x: (( y ))", "code arithmetic on y: (( x ))"}},
		{name: "arithmetic on an array holding text", command: "x=('a[$(rm x)]'); (( x ))", events: []string{"code arithmetic on x: (( a[$(rm x)] ))"}},
		{name: "a name inside a substitution in arithmetic is no variable", command: "y=text; (( $(echo y) ))", events: []string{"call echo y"}},
		{name: "arithmetic on a number", command: "i=0; (( i++ ))", events: nil},
		{name: "arithmetic on an arithmetic result", command: "i=$((i + 1)); (( i < 5 ))", events: nil},
		{name: "arithmetic on a variable the command never sets", command: "(( $# > 1 ))", events: nil},
		{
			name: "xcrun past its value options", command: "xcrun --sdk macosx sdd new",
			events: []string{"call xcrun --sdk macosx sdd new", "launched --sdk macosx sdd new", "launched macosx sdd new", "launched sdd new (certain)", "launched new"},
		},
		{name: "xcrun finding a tool runs nothing", command: "xcrun -f sdd", events: []string{"call xcrun -f sdd", "launched -f sdd", "launched sdd"}},
		{
			name: "sandbox-exec past its profile", command: "sandbox-exec -p '(version 1)' sdd",
			events: []string{"call sandbox-exec -p (version 1) sdd", "launched -p (version 1) sdd", "launched (version 1) sdd", "launched sdd (certain)"},
		},
		{
			name: "launchctl submit past its options", command: "launchctl submit -l job -- sdd",
			events: []string{"call launchctl submit -l job -- sdd", "launched submit -l job -- sdd", "launched -l job -- sdd", "launched job -- sdd", "launched -- sdd", "launched sdd (certain)"},
		},
		{name: "another launchctl subcommand runs nothing", command: "launchctl list sdd", events: []string{"call launchctl list sdd"}},
		{
			name: "uv run past its options", command: "uv run --with x sdd",
			events: []string{"call uv run --with x sdd", "launched run --with x sdd", "launched --with x sdd", "launched x sdd", "launched sdd (certain)"},
		},
		{
			name: "npx past its package", command: "npx -p pkg sdd",
			events: []string{"call npx -p pkg sdd", "launched -p pkg sdd", "launched pkg sdd", "launched sdd (certain)"},
		},
		{
			name: "npx running a command string", command: "npx -c 'rm x'",
			events: []string{"call npx -c rm x", "code npx -c: rm x", "launched -c rm x", "launched rm x"},
		},
		{
			name: "pnpm exec past its options", command: "pnpm exec -C d sdd",
			events: []string{"call pnpm exec -C d sdd", "launched exec -C d sdd", "launched -C d sdd", "launched d sdd", "launched sdd (certain)"},
		},
		{
			name: "pnpm exec in shell mode", command: "pnpm exec -c 'rm x'",
			events: []string{"call pnpm exec -c rm x", "code pnpm exec -c: rm x", "launched exec -c rm x", "launched -c rm x", "launched rm x (certain)"},
		},
		{
			name: "pnpm exec past pnpm's own options", command: "pnpm -r exec sdd",
			events: []string{"call pnpm -r exec sdd", "launched -r exec sdd", "launched exec sdd", "launched sdd (certain)"},
		},
		{
			name: "pnpm exec past pnpm's own option value", command: "pnpm -C sub exec sdd",
			events: []string{"call pnpm -C sub exec sdd", "launched -C sub exec sdd", "launched sub exec sdd", "launched exec sdd", "launched sdd (certain)"},
		},
		{name: "zsh's sched past its -o flag", command: "sched -o +1 rm x", events: []string{"call sched -o +1 rm x", "code sched: rm x"}},
		{name: "zsh's sched removing an item", command: "sched -2 rm x", events: []string{"call sched -2 rm x"}},
		{name: "zsh's sched past its time", command: "sched +1:00 rm x", events: []string{"call sched +1:00 rm x", "code sched: rm x"}},
		{name: "zsh's sched listing", command: "sched", events: []string{"call sched"}},
		{name: "a heredoc fed to at", command: "at now <<'EOF'\nrm x\nEOF", events: []string{"code at stdin: rm x\n", "call at now"}},
		{name: "a here-string fed to batch", command: "batch <<< 'rm x'", events: []string{"code batch stdin: rm x", "call batch"}},
		{name: "code piped to at", command: "echo x | at now", events: []string{"piped at", "call echo x", "call at now"}},
		{name: "code piped to at under a launcher", command: "echo x | sudo at -m now", events: []string{"piped at", "call echo x", "call sudo at -m now", "launched at -m now (certain)", "launched -m now", "launched now"}},
		{name: "a job file for at", command: "echo x | at -f job now", events: []string{"call echo x", "call at -f job now"}},
		{name: "at listing its jobs", command: "echo x | at -l", events: []string{"call echo x", "call at -l"}},
		{name: "a job file for at past its queue", command: "echo x | at -q b -f job now", events: []string{"call echo x", "call at -q b -f job now"}},
		{name: "code piped to at past its time", command: "echo x | at now -f job", events: []string{"piped at", "call echo x", "call at now -f job"}},
		{name: "tmux new-session's command", command: "tmux new-session -d -s s 'rm x'", events: []string{"call tmux new-session -d -s s rm x", "code tmux new-session: rm x"}},
		{name: "tmux's alias and words", command: "tmux -L sock neww -t w rm x", events: []string{"call tmux -L sock neww -t w rm x", "code tmux neww: rm x"}},
		{name: "tmux split-window by prefix", command: "tmux split -l 5 'rm x'", events: []string{"call tmux split -l 5 rm x", "code tmux split: rm x"}},
		{name: "tmux respawn-pane", command: "tmux respawn-pane -k 'rm x'", events: []string{"call tmux respawn-pane -k rm x", "code tmux respawn-pane: rm x"}},
		{name: "tmux run-shell", command: "tmux run-shell -b 'rm x'", events: []string{"call tmux run-shell -b rm x", "code tmux run-shell: rm x"}},
		{name: "tmux commands chained", command: `tmux new -d \; neww 'rm x'`, events: []string{"call tmux new -d ; neww rm x", "code tmux neww: rm x"}},
		{name: "tmux's own -c", command: "tmux -c 'rm x'", events: []string{"call tmux -c rm x", "code tmux -c: rm x"}},
		{name: "another tmux command", command: "tmux ls", events: []string{"call tmux ls"}},
		{
			name: "screen past its options", command: "screen -dmS s sdd",
			events: []string{"call screen -dmS s sdd", "launched -dmS s sdd", "launched s sdd", "launched sdd (certain)"},
		},
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

func TestTheWalkReadsTheScriptsACommandRuns(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolving the test root: %v", err)
	}

	test.FixtureTreeReplacing(t, "shellguard/scripts", root, strings.NewReplacer("{outside}", "/elsewhere"))
	test.WriteFile(t, filepath.Join(root, "sub", "build.sh"), test.Fixture(t, "shellguard/scripts/build.sh"))
	test.WriteFile(t, filepath.Join(root, "tool"), "\x7fELF\x00\x00binary")

	python := strings.TrimSpace(test.Fixture(t, "shellguard/scripts/tidy.py"))

	cases := []struct {
		name    string
		command string
		depth   int
		events  []string
	}{
		{
			name: "a script a shell runs", command: "bash build.sh",
			events: []string{"call bash build.sh in {root}", "script build.sh: #!/bin/sh\necho building in {root}"},
		},
		{
			name: "a script past the end of a shell's options", command: "sh -e -- build.sh",
			events: []string{"call sh -e -- build.sh in {root}", "script build.sh: #!/bin/sh\necho building in {root}"},
		},
		{name: "a shell reading its standard input", command: "bash -s build.sh", events: []string{"call bash -s build.sh in {root}"}},
		{name: "a shell given code", command: "bash -c 'echo hi' build.sh", events: []string{"call bash -c echo hi build.sh in {root}", "code bash -c: echo hi in {root}"}},
		{
			name: "a sourced script", command: ". activate.sh",
			events: []string{"call . activate.sh in {root}", "script activate.sh: export BUILD_MODE=release in {root}"},
		},
		{
			name: "a script after a cd the walk follows", command: "cd sub && bash build.sh",
			events: []string{"call cd sub in {root}", "call bash build.sh in {root}/sub", "script build.sh: #!/bin/sh\necho building in {root}/sub"},
		},
		{
			name: "a script after a cd behind another command", command: "ls && cd sub; bash build.sh",
			events: []string{"call ls in {root}", "call cd sub in {root}", "call bash build.sh", "unread build.sh: the directory it runs in is not known"},
		},
		{
			name: "a script after a cd to a missing directory", command: "cd none; bash build.sh",
			events: []string{"call cd none in {root}", "call bash build.sh", "unread build.sh: the directory it runs in is not known"},
		},
		{
			name: "a script a launcher runs elsewhere", command: "env -C sub bash build.sh",
			events: []string{
				"call env -C sub bash build.sh in {root}", "launched -C sub bash build.sh", "launched sub bash build.sh",
				"launched bash build.sh (certain)", "launched build.sh", "unread build.sh: the directory it runs in is not known",
			},
		},
		{
			name: "a script after a sourced script moves", command: "source leave.sh; bash build.sh",
			events: []string{
				"call source leave.sh in {root}", "script leave.sh: cd /elsewhere in {root}", "call bash build.sh",
				"unread build.sh: the directory it runs in is not known",
			},
		},
		{name: "a script executed with a python shebang", command: "./tidy.py", events: []string{"call ./tidy.py in {root}", "script ./tidy.py by python3: " + python + " in {root}"}},
		{name: "a script executed with another interpreter", command: "./Makefile.run", events: []string{"call ./Makefile.run in {root}"}},
		{name: "an executed binary", command: "./tool", events: []string{"call ./tool in {root}"}},
		{name: "a command by a path the walk knows", command: "/bin/bash build.sh", events: []string{"call /bin/bash build.sh in {root}", "script build.sh: #!/bin/sh\necho building in {root}"}},
		{name: "a launcher by its path", command: "/usr/bin/env ls", events: []string{"call /usr/bin/env ls in {root}", "launched ls (certain) in {root}"}},
		{name: "a missing script", command: "bash none.sh", events: []string{"call bash none.sh in {root}", "unread none.sh: no such file"}},
		{name: "a script named by a variable", command: `bash "$x"`, events: []string{`call bash $x in {root}`, "unread $x: its path is built at run time"}},
		{
			name: "a script a here-document writes", command: "cat > s.sh <<EOF\necho \\$HOME \\\\ x\nEOF\nbash s.sh",
			events: []string{"call cat in {root}", "call bash s.sh in {root}", `script s.sh: echo $HOME \ x in {root}`},
		},
		{
			name: "a script a quoted here-document writes", command: "cat > s.sh <<'EOF'\necho \\$HOME\nEOF\nbash s.sh",
			events: []string{"call cat in {root}", "call bash s.sh in {root}", `script s.sh: echo \$HOME in {root}`},
		},
		{
			name: "a script tee writes from a here-document", command: "tee s.sh > /dev/null <<'EOF'\necho hi\nEOF\nbash s.sh",
			events: []string{"call tee s.sh in {root}", "call bash s.sh in {root}", "script s.sh: echo hi in {root}"},
		},
		{
			name: "a script a here-document appends to", command: "cat >> build.sh <<'EOF'\necho more\nEOF\nbash build.sh",
			events: []string{"call cat in {root}", "call bash build.sh in {root}", "script build.sh: #!/bin/sh\necho building\necho more in {root}"},
		},
		{
			name: "a script a here-document with an expansion writes", command: "cat > s.sh <<EOF\necho $HOME\nEOF\nbash s.sh",
			events: []string{"call cat in {root}", "call bash s.sh in {root}", "unread s.sh: the command writes it before running it"},
		},
		{
			name: "a script after a write the walk cannot place", command: `echo x > "$out"; bash build.sh`,
			events: []string{"call echo x in {root}", "call bash build.sh in {root}", "unread build.sh: the command writes it before running it"},
		},
		{
			name: "a script in shell code", command: "bash -c 'bash build.sh'",
			events: []string{"call bash -c bash build.sh in {root}", "code bash -c: bash build.sh in {root}"},
		},
		{name: "a script too deep", command: "bash build.sh", depth: shellwalk.MaxDepth, events: []string{"call bash build.sh in {root}", "unread build.sh: scripts run scripts too deep"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file, err := shellwalk.Parse(tc.command)
			if err != nil {
				t.Fatalf("parsing: %v", err)
			}

			recorder := &test.WalkRecorder{}
			shellwalk.WalkWith(file, recorder, shellwalk.Scripts{Files: test.TreeFiles{Roots: []string{root}}, Dir: root, Depth: tc.depth})

			want := make([]string, 0, len(tc.events))
			for _, event := range tc.events {
				want = append(want, strings.ReplaceAll(event, "{root}", root))
			}

			if !slices.Equal(recorder.Events, want) {
				t.Errorf("the walk should report every script the command runs, where it runs, got: %q", recorder.Events)
			}
		})
	}
}

func TestTheWalkFollowsACdWhatFollowsRunsOnlyAfter(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolving the test root: %v", err)
	}

	test.WriteFile(t, filepath.Join(root, "sub", "build.sh"), test.Fixture(t, "shellguard/scripts/build.sh"))

	const read = "script ./build.sh: #!/bin/sh\necho building in {root}/sub"

	const lost = "unread ./build.sh: the directory it runs in is not known"

	cases := []struct {
		name    string
		command string
		events  []string
	}{
		{
			name: "a cd guarded by exit", command: "cd sub || exit 1; ./build.sh",
			events: []string{"call cd sub in {root}", "call exit 1 in {root}/sub", "call ./build.sh in {root}/sub", read},
		},
		{
			name: "a cd guarded by a bare return", command: "cd sub || return; ./build.sh",
			events: []string{"call cd sub in {root}", "call return in {root}/sub", "call ./build.sh in {root}/sub", read},
		},
		{
			name: "a cd behind another command in a chain", command: "make && cd sub && ./build.sh",
			events: []string{"call make in {root}", "call cd sub in {root}", "call ./build.sh in {root}/sub", read},
		},
		{
			name: "a statement after a chain with a cd behind another command", command: "make && cd sub && true; ./build.sh",
			events: []string{"call make in {root}", "call cd sub in {root}", "call true in {root}/sub", "call ./build.sh", lost},
		},
		{
			name: "a statement after a chain a cd opens", command: "cd sub && make; ./build.sh",
			events: []string{"call cd sub in {root}", "call make in {root}/sub", "call ./build.sh in {root}/sub", read},
		},
		{
			name: "a cd followed by something else on failure", command: "cd sub || echo no; ./build.sh",
			events: []string{"call cd sub in {root}", "call echo no", "call ./build.sh", lost},
		},
		{
			name: "a cd guarded by an exit given too many operands", command: "cd sub || exit 1 2; ./build.sh",
			events: []string{"call cd sub in {root}", "call exit 1 2", "call ./build.sh", lost},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file, err := shellwalk.Parse(tc.command)
			if err != nil {
				t.Fatalf("parsing: %v", err)
			}

			recorder := &test.WalkRecorder{}
			shellwalk.WalkWith(file, recorder, shellwalk.Scripts{Files: test.TreeFiles{Roots: []string{root}}, Dir: root})

			want := make([]string, 0, len(tc.events))
			for _, event := range tc.events {
				want = append(want, strings.ReplaceAll(event, "{root}", root))
			}

			if !slices.Equal(recorder.Events, want) {
				t.Errorf("the walk should place the script where the cd certainly took the shell, got: %q", recorder.Events)
			}
		})
	}
}

func TestAWalkWithoutScriptsReadsNone(t *testing.T) {
	file, err := shellwalk.Parse("bash none.sh; ./x")
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}

	recorder := &test.WalkRecorder{}
	shellwalk.Walk(file, recorder)

	if !slices.Equal(recorder.Events, []string{"call bash none.sh", "call ./x"}) {
		t.Errorf("a walk reading no scripts should report none, got: %q", recorder.Events)
	}
}

func TestCertainAndTimeLimitsFindTheCommandRun(t *testing.T) {
	cases := []struct {
		command   string
		certain   []int
		timeLimit int
	}{
		{command: "make test", certain: []int{0}, timeLimit: -1},
		{command: "timeout -s KILL 9 make", certain: []int{0, 4}, timeLimit: 4},
		{command: "gtimeout 9 make", certain: []int{0, 2}, timeLimit: 2},
		{command: "uu-timeout 9 make", certain: []int{0, 2}, timeLimit: 2},
		{command: "perl -e 'alarm 9; exec @ARGV' make", certain: []int{0, 3}, timeLimit: 3},
		{command: "perl -e 'print 1' make", certain: []int{0}, timeLimit: -1},
		{command: "sudo gtimeout 9 make", certain: []int{0, 1, 3}, timeLimit: -1},
		{command: "nice -n 5 make", certain: []int{0, 3}, timeLimit: -1},
		{command: "gtimeout 9", certain: []int{0}, timeLimit: -1},
	}

	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			file, err := shellwalk.Parse(tc.command)
			if err != nil {
				t.Fatalf("parsing: %v", err)
			}

			args := file.Stmts[0].Cmd.(*syntax.CallExpr).Args

			if got := shellwalk.Certain(args); !slices.Equal(got, tc.certain) {
				t.Errorf("every word the shell certainly runs as a command should be found, got %v", got)
			}

			if got := shellwalk.TimeLimitRuns(args); got != tc.timeLimit {
				t.Errorf("only a time limit should name the command it runs, got %d", got)
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
		{words: []string{"uv", "--directory", "sub", "run", "sdd"}, moves: true},
		{words: []string{"pnpm", "-C", "sub", "exec", "sdd"}, moves: true},
		{words: []string{"uv", "run", "--with", "x", "sdd"}},
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
		{word: "[ab]x", patterned: true},
		{word: "sd[d]", patterned: true},
		{word: `run["a]"]`, patterned: true},
		{word: `run[a"]"`, patterned: true},
		{word: `run[a\]`, patterned: true},
		{word: "run[.log"},
		{word: "run[$x", patterned: true},
		{word: `run["$x"`, patterned: true},
		{word: "run[$(echo)", patterned: true},
		{word: `run["a"`},
		{word: "["},
		{word: `"]"[`},
		{word: `\[ab]`},
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

func TestOneKnownWordFindsWhatStaysOneWord(t *testing.T) {
	cases := []struct {
		word string
		one  bool
	}{
		{word: "make", one: true},
		{word: "[", one: true},
		{word: `"make"`, one: true},
		{word: `'make'`, one: true},
		{word: `"$tool"`, one: true},
		{word: `"${tool}-x"`, one: true},
		{word: `"$(which make)"`, one: true},
		{word: "$tool"},
		{word: "$(which make)"},
		{word: "{rm,-rf,/}"},
		{word: "./*"},
		{word: `"${tools[@]}"`},
		{word: `"$*"`},
		{word: `"${!name}"`},
		{word: `x"$tool"`},
		{word: `"$tool"$x`},
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

			if shellwalk.OneKnownWord(call.Args[1]) != tc.one {
				t.Error("a word should count as one word only where no expansion, glob or brace can split it")
			}
		})
	}
}
