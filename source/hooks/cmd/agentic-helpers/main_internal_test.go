package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("finding the test program: %v", err)
	}

	base := t.TempDir()
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
		{about: "capture names its flags", name: "agentic-capture", args: []string{"--help"},
			stdout: "usage: agentic-capture <draft.md>"},
		{about: "capture refuses a draft no graph governs", name: "agentic-capture", args: []string{"draft.md"},
			code: 1, stderr: "no .sdd graph governs this draft or this directory"},
		{about: "capture reads its own switch", name: "agentic-capture", args: []string{"draft.md"},
			extra: []string{"CLAUDE_PLUGIN_OPTION_CAPTURE_ENABLED=false"}, code: 1,
			stderr: "agentic-capture: off on this machine (plugin option capture_enabled)"},
		{about: "agentic-helpers checks the pieces", name: "agentic-helpers", args: []string{"check"},
			stdout: "memory          on\n"},
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
