package shellguard_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

const self = "agentic-shell-guard: "

// onMachine is the guard judging on a machine whose PATH, the directory it
// returns, holds installed: sed and date as GNU's where gnu names them and
// BSD's otherwise.
func onMachine(t *testing.T, options string, installed []string, gnu []string) (guard, string) {
	t.Helper()

	g := newGuard(t, true, options)
	path := test.FlavouredPath(t, installed, gnu)
	g.env.Getenv = test.Getenv(path)

	return g, path
}

func TestAMissingTimeoutNamesEachAlternativeInTurn(t *testing.T) {
	cases := []struct {
		name      string
		installed []string
		reason    string
	}{
		{"timeout installed", []string{"timeout", "gtimeout", "uu-timeout", "perl"}, ""},
		{"gtimeout first", []string{"gtimeout", "uu-timeout", "perl"}, "timeout is not installed here; use: gtimeout 9 make test"},
		{"uu-timeout next", []string{"uu-timeout", "perl"}, "timeout is not installed here; use: uu-timeout 9 make test"},
		{"the perl alarm form last", []string{"perl"}, "timeout is not installed here; use: perl -e 'alarm 9; exec @ARGV' make test"},
		{"nothing stands in", nil, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, _ := onMachine(t, "", tc.installed, nil)

			decision, reason := g.judge("timeout 9 make test", "{project}")
			switch {
			case tc.reason == "" && decision != "allow":
				t.Errorf("with timeout installed or nothing to name, the call should run, got %s %q", decision, reason)
			case tc.reason != "" && (decision != "deny" || reason != self+tc.reason):
				t.Errorf("the refusal should name the first installed alternative, got %s %q", decision, reason)
			}
		})
	}
}

func TestTheRewriteIsBuiltFromTheParsedCommand(t *testing.T) {
	cases := []struct {
		name      string
		installed []string
		command   string
		use       string
	}{
		{"a pipeline and quoting", []string{"perl"}, `cd sub && timeout 5m go test './...' | tail -5`,
			`cd sub && perl -e 'alarm 300; exec @ARGV' go test './...' | tail -5`},
		{"a fraction rounded up", []string{"perl"}, "timeout 1.5 make", "perl -e 'alarm 2; exec @ARGV' make"},
		{"options the alarm form cannot carry", []string{"perl"}, "timeout -s KILL 9 make",
			"perl -e 'alarm <seconds>; exec @ARGV' <command>"},
		{"a duration built at run time", []string{"perl"}, `timeout "$d" make`, "perl -e 'alarm <seconds>; exec @ARGV' <command>"},
		{"options gtimeout carries", []string{"gtimeout"}, "timeout -s KILL -k 5 9 make", "gtimeout -s KILL -k 5 9 make"},
		{"a duration gtimeout carries", []string{"gtimeout"}, `timeout "$d" make`, `gtimeout "$d" make`},
		{"under a launcher", []string{"gtimeout"}, "sudo timeout 9 make", "sudo gtimeout 9 make"},
		{"a time limit inside a time limit", []string{"gtimeout"}, "timeout 9 timeout 3 make", "gtimeout 9 gtimeout 3 make"},
		{"in a substitution", []string{"gtimeout"}, `echo "$(timeout 9 make)"`, `echo "$(gtimeout 9 make)"`},
		{"timeout with nothing to run", []string{"gtimeout"}, "timeout 9", "gtimeout <duration> <command>"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, _ := onMachine(t, "", tc.installed, nil)

			decision, reason := g.judge(tc.command, "{project}")
			if decision != "deny" || reason != self+"timeout is not installed here; use: "+tc.use {
				t.Errorf("the refusal should name the command rewritten where a rewrite is exact, got %s %q", decision, reason)
			}
		})
	}
}

func TestWhatNamesNoMissingCommandRuns(t *testing.T) {
	for _, command := range []string{
		"echo timeout", "/usr/local/bin/timeout 9 make", `bash -c 'timeout 9 make'`, "command -v timeout",
	} {
		t.Run(command, func(t *testing.T) {
			g, _ := onMachine(t, "", []string{"gtimeout", "bash"}, nil)

			if decision, reason := g.judge(command, "{project}"); decision != "allow" {
				t.Errorf("a call that runs no missing command by name should pass this guard, got %s %q", decision, reason)
			}
		})
	}
}

