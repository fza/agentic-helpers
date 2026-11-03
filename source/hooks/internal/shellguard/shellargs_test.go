package shellguard_test

import (
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

// Every case below is how the shell itself behaved, run with marker scripts on
// macOS's bash, sh, zsh, dash and ksh93, on mksh 59c and on fish 4.9, and on
// Debian's bash 5.2, sh (dash), dash 0.5.12, ksh 93u+m, mksh R59 and fish 3.6
// and 4.0. Where the two platforms differ, the stricter reading is the case.
func TestAShellsArgumentsAreJudgedAsTheShellReadsThem(t *testing.T) {
	g := newGuard(t, true, "")
	test.FixtureTreeReplacing(t, "shellguard/scripts", filepath.Join(g.tree.Project, "scripts"), g.placeholders)

	const (
		judged     = "agentic-shell-guard: in the script clean-outside.sh: outside project: {outside}/x"
		judgedFish = "agentic-shell-guard: in the script clean.fish: inline code for fish names a removal or mktemp; refusing"
		code       = "deny"
		ask        = "ask"
		allow      = "allow"
		anyReason  = "*"
	)

	cases := []struct {
		name     string
		command  string
		decision string
		reason   string
	}{
		{"a script takes -c as its argument", "bash clean-outside.sh -c 'echo hi'", ask, judged},
		{"zsh's script takes -c as its argument", "zsh clean-outside.sh -c 'echo hi'", ask, judged},
		{"fish's script takes -c as its argument", "fish clean.fish -c 'echo hi'", ask, judgedFish},

		{"zsh's value attached", "zsh -oerrexit clean-outside.sh", ask, judged},
		{"zsh's value attached in a group", "zsh -eoerrexit clean-outside.sh", ask, judged},
		{"ksh's value attached", "ksh -ovi clean-outside.sh", ask, judged},
		{"mksh's value attached", "mksh -ovi clean-outside.sh", ask, judged},
		{"zsh's value holding an s", "zsh -osh_word_split clean-outside.sh", ask, judged},
		{"zsh's value holding a c", "zsh -ocorrect clean-outside.sh", ask, judged},
		{"zsh's value holding an n is no parse", "zsh -ono_rcs clean-outside.sh", ask, judged},
		{"bash's -o takes the next word", "bash -o errexit clean-outside.sh", ask, judged},
		{"bash's +o takes the next word", "bash +o errexit clean-outside.sh", ask, judged},
		{"bash's +O takes the next word", "bash +O extglob clean-outside.sh", ask, judged},
		{"zsh's -O takes none", "zsh -O clean-outside.sh", ask, judged},
		{"sh's -O takes the next word", "sh -O extglob clean-outside.sh", ask, judged},
		{"dash's -o takes the next word", "dash -o errexit clean-outside.sh", ask, judged},
		{"ksh's +o takes the next word", "ksh +o errexit clean-outside.sh", ask, judged},
		{"ksh's -R takes the next word", "ksh -R db clean-outside.sh", ask, judged},
		{"mksh's -o takes the next word", "mksh -o errexit clean-outside.sh", ask, judged},
		{"mksh's -T takes the next word", "mksh -T tty clean-outside.sh", ask, judged},

		{"zsh's + ends the options", "zsh + -c clean-outside.sh", ask, "agentic-shell-guard: cannot read the script -c (no such file)"},
		{"zsh's +- ends the options", "zsh +- -c clean-outside.sh", ask, "agentic-shell-guard: cannot read the script -c (no such file)"},
		{"zsh's -b ends the options", "zsh -b -c clean-outside.sh", ask, "agentic-shell-guard: cannot read the script -c (no such file)"},
		{"zsh's +b ends the options", "zsh +b -c clean-outside.sh", ask, "agentic-shell-guard: cannot read the script -c (no such file)"},
		{"zsh's -b grouped ends the options", "zsh -eb -c clean-outside.sh", ask, "agentic-shell-guard: cannot read the script -c (no such file)"},
		{"ksh's + ends the options", "ksh + -c clean-outside.sh", ask, "agentic-shell-guard: cannot read the script -c (no such file)"},
		{"mksh's + ends the options", "mksh + -c clean-outside.sh", ask, "agentic-shell-guard: cannot read the script -c (no such file)"},
		{"bash's + ends nothing", "bash + -c 'rm x'", code, anyReason},
		{"sh's + ends nothing", "sh + -c 'rm x'", code, anyReason},
		{"dash's + ends nothing", "dash + -c 'rm x'", code, anyReason},
		{"ksh's +- ends nothing", "ksh +- -c 'rm x'", code, anyReason},
		{"bash's -b is a flag", "bash -b -c 'rm x'", code, anyReason},

		{"a - past the end of the options is a file", "bash - -", ask, "agentic-shell-guard: cannot read the script - (no such file)"},
		{"a - past -- is a file", "bash -- -", ask, "agentic-shell-guard: cannot read the script - (no such file)"},
		{"a - past zsh's + is a file", "zsh + -", ask, "agentic-shell-guard: cannot read the script - (no such file)"},
		{"/dev/stdin past the end of the options is the input", "bash -- /dev/stdin", allow, ""},

		{"bash's +c is -c", "bash +c 'rm x'", code, anyReason},
		{"sh's +c is -c", "sh +c 'rm x'", code, anyReason},
		{"zsh's +c is -c", "zsh +c 'rm x'", code, anyReason},
		{"dash's +c is -c", "dash +c 'rm x'", code, anyReason},
		{"bash's +ec is -c", "bash +ec 'rm x'", code, anyReason},
		{"ksh's +c is no -c", "ksh +c clean-outside.sh", ask, judged},
		{"mksh's +c is no -c", "mksh +c clean-outside.sh", ask, judged},
		{"bash's +s is -s", "bash +s clean-outside.sh", allow, ""},
		{"sh's +s may be no -s, as dash reads it", "sh +s clean-outside.sh", ask, judged},
		{"sh's +s in a group may be no -s", "sh +es clean-outside.sh", ask, judged},
		{"sh's -s is -s", "sh -s clean-outside.sh", allow, ""},
		{"zsh's +s is no -s", "zsh +s clean-outside.sh", ask, judged},
		{"dash's +s is no -s", "dash +s clean-outside.sh", ask, judged},
		{"ksh's +s is no -s", "ksh +s clean-outside.sh", ask, judged},
		{"mksh's +s is no -s", "mksh +s clean-outside.sh", ask, judged},

		{"fish's - is a file", "fish -", ask, "agentic-shell-guard: cannot read the script - (no such file)"},
		{"fish's - is a file, not an end", "fish - clean.fish", ask, "agentic-shell-guard: cannot read the script - (no such file)"},
		{"fish's -c past - is an argument", "fish - -c 'rm x'", ask, "agentic-shell-guard: cannot read the script - (no such file)"},
		{"fish's -- ends the options", "fish -- -c 'rm x'", ask, "agentic-shell-guard: cannot read the script -c (no such file)"},
		{"fish's -C runs before its script", "fish -C 'echo hi' clean.fish", ask, judgedFish},
		{"fish's --init-command runs before its script", "fish --init-command 'echo hi' clean.fish", ask, judgedFish},
		{"fish's -d takes a value", "fish -d proc clean.fish", ask, judgedFish},
		{"fish's -D takes a value", "fish -D 3 clean.fish", ask, judgedFish},
		{"fish's -f takes a value", "fish -f flag clean.fish", ask, judgedFish},
		{"fish's -o takes a value", "fish -o log clean.fish", ask, judgedFish},
		{"fish's -p takes a value", "fish -p log clean.fish", ask, judgedFish},
		{"fish's value attached", "fish -dproc clean.fish", ask, judgedFish},
		{"fish's value in a group", "fish -Nd proc clean.fish", ask, judgedFish},
		{"fish's --debug-stack-frames takes a value", "fish --debug-stack-frames 3 clean.fish", ask, judgedFish},
		{"fish's long value attached", "fish --debug=proc clean.fish", ask, judgedFish},
		{"fish's abbreviated long value", "fish --debug-s 3 clean.fish", ask, judgedFish},
		{"fish's -c attached", "fish -c'rm x'", code, anyReason},
		{"fish's -C attached", "fish -C'rm x'", code, anyReason},
		{"fish's -c in a group", "fish -Pc 'rm x'", code, anyReason},
		{"fish's -c swallows the rest of its group", "fish -cP 'rm x'", allow, ""},
		{"fish's --command attached", "fish --command='rm x'", code, anyReason},
		{"fish's --init-command attached", "fish --init-command='rm x'", code, anyReason},
		{"fish's abbreviated --command", "fish --comm 'rm x'", code, anyReason},
		{"fish's abbreviated --command attached", "fish --comm='rm x'", code, anyReason},
		{"fish's shortest --command", "fish --c 'rm x'", code, anyReason},
		{"fish's abbreviated --init-command", "fish --init 'rm x'", code, anyReason},
		{"fish's second -c", "fish -c 'echo hi' -c 'rm x'", code, anyReason},
		{"fish's second -C", "fish -C 'echo hi' -C 'rm x'", code, anyReason},
		{"fish's first -c", "fish -c 'rm x' -c 'echo hi'", code, anyReason},
		{"fish's first -C", "fish -C 'rm x' -C 'echo hi'", code, anyReason},
		{"fish's --debug takes a value", "fish --debug proc clean.fish", ask, judgedFish},
		{"fish's --profile takes a value", "fish --profile log clean.fish", ask, judgedFish},
		{"a word built at run time holds no option", `bash "$opt" -c 'echo hi'`, ask, anyReason},
		{"fish's word built at run time holds no option", `fish "$opt" -c 'echo hi'`, ask, anyReason},
		{"fish's -C after -c", "fish -c 'echo hi' -C 'rm x'", code, anyReason},
		{"fish's operand past -c is an argument", "fish -c 'echo hi' clean.fish", allow, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision, reason := g.judge(tc.command, "{project}/scripts")

			if decision != tc.decision {
				t.Fatalf("the call should be judged as the shell reads it, got %s %q", decision, reason)
			}

			if tc.reason != anyReason && reason != g.placeholders.Replace(tc.reason) {
				t.Errorf("the reason should name what the shell runs, got %q", reason)
			}
		})
	}
}

