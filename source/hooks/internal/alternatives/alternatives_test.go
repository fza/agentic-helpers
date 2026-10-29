package alternatives_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/alternatives"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func literal(words ...string) []alternatives.Arg {
	args := make([]alternatives.Arg, 0, len(words))
	for _, word := range words {
		args = append(args, alternatives.Arg{Text: word, Literal: true, OneWord: true})
	}

	return args
}

// usageOf is what the first usage of the command the arguments make says,
// empty where they make none.
func usageOf(t *testing.T, name string, args []alternatives.Arg) string {
	t.Helper()

	entry, found := alternatives.Lookup(name)
	if !found {
		t.Fatalf("the table should hold %s", name)
	}

	for _, usage := range entry.Usages {
		if usage.Matches(args) {
			return usage.Says
		}
	}

	return ""
}

func TestEachDialectUsageMatchesOnlyWhatFails(t *testing.T) {
	const (
		inPlace = "-i takes the next word as its backup suffix"
		long    = "it takes no long option"
		nulData = "it has no -z"
		escape  = `\+ \? \| \s \w \b \< \> match nothing in a basic expression`
		zero    = "a 0,/re/ address matches nothing"
		oneLine = "a, i and c take their text only after a backslash and a newline"
		dateD   = "it has no -d"
	)

	cases := []struct {
		name    string
		command string
		args    []string
		says    string
	}{
		{"in place without a suffix", "sed", []string{"-i", "s/a/A/", "f"}, inPlace},
		{"in place before -e", "sed", []string{"-i", "-e", "s/a/A/", "f"}, inPlace},
		{"in place grouped last", "sed", []string{"-ni", "s/a/A/p", "f"}, inPlace},
		{"in place as the last word", "sed", []string{"-i"}, inPlace},
		{"in place with an empty suffix", "sed", []string{"-i", "", "s/a/b/", "f"}, ""},
		{"in place with an attached suffix", "sed", []string{"-i.bak", "s/a/b/", "f"}, ""},
		{"a long option", "sed", []string{"--in-place", "s/a/A/", "f"}, long},
		{"a long option with its value", "sed", []string{"--expression=s/a/A/", "f"}, long},
		{"the end of options", "sed", []string{"--", "s/a/b/", "f"}, ""},
		{"null data", "sed", []string{"-z", "s/a/b/", "f"}, nulData},
		{"null data grouped", "sed", []string{"-nz", "p", "f"}, nulData},
		{"a plus in a basic expression", "sed", []string{"s/a\\+/X/", "f"}, escape},
		{"a question mark in a basic expression", "sed", []string{"s/a\\?/X/", "f"}, escape},
		{"an alternation in a basic expression", "sed", []string{"s/a\\|b/X/", "f"}, escape},
		{"a space class in a basic expression", "sed", []string{"s/\\s//g", "f"}, escape},
		{"a word class in a basic expression", "sed", []string{"s/\\w*//", "f"}, escape},
		{"a word boundary in a basic expression", "sed", []string{"s/\\bx//", "f"}, escape},
		{"a word start in a basic expression", "sed", []string{"s/\\<x//", "f"}, escape},
		{"a word end in a basic expression", "sed", []string{"s/x\\>//", "f"}, escape},
		{"an escape in an address", "sed", []string{"/a\\+/d", "f"}, escape},
		{"an escape in a custom-delimited address", "sed", []string{"\\#a\\+#d", "f"}, escape},
		{"an escape in an -e script", "sed", []string{"-n", "-e", "s/a\\+/X/p", "f"}, escape},
		{"an extended expression", "sed", []string{"-E", "s/a+/X/", "f"}, ""},
		{"an escape in an extended expression", "sed", []string{"-E", "s/a\\+/X/", "f"}, ""},
		{"an escape in an -r expression", "sed", []string{"-r", "s/\\s//", "f"}, ""},
		{"a plus in the replacement", "sed", []string{"s/a/x\\+y/", "f"}, ""},
		{"an escaped delimiter", "sed", []string{"s|a\\|b|X|", "f"}, ""},
		{"an escape inside a bracket", "sed", []string{"s/[\\s]/X/", "f"}, ""},
		{"a class bracket", "sed", []string{"s/[[:space:]]\\{2\\}/X/", "f"}, ""},
		{"an escape after a class bracket", "sed", []string{"s/[[:space:]]\\+/X/", "f"}, escape},
		{"an escape in a y string", "sed", []string{"y/a\\+/bcd/", "f"}, ""},
		{"an escape in appended text", "sed", []string{"/x/a\\\nfoo\\+", "f"}, ""},
		{"a zero address", "sed", []string{"0,/re/d", "f"}, zero},
		{"a line range from one", "sed", []string{"1,/re/d", "f"}, ""},
		{"a ten address", "sed", []string{"10,/re/d", "f"}, ""},
		{"a zero-led line one", "sed", []string{"01,/re/d", "f"}, ""},
		{"one-line append", "sed", []string{"/x/a text", "f"}, oneLine},
		{"one-line insert", "sed", []string{"1i text", "f"}, oneLine},
		{"one-line change", "sed", []string{"$c text", "f"}, oneLine},
		{"append after a newline", "sed", []string{"/x/a\\\ntext", "f"}, ""},
		{"an i inside a replacement", "sed", []string{"s/a/i b/", "f"}, ""},
		{"a script after -f", "sed", []string{"-f", "s.sed", "f"}, ""},
		{"a file named like an option", "sed", []string{"s/a/b/", "-i", "f"}, ""},
		{"date -d", "date", []string{"-d", "yesterday"}, dateD},
		{"date -d grouped", "date", []string{"-ud", "@0"}, dateD},
		{"date -d after a long option", "date", []string{"--utc", "-d", "@0"}, dateD},
		{"date with a format", "date", []string{"+%s"}, ""},
		{"date parsing BSD's way", "date", []string{"-j", "-f", "%Y", "2026", "+%s"}, ""},
		{"date with a reference value", "date", []string{"-r", "1700000000"}, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := usageOf(t, tc.command, literal(tc.args...))
			if got != tc.says {
				t.Errorf("the call should make exactly the usage verified to fail, got %q", got)
			}
		})
	}
}