func TestARemovalBehindATimeLimitIsJudgedAsStandingAlone(t *testing.T) {
	for _, limit := range []string{"timeout 9", "gtimeout 9", "uu-timeout -k 5 9", "perl -e 'alarm 9; exec @ARGV'"} {
		t.Run(limit, func(t *testing.T) {
			g, _ := onMachine(t, "", []string{"timeout", "gtimeout", "uu-timeout", "perl"}, nil)

			decision, reason := g.judge(limit+" rm -rf ~/x", "{project}")
			if decision != "ask" || reason != self+"outside project: ~/x" {
				t.Errorf("a removal outside the project behind a time limit should get the outside-project verdict, got %s %q", decision, reason)
			}

			if decision, reason = g.judge(limit+" rm -rf ./build", "{project}"); decision != "allow" {
				t.Errorf("a removal inside the project behind a time limit should run, got %s %q", decision, reason)
			}

			if decision, _ = g.judge(limit+` rm -rf "$x"`, "{project}"); decision != "deny" {
				t.Errorf("a removal behind a time limit should keep every other removal guard, got %s", decision)
			}

			if decision, _ = g.judge("ls && "+limit+" rm -rf ./build", "{project}"); decision != "deny" {
				t.Errorf("a removal behind a time limit should still run alone, got %s", decision)
			}
		})
	}
}

func TestEachDialectUsageIsRefusedOnlyOnItsFlavour(t *testing.T) {
	cases := []struct {
		command string
		says    string
	}{
		{"sed -i 's/a/A/' f", "-i takes the next word as its backup suffix"},
		{"sed -i -e 's/a/A/' f", "-i takes the next word as its backup suffix"},
		{"sed --in-place 's/a/A/' f", "it takes no long option"},
		{"sed -z 's/a/b/' f", "it has no -z"},
		{`sed 's/a\+/X/' f`, `\+ \? \| \s \w \b \< \> match nothing in a basic expression`},
		{`sed 's/a\?/X/' f`, `\+ \? \| \s \w \b \< \> match nothing in a basic expression`},
		{`sed 's/a\|b/X/' f`, `\+ \? \| \s \w \b \< \> match nothing in a basic expression`},
		{`sed 's/\s//g' f`, `\+ \? \| \s \w \b \< \> match nothing in a basic expression`},
		{`sed 's/\w//' f`, `\+ \? \| \s \w \b \< \> match nothing in a basic expression`},
		{`sed 's/\bx//' f`, `\+ \? \| \s \w \b \< \> match nothing in a basic expression`},
		{`sed 's/\<x//' f`, `\+ \? \| \s \w \b \< \> match nothing in a basic expression`},
		{`sed 's/x\>//' f`, `\+ \? \| \s \w \b \< \> match nothing in a basic expression`},
		{"sed '0,/re/d' f", "a 0,/re/ address matches nothing"},
		{"sed '/x/a text' f", "a, i and c take their text only after a backslash and a newline"},
		{"sed '1i text' f", "a, i and c take their text only after a backslash and a newline"},
		{"sed '$c text' f", "a, i and c take their text only after a backslash and a newline"},
		{"date -d yesterday", "it has no -d"},
	}

	for _, tc := range cases {
		name := strings.Fields(tc.command)[0]
		alternative := "g" + name

		t.Run(tc.command, func(t *testing.T) {
			bsd, _ := onMachine(t, "", []string{name}, nil)

			decision, reason := bsd.judge(tc.command, "{project}")
			lead := self + name + " here is BSD " + name + ": " + tc.says + "; instead "
			if decision != "deny" || !strings.HasPrefix(reason, lead) {
				t.Errorf("the usage should be refused on the BSD flavour with its own form, got %s %q", decision, reason)
			}

			withGNU, _ := onMachine(t, "", []string{name, alternative}, nil)

			decision, reason = withGNU.judge(tc.command, "{project}")
			use := self + name + " here is BSD " + name + ": " + tc.says + "; use: " + alternative + " " +
				strings.TrimPrefix(tc.command, name+" ")
			if decision != "deny" || reason != use {
				t.Errorf("the refusal should name the GNU alternative installed, got %s %q", decision, reason)
			}

			gnu, _ := onMachine(t, "", []string{name}, []string{name})
			if decision, reason = gnu.judge(tc.command, "{project}"); decision != "allow" {
				t.Errorf("the usage should run on the GNU flavour, got %s %q", decision, reason)
			}

			unknown := newGuard(t, true, "")
			unknown.env.Getenv = test.Getenv(test.Stubs(t, name))

			if decision, reason = unknown.judge(tc.command, "{project}"); decision != "allow" {
				t.Errorf("a binary carrying no flavour marker should leave the usage running, got %s %q", decision, reason)
			}
		})
	}
}