func TestPipedCodeIsJudgedAsTheShellReadsIt(t *testing.T) {
	g := newGuard(t, true, "")
	test.FixtureTreeReplacing(t, "shellguard/scripts", filepath.Join(g.tree.Project, "scripts"), g.placeholders)

	cases := []struct {
		name     string
		command  string
		decision string
	}{
		{"dash runs its input beside -c with -s", "echo 'rm x' | dash -c -s 'echo hi'", "deny"},
		{"dash runs its input beside -cs", "echo 'rm x' | dash -cs 'echo hi'", "deny"},
		{"dash runs its input beside -sc", "echo 'rm x' | dash -sc 'echo hi'", "deny"},
		{"sh may be dash", "echo 'rm x' | sh -cs 'echo hi'", "deny"},
		{"bash runs no input beside -c with -s", "echo 'rm x' | bash -cs 'echo hi'", "allow"},
		{"zsh runs no input beside -c with -s", "echo 'rm x' | zsh -cs 'echo hi'", "allow"},
		{"zsh's option value holding a c leaves the input", "echo 'rm x' | zsh -ocorrect", "deny"},
		{"ksh's option value holding a c leaves the input", "echo 'rm x' | ksh -ocsh", "deny"},
		{"bash's +s reads the input past an operand", "echo 'rm x' | bash +s foo", "deny"},
		{"zsh's +s leaves the operand the script", "echo x | zsh +s scripts/build.sh", "allow"},
		{"sh's +s may read the input as bash does", "echo 'rm x' | sh +s scripts/build.sh", "deny"},
		{"fish's -C leaves the input", "echo 'rm x' | fish -C 'echo hi'", "deny"},
		{"fish's -c replaces the input", "echo 'rm x' | fish -c 'echo hi'", "allow"},
		{"fish's --command replaces the input", "echo 'rm x' | fish --comm='echo hi'", "allow"},
		{"fish's -c attached replaces the input", "echo 'rm x' | fish -c'echo hi'", "allow"},
		{"fish's value letter past the group leaves the input", "echo 'rm x' | fish -d proc", "deny"},
		{"fish takes - for a file, not the input", "echo x | fish - scripts/build.sh", "ask"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision, reason := g.judge(tc.command, "{project}")
			if decision != tc.decision {
				t.Errorf("code on a shell's input should be judged as the shell reads it, got %s %q", decision, reason)
			}
		})
	}
}
