package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

// programRole makes the test binary run as the program itself: started under
// a link named for a command, it is the folded program under that name.
const programRole = "AGENTIC_HELPERS_TEST_PROGRAM"

func TestMain(m *testing.M) {
	if os.Getenv(programRole) != "" {
		main()
	}

	os.Exit(m.Run())
}

type ran struct {
	code   int
	stdout string
	stderr string
}

// runAs starts the program through a link named name, in a scratch home and
// a project that is no git work tree, with a git on the PATH saying so.
func runAs(t *testing.T, name string, extra []string, args ...string) ran {
	t.Helper()

	return runIn(t, t.TempDir(), "", name, extra, args...)
}

// runIn is runAs in base, whose project folder a test may have filled, with
// stdin as the program's input.
func runIn(t *testing.T, base string, stdin string, name string, extra []string, args ...string) ran {
	t.Helper()

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("finding the test program: %v", err)
	}

	bin := filepath.Join(base, "bin")
	tools := filepath.Join(base, "tools")
	project := filepath.Join(base, "project")

	test.WriteFile(t, filepath.Join(tools, "git"), test.Fixture(t, "folded/git"))

	err = os.Chmod(filepath.Join(tools, "git"), 0o700)
	if err != nil {
		t.Fatalf("making the fake git runnable: %v", err)
	}

	for _, dir := range []string{bin, project} {
		err = os.MkdirAll(dir, 0o700)
		if err != nil {
			t.Fatalf("creating %s: %v", dir, err)
		}
	}

	err = os.Symlink(self, filepath.Join(bin, name))
	if err != nil {
		t.Fatalf("linking the program under %s: %v", name, err)
	}

	command := exec.Command(filepath.Join(bin, name), args...)
	command.Dir = project
	if stdin != "" {
		command.Stdin = strings.NewReader(stdin)
	}

	command.Env = append([]string{
		programRole + "=1",
		"PATH=" + tools + ":/usr/bin:/bin",
		"HOME=" + filepath.Join(base, "home"),
		"TMPDIR=" + base,
		"CLAUDE_PROJECT_DIR=" + project,
	}, extra...)

	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr

	err = command.Run()

	var exited *exec.ExitError
	if err != nil && !errors.As(err, &exited) {
		t.Fatalf("running %s: %v", name, err)
	}

	return ran{code: command.ProcessState.ExitCode(), stdout: stdout.String(), stderr: stderr.String()}
}

func TestEachNameRunsItsOwnCommand(t *testing.T) {
	cases := []struct {
		about  string
		name   string
		extra  []string
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{about: "memory lists its subcommands", name: "agentic-memory", args: []string{"help"},
			stdout: "usage: agentic-memory <subcommand>\n"},
		{about: "memory refuses a search outside a work tree", name: "agentic-memory", args: []string{"search", "--term", "x"},
			code: 1, stderr: "is not inside a git work tree; memory works only in one"},
		{about: "memory reads its own switch", name: "agentic-memory", args: []string{"who"},
			extra: []string{"CLAUDE_PLUGIN_OPTION_MEMORY_ENABLED=false"}, code: 1,
			stderr: "agentic-memory: off on this machine (plugin option memory_enabled)"},
		{about: "tools lists its subcommands", name: "agentic-tools", args: []string{"help"},
			stdout: "usage: agentic-tools <subcommand>\n"},
		{about: "tools refuses a listing outside a work tree", name: "agentic-tools", args: []string{"list", "--json"},
			code: 1, stderr: "is not inside a git work tree; the tool library works only in one"},
		{about: "tools reads its own switch", name: "agentic-tools", args: []string{"list"},
			extra: []string{"CLAUDE_PLUGIN_OPTION_TOOLS_ENABLED=false"}, code: 1,
			stderr: "agentic-tools: off on this machine (plugin option tools_enabled)"},
		{about: "scratch names its modes", name: "agentic-scratch", args: []string{"now"},
			code: 1, stderr: "usage: agentic-scratch end|start"},
		{about: "agentic-helpers checks the pieces", name: "agentic-helpers", args: []string{"check"},
			stdout: "memory       on\n"},
		{about: "agentic-helpers bounds a command", name: "agentic-helpers", args: []string{"bound", "5s", "/bin/echo", "bounded"},
			stdout: "bounded\n"},
		{about: "any other name is agentic-helpers", name: "agentic-helpers.dev", args: []string{"bound", "5s", "/bin/echo", "bounded"},
			stdout: "bounded\n"},
	}

	for _, tc := range cases {
		t.Run(tc.about, func(t *testing.T) {
			got := runAs(t, tc.name, tc.extra, tc.args...)

			if got.code != tc.code || !strings.Contains(got.stdout, tc.stdout) || !strings.Contains(got.stderr, tc.stderr) {
				t.Errorf("the name should run its own command as that command runs alone, got: %+v", got)
			}
		})
	}
}

func TestScratchSweepsTheProjectUnderItsName(t *testing.T) {
	cases := []struct {
		about  string
		mode   string
		extra  []string
		stdin  string
		age    time.Duration
		swept  bool
		stdout string
	}{
		{about: "end removes the ending session's folder", mode: "end",
			stdin: `{"session_id": "` + test.Session + `"}`, swept: true, stdout: "swept "},
		{about: "start reaps a session folder a week idle", mode: "start",
			age: 8 * 24 * time.Hour, swept: true, stdout: "Scratch: reaped 1 session directory(s) nobody came back to."},
		{about: "start keeps a session folder in use", mode: "start"},
		{about: "the sweep reads its own switch", mode: "end", extra: []string{"CLAUDE_PLUGIN_OPTION_SCRATCH_ENABLED=false"},
			stdin: `{"session_id": "` + test.Session + `"}`},
	}

	for _, tc := range cases {
		t.Run(tc.about, func(t *testing.T) {
			base := t.TempDir()
			project := filepath.Join(base, "project")
			held := filepath.Join(project, ".tmp", "seats", test.Session)
			theirs := filepath.Join(project, ".tmp", "seats", test.OtherSession)

			for _, folder := range []string{held, theirs} {
				test.WriteFile(t, filepath.Join(folder, "probe.txt"), "scratch")
			}

			if tc.age > 0 {
				test.Age(t, held, tc.age)
			}

			got := runIn(t, base, tc.stdin, "agentic-scratch", tc.extra, tc.mode)

			if got.code != 0 || !strings.Contains(got.stdout, tc.stdout) {
				t.Errorf("the sweep should run as agentic-scratch runs alone, got: %+v", got)
			}

			if test.Exists(t, held) == tc.swept {
				t.Errorf("the session's folder should go exactly where the sweep owes it, got: %+v", got)
			}

			if !test.Exists(t, theirs) {
				t.Error("another session's folder in use should stay")
			}
		})
	}
}

func TestAProcessStartedFromTheNamedPathRunsAsThatCommand(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("finding the test program: %v", err)
	}

	got := namedPath("agentic-memory")

	if filepath.Base(got) != "agentic-memory" || filepath.Dir(got) != filepath.Dir(executable) {
		t.Errorf("the named path should be the link beside the program under that name, got: %s", got)
	}
}