func TestAWordBuiltAtRunTimeEndsTheReading(t *testing.T) {
	cases := map[string][]alternatives.Arg{
		"an expansion": {{Text: "", Literal: false}, {Text: "-i", Literal: true}, {Text: "s/a/b/", Literal: true}},
		"a glob":       {{Text: "-z*", Literal: false}, {Text: "s/a/b/", Literal: true}},
	}

	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if got := usageOf(t, "sed", args); got != "" {
				t.Errorf("a word the shell builds at run time should leave the rest unjudged, got %q", got)
			}
		})
	}
}

func TestOnlyTheExactAlarmFormLaunches(t *testing.T) {
	cases := []struct {
		name  string
		words []string
		at    int
	}{
		{"the alarm form", []string{"perl", "-e", "alarm 9; exec @ARGV", "make"}, 3},
		{"other perl code", []string{"perl", "-e", "print 1", "rm"}, -1},
		{"a fractional alarm", []string{"perl", "-e", "alarm 1.5; exec @ARGV", "make"}, -1},
		{"the form with nothing to run", []string{"perl", "-e", "alarm 9; exec @ARGV"}, -1},
		{"another program", []string{"ruby", "-e", "alarm 9; exec @ARGV", "make"}, -1},
		{"perl options after the form", []string{"perl", "-e", "alarm 9; exec @ARGV", "-e", "system 1", "x"}, -1},
		{"a program and its arguments", []string{"perl", "-e", "alarm 9; exec @ARGV", "make", "test"}, 3},
		{"shell code before another word", []string{"perl", "-e", "alarm 9; exec @ARGV", "rm -rf ~/x", "x"}, -1},
		{"one word with a space", []string{"perl", "-e", "alarm 9; exec @ARGV", "rm -rf x"}, -1},
		{"one word with a tab", []string{"perl", "-e", "alarm 9; exec @ARGV", "rm\tx"}, -1},
		{"one word with a semicolon", []string{"perl", "-e", "alarm 9; exec @ARGV", "make;ls"}, -1},
		{"one word with a dollar", []string{"perl", "-e", "alarm 9; exec @ARGV", "rm${IFS}x"}, -1},
		{"one word with a tilde", []string{"perl", "-e", "alarm 9; exec @ARGV", "~/bin/x"}, -1},
		{"one word with a newline", []string{"perl", "-e", "alarm 9; exec @ARGV", "a\nb"}, -1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := alternatives.PrefixLaunch(tc.words[0], literal(tc.words...)); got != tc.at {
				t.Errorf("only the exact alarm form should launch the word after it, got %d", got)
			}
		})
	}

	unknown := literal("perl", "-e", "alarm 9; exec @ARGV", "make")
	unknown[2].Literal = false

	if alternatives.PrefixLaunch("perl", unknown) >= 0 {
		t.Error("code the shell builds at run time should launch nothing")
	}

	single := literal("perl", "-e", "alarm 9; exec @ARGV", "make")
	single[3].Literal = false

	if alternatives.PrefixLaunch("perl", single) >= 0 {
		t.Error("a single word the shell builds at run time may hold shell code, and should launch nothing")
	}

	vanishing := literal("perl", "-e", "alarm 9; exec @ARGV", "make", "")
	vanishing[4] = alternatives.Arg{}

	if alternatives.PrefixLaunch("perl", vanishing) >= 0 {
		t.Error("a word after the program that may split or vanish should leave the call inline code")
	}

	quoted := literal("perl", "-e", "alarm 9; exec @ARGV", "make", "")
	quoted[4] = alternatives.Arg{OneWord: true}

	if alternatives.PrefixLaunch("perl", quoted) != 3 {
		t.Error("a word certain to stay one word, a double-quoted expansion among them, should keep the call a launcher")
	}
}