func TestWhatWorksOnBSDRuns(t *testing.T) {
	for _, command := range []string{
		"sed -E 's/a+/X/' f", "sed -i '' 's/a/b/' f", `sed 's/a/x\+y/' f`, "sed -n '/x/p' f", "sed -i.bak 's/a/b/' f",
		"sed \"$script\" f", `sed $'/x/a\\\ntext' f`, "date +%s", "date -j -f %Y 2026 +%s",
		"/opt/gnu/sed -i 's/a/b/' f",
	} {
		t.Run(command, func(t *testing.T) {
			g, _ := onMachine(t, "", []string{"sed", "date"}, nil)

			if decision, reason := g.judge(command, "{project}"); decision != "allow" {
				t.Errorf("a usage BSD runs as written should pass, got %s %q", decision, reason)
			}
		})
	}
}

func TestADialectNamedByPathRunsNothing(t *testing.T) {
	g, path := onMachine(t, "", []string{"sed"}, nil)

	for _, command := range []string{path + "/sed -i 's/a/b/' f", "./sed -i 's/a/b/' f"} {
		decision, reason := g.judge(command, "{project}")
		if decision != "allow" {
			t.Errorf("a command named by a path should not be judged, even where it carries a BSD marker, got %s %q", decision, reason)
		}
	}
}

func TestPerlCodeAfterTheAlarmFormStaysInlineCode(t *testing.T) {
	for _, command := range []string{
		`perl -e 'alarm 9; exec @ARGV' -e 'system("rm -rf ~/x")' x`,
		`perl -e 'alarm 9; exec @ARGV' 'rm -rf ~/x'`,
		`perl -e 'alarm 9; exec @ARGV' 'rm${IFS}x'`,
		`perl -e 'alarm 9; exec @ARGV' 'rm -rf ~/x' $empty`,
		`perl -e 'alarm 9; exec @ARGV' 'rm -rf ~/x' "$@"`,
		`perl -e 'alarm 9; exec @ARGV' 'rm -rf ~/x' *.none`,
		`perl -e 'alarm 9; exec @ARGV' 'rm -rf ~/x' x`,
	} {
		t.Run(command, func(t *testing.T) {
			g, _ := onMachine(t, "", []string{"timeout", "perl"}, nil)

			decision, reason := g.judge(command, "{project}")
			if decision != "deny" || reason != self+"inline code for perl names a removal or mktemp; refusing" {
				t.Errorf("code perl or its shell runs should take the inline-code verdict, got %s %q", decision, reason)
			}
		})
	}
}

func TestEveryFindingIsRewrittenAndTheFirstNamed(t *testing.T) {
	g, _ := onMachine(t, "", []string{"gtimeout", "sed", "gsed"}, nil)

	decision, reason := g.judge("timeout 9 sed -i 's/a/b/' f", "{project}")
	if decision != "deny" || reason != self+"timeout is not installed here; use: gtimeout 9 gsed -i 's/a/b/' f" {
		t.Errorf("the first finding should lead, with every exact rewrite applied, got %s %q", decision, reason)
	}
}

func TestAFlavourIsKeptAcrossCalls(t *testing.T) {
	g, path := onMachine(t, "", []string{"sed"}, nil)
	g.env.StateDir = t.TempDir()
	sed := filepath.Join(path, "sed")

	if decision, _ := g.judge("sed -i 's/a/b/' f", "{project}"); decision != "deny" {
		t.Fatalf("BSD sed should refuse the usage, got %s", decision)
	}

	info, err := os.Stat(sed)
	if err != nil {
		t.Fatal(err)
	}

	test.WriteFile(t, sed, strings.Repeat("x", int(info.Size())))

	err = os.Chtimes(sed, info.ModTime(), info.ModTime())
	if err != nil {
		t.Fatal(err)
	}

	if decision, _ := g.judge("sed -i 's/a/b/' f", "{project}"); decision != "deny" {
		t.Errorf("the flavour should be kept under the cache root for the same binary, got %s", decision)
	}
}