func TestFindLooksUpTheMachinesPath(t *testing.T) {
	stubs := test.Stubs(t, "gtimeout")
	plain := filepath.Join(stubs, "plain")
	test.WriteFile(t, plain, "not executable")

	err := os.Chmod(plain, 0o644)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Mkdir(filepath.Join(stubs, "directory"), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	nested := test.Stubs(t, "tool")

	err = os.Rename(nested, filepath.Join(stubs, "sub"))
	if err != nil {
		t.Fatal(err)
	}

	machine := alternatives.Machine{Path: strings.Join([]string{"", filepath.Join(stubs, "missing"), stubs}, ":")}

	cases := []struct {
		name  string
		found bool
	}{
		{"gtimeout", true}, {"plain", false}, {"directory", false}, {"timeout", false}, {"sub/tool", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, found := machine.Find(tc.name)
			if found != tc.found {
				t.Errorf("only an executable regular file on the PATH should be found, got %v", found)
			}
		})
	}
}

func TestAnEmptyPathEntryIsNotTheWorkingDirectory(t *testing.T) {
	t.Chdir(test.Stubs(t, "gtimeout"))

	machine := alternatives.Machine{Path: ":"}
	if _, found := machine.Find("gtimeout"); found {
		t.Error("the hook's working directory is not the shell's, so an empty PATH entry should find nothing")
	}
}

func TestARelativePathEntryFindsNothing(t *testing.T) {
	stubs := test.Stubs(t, "sed")
	t.Chdir(filepath.Dir(stubs))

	machine := alternatives.Machine{Path: filepath.Base(stubs) + ":.:./" + filepath.Base(stubs)}
	if _, found := machine.Find("sed"); found {
		t.Error("a relative PATH entry names a directory under the working directory, so it should find nothing")
	}

	marked := test.Binaries(t, map[string]string{"sed": "bsd-sed"})
	t.Chdir(filepath.Dir(marked))

	if got := machine.Flavour(context.Background(), entryOf(t, "sed"), filepath.Join(filepath.Base(marked), "sed")); got != "" {
		t.Errorf("a relative path should get no flavour, got %q", got)
	}
}

func entryOf(t *testing.T, name string) alternatives.Entry {
	t.Helper()

	entry, found := alternatives.Lookup(name)
	if !found {
		t.Fatalf("the table should hold %s", name)
	}

	return entry
}

func TestTheFlavourIsReadFromTheBinarysMarkers(t *testing.T) {
	stubs := test.Binaries(t, map[string]string{
		"gsed": "gnu-sed", "bsdsed": "bsd-sed", "gdate": "gnu-date", "bsddate": "bsd-date", "busybox": "unmarked",
	})

	both := filepath.Join(stubs, "both")
	test.WriteFile(t, both, test.Fixture(t, "alternatives/bsd-sed")+test.Fixture(t, "alternatives/gnu-sed"))

	err := os.Mkdir(filepath.Join(stubs, "directory"), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		command string
		binary  string
		flavour string
	}{
		{"sed", "gsed", alternatives.FlavourGNU},
		{"sed", "bsdsed", alternatives.FlavourBSD},
		{"date", "gdate", alternatives.FlavourGNU},
		{"date", "bsddate", alternatives.FlavourBSD},
		{"sed", "busybox", ""},
		{"date", "gsed", ""},
		{"sed", "both", alternatives.FlavourGNU},
		{"sed", "directory", ""},
		{"sed", "missing", ""},
	}

	for _, tc := range cases {
		t.Run(tc.command+" "+tc.binary, func(t *testing.T) {
			machine := &alternatives.Machine{}

			if got := machine.Flavour(context.Background(), entryOf(t, tc.command), filepath.Join(stubs, tc.binary)); got != tc.flavour {
				t.Errorf("the flavour should be the one the command's own markers name, GNU first, and none without one, got %q", got)
			}
		})
	}
}

func TestAMarkerPastTheReadLimitNamesNoFlavour(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sed")
	padding := strings.Repeat("\x00", 8<<20)
	test.WriteFile(t, path, padding+test.Fixture(t, "alternatives/bsd-sed"))

	machine := &alternatives.Machine{}
	if got := machine.Flavour(context.Background(), entryOf(t, "sed"), path); got != "" {
		t.Errorf("only a bounded head of the binary should be read, got %q", got)
	}
}

func TestTheFlavourIsReadOncePerBinary(t *testing.T) {
	stubs := test.Binaries(t, map[string]string{"sed": "bsd-sed"})
	path := filepath.Join(stubs, "sed")
	cache := filepath.Join(t.TempDir(), "cache", "flavours.json")
	sed := entryOf(t, "sed")

	first := &alternatives.Machine{Path: stubs, CacheFile: cache}
	if got := first.Flavour(context.Background(), sed, path); got != alternatives.FlavourBSD {
		t.Fatalf("the BSD marker should read as BSD, got %q", got)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	unmarked := strings.Repeat("x", int(info.Size()))
	test.WriteFile(t, path, unmarked)

	err = os.Chtimes(path, info.ModTime(), info.ModTime())
	if err != nil {
		t.Fatal(err)
	}

	second := &alternatives.Machine{Path: stubs, CacheFile: cache}
	if got := second.Flavour(context.Background(), sed, path); got != alternatives.FlavourBSD {
		t.Errorf("a binary of the same path, time and size should keep its cached flavour, got %q", got)
	}

	later := info.ModTime().Add(time.Hour)

	err = os.Chtimes(path, later, later)
	if err != nil {
		t.Fatal(err)
	}

	third := &alternatives.Machine{Path: stubs, CacheFile: cache}
	if got := third.Flavour(context.Background(), sed, path); got != "" {
		t.Errorf("a changed binary should be read again, got %q", got)
	}
}

func TestTheLineNamesOnlyTheSubstitutionsNeeded(t *testing.T) {
	cases := []struct {
		name      string
		installed []string
		gnu       []string
		line      string
	}{
		{"timeout installed and GNU tools", []string{"timeout", "gtimeout", "perl", "sed", "date"}, []string{"sed", "date"}, ""},
		{"timeout missing, gtimeout first", []string{"gtimeout", "uu-timeout", "perl"}, nil, "this machine: timeout -> gtimeout"},
		{"timeout missing, uu-timeout next", []string{"uu-timeout", "perl"}, nil, "this machine: timeout -> uu-timeout"},
		{"timeout missing, perl last", []string{"perl"}, nil, "this machine: timeout -> perl -e 'alarm N; exec @ARGV'"},
		{"nothing stands in", nil, nil, ""},
		{"BSD sed and date", []string{"gtimeout", "sed", "date"}, nil, "this machine: timeout -> gtimeout; sed and date are BSD"},
		{"BSD sed only", []string{"timeout", "sed", "date"}, []string{"date"}, "this machine: sed is BSD"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			machine := &alternatives.Machine{Path: test.FlavouredPath(t, tc.installed, tc.gnu)}

			if got := machine.Line(context.Background()); got != tc.line {
				t.Errorf("the line should name each substitution the machine needs and nothing else, got %q", got)
			}
		})
	}
}

func TestEveryLauncherAlternativeIsALauncher(t *testing.T) {
	programs := map[string]bool{}
	for _, launcher := range alternatives.Launchers() {
		programs[launcher.Program] = true
	}

	for _, entry := range alternatives.Table {
		if !entry.Launcher {
			continue
		}

		for _, alternative := range entry.Alternatives {
			if !programs[alternative.Program] {
				t.Errorf("every alternative of a launcher entry should launch, %s does not", alternative.Program)
			}
		}
	}
}
