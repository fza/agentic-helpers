package process_test

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/process"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestParseLstart(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatalf("loading a zone: %v", err)
	}

	cases := []struct {
		name     string
		text     string
		want     time.Time
		readable bool
	}{
		{
			name: "C locale text", text: "Sat Oct 24 14:35:50 2026\n",
			want: time.Date(2026, time.October, 24, 14, 35, 50, 0, berlin), readable: true,
		},
		{
			name: "a one-digit day padded with a space", text: "Mon Oct  5 09:01:02 2026",
			want: time.Date(2026, time.October, 5, 9, 1, 2, 0, berlin), readable: true,
		},
		{name: "German text", text: "Sa. 24 Okt. 14:35:50 2026"},
		{name: "nothing", text: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, readable := process.ParseLstart(tc.text, berlin)
			if readable != tc.readable || !got.Equal(tc.want) {
				t.Errorf("the text should read as its instant in the given zone, or not at all, got %v %v", got, readable)
			}
		})
	}
}

func TestTheTableReadsTheKernelNeverPs(t *testing.T) {
	bin := t.TempDir()
	test.WriteFile(t, filepath.Join(bin, "ps"), test.Fixture(t, "process/failing-ps.sh"))

	err := os.Chmod(filepath.Join(bin, "ps"), 0o755)
	if err != nil {
		t.Fatalf("making the fake ps runnable: %v", err)
	}

	t.Setenv("PATH", bin)

	ctx := context.Background()

	parent, name := process.Table{}.ParentAndName(ctx, os.Getpid())
	if parent != os.Getppid() || name == "" {
		t.Errorf("the table should name this process and its parent without ps, got %d %q", parent, name)
	}

	started, found := process.Table{}.StartTime(ctx, os.Getpid())
	if !found || started.After(time.Now()) || time.Since(started) > time.Hour || started.Nanosecond() != 0 {
		t.Errorf("the table should read this process's start in whole seconds without ps, got %v %v", started, found)
	}

	parent, name = process.Table{}.ParentAndName(ctx, test.DeadPID)
	_, found = process.Table{}.StartTime(ctx, test.DeadPID)

	if parent != 0 || name != "" || found {
		t.Error("a pid nothing runs should read as gone")
	}
}

func TestProcargsCommand(t *testing.T) {
	procargs := func(count uint32, rest string) []byte {
		return append(binary.NativeEndian.AppendUint32(nil, count), rest...)
	}

	cases := []struct {
		name       string
		raw        []byte
		executable string
		args       []string
		readable   bool
	}{
		{
			name:       "the executable padded with NULs, then the arguments, then the environment",
			raw:        procargs(2, "/opt/claude/2.1.318\x00\x00\x00\x00claude\x00-p\x00HOME=/Users/dev\x00\x00ptr_munge=\x00"),
			executable: "/opt/claude/2.1.318",
			args:       []string{"claude", "-p"},
			readable:   true,
		},
		{
			name:       "an argument holding an equals sign",
			raw:        procargs(2, "/bin/env\x00env\x00A=1\x00B=2\x00"),
			executable: "/bin/env",
			args:       []string{"env", "A=1"},
			readable:   true,
		},
		{name: "no arguments counted", raw: procargs(0, "/bin/sh\x00"), executable: "/bin/sh", args: []string{}, readable: true},
		{name: "arguments cut short", raw: procargs(3, "/bin/sh\x00sh\x00-c\x00")},
		{name: "an executable path never ended", raw: procargs(1, "/bin/sh")},
		{name: "an executable path never ended, no arguments counted", raw: procargs(0, "/bin/sh")},
		{name: "no argument count", raw: []byte{1, 0}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			executable, args, readable := process.ProcargsCommand(tc.raw)
			if readable != tc.readable || executable != tc.executable || !slices.Equal(args, tc.args) {
				t.Errorf("the command should read the executable, then as many arguments as counted, got %q %q %v",
					executable, args, readable)
			}
		})
	}
}

func TestTheTableReadsTheCommandAProcessStartedWith(t *testing.T) {
	executable, args, readable := process.Table{}.Command(context.Background(), os.Getpid())
	if !readable || len(args) == 0 || !slices.Equal(args, os.Args) || executable == "" {
		t.Errorf("this process's command should read from the kernel, got %q %q %v", executable, args, readable)
	}

	_, _, readable = process.Table{}.Command(context.Background(), test.DeadPID)
	if readable {
		t.Error("a pid nothing runs should have no command")
	}
}

func TestProcfsReadsTheCommand(t *testing.T) {
	root := t.TempDir()
	test.FixtureTree(t, "process/proc", root)

	err := os.Symlink("/usr/bin/node", filepath.Join(root, "4242", "exe"))
	if err != nil {
		t.Fatalf("linking the executable: %v", err)
	}

	procfs := process.Procfs{Root: os.DirFS(root)}

	executable, args, readable := procfs.Command(4242)
	want := []string{"node", "/usr/lib/node_modules/@anthropic-ai/claude-code/cli.js", "--resume"}
	if !readable || executable != "/usr/bin/node" || !slices.Equal(args, want) {
		t.Errorf("the command should be where exe leads and cmdline split at every NUL, got %q %q %v",
			executable, args, readable)
	}

	_, _, readable = procfs.Command(4343)
	if readable {
		t.Error("a process whose exe does not read should have no command")
	}

	err = os.Mkdir(filepath.Join(root, "7000"), 0o755)
	if err != nil {
		t.Fatalf("creating a process without arguments: %v", err)
	}

	err = os.Symlink("/usr/bin/node", filepath.Join(root, "7000", "exe"))
	if err != nil {
		t.Fatalf("linking the executable: %v", err)
	}

	_, _, readable = procfs.Command(7000)
	if readable {
		t.Error("a process whose cmdline does not read should have no command")
	}

	test.WriteFile(t, filepath.Join(root, "7100", "cmdline"), "node\x00")

	_, _, readable = procfs.Command(7100)
	if readable {
		t.Error("a process whose exe does not read, as another user's, should have no command")
	}

	_, _, readable = procfs.Command(999)
	if readable {
		t.Error("a pid not in the tree should have no command")
	}
}

func TestProcfsReadsTheProcTree(t *testing.T) {
	booted := int64(1_761_000_000)

	cases := []struct {
		name    string
		tree    string
		pid     int
		parent  int
		command string
		started time.Time
		found   bool
	}{
		{
			name: "a plain name", tree: "process/proc", pid: 4242,
			parent: 4000, command: "bash", started: time.Unix(booted+1234, 0), found: true,
		},
		{
			name: "a name with spaces and parentheses", tree: "process/proc", pid: 4343,
			parent: 4242, command: "tmux: (a) b) c", started: time.Unix(booted+2500, 0), found: true,
		},
		{name: "a stat cut inside the name", tree: "process/proc", pid: 4444},
		{name: "a stat cut before the start time", tree: "process/proc", pid: 4646},
		{name: "a name never closed", tree: "process/proc", pid: 4848},
		{name: "a parent that is no number", tree: "process/proc", pid: 4747, started: time.Unix(booted+50, 0), found: true},
		{name: "a start time that is no number", tree: "process/proc", pid: 4545, parent: 4242, command: "sleeper"},
		{name: "a zombie awaiting its reaping", tree: "process/proc", pid: 4949},
		{name: "a dead process", tree: "process/proc", pid: 5050},
		{name: "a pid not in the tree", tree: "process/proc", pid: 999},
		{name: "no boot time", tree: "process/proc-without-btime", pid: 4242, parent: 4000, command: "bash"},
		{name: "a boot time that is no number", tree: "process/proc-with-bad-btime", pid: 4242, parent: 4000, command: "bash"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			test.FixtureTree(t, tc.tree, root)

			procfs := process.Procfs{Root: os.DirFS(root)}

			parent, command := procfs.ParentAndName(tc.pid)
			if parent != tc.parent || command != tc.command {
				t.Errorf("the parent and the whole name should read from the stat, got %d %q", parent, command)
			}

			started, found := procfs.StartTime(tc.pid)
			if found != tc.found || !started.Equal(tc.started) {
				t.Errorf("the start should be the boot time plus whole seconds of ticks, or none, got %v %v", started, found)
			}
		})
	}
}

func TestProcfsReadsTheControllingTerminal(t *testing.T) {
	root := t.TempDir()
	test.FixtureTree(t, "process/proc", root)

	procfs := process.Procfs{Root: os.DirFS(root)}

	cases := []struct {
		name  string
		pid   int
		major uint32
		minor uint32
		found bool
	}{
		{name: "a pseudo-terminal", pid: 4242, major: 136, found: true},
		{name: "no terminal", pid: 4343},
		{name: "a stat cut before the terminal", pid: 4444},
		{name: "a dead process", pid: 5050},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			major, minor, found := procfs.TerminalDevice(tc.pid)
			if found != tc.found || major != tc.major || minor != tc.minor {
				t.Errorf("the terminal should unpack from the stat's seventh field, or be none, got %d %d %v", major, minor,
					found)
			}
		})
	}
}

func TestATerminalDeviceTakesItsPlatformsName(t *testing.T) {
	darwin := []struct {
		device uint32
		want   string
	}{
		{device: 16<<24 | 1, want: "/dev/ttys001"},
		{device: 16<<24 | 42, want: "/dev/ttys042"},
		{device: 16<<24 | 1234, want: "/dev/ttys1234"},
		{device: 4<<24 | 1, want: ""},
	}

	for _, tc := range darwin {
		if got := process.DarwinTerminal(tc.device); got != tc.want {
			t.Errorf("a darwin device should name its pseudo-terminal, or nothing, got %q", got)
		}
	}

	linux := []struct {
		major uint32
		minor uint32
		want  string
	}{
		{major: 136, minor: 3, want: "/dev/pts/3"},
		{major: 136, minor: 4096, want: "/dev/pts/4096"},
		{major: 137, minor: 2, want: "/dev/pts/258"},
		{major: 4, minor: 2, want: "/dev/tty2"},
		{major: 4, minor: 64, want: ""},
		{major: 5, minor: 0, want: ""},
	}

	for _, tc := range linux {
		if got := process.LinuxTerminal(tc.major, tc.minor); got != tc.want {
			t.Errorf("a linux device should name its pseudo-terminal or console, or nothing, got %q", got)
		}
	}
}

func TestTheTableNamesNoTerminalForAProcessGone(t *testing.T) {
	if got := (process.Table{}).Terminal(context.Background(), test.DeadPID); got != "" {
		t.Errorf("a pid nothing runs should have no terminal, got %q", got)
	}
}

func TestADarwinProcessOnItsWayOutIsGone(t *testing.T) {
	cases := []struct {
		name    string
		stat    int8
		flags   int32
		exiting bool
	}{
		{name: "running", stat: 2},
		{name: "sleeping with other flags", stat: 3, flags: 0x4004},
		{name: "a zombie", stat: 5, exiting: true},
		{name: "inside exit", stat: 2, flags: 0x00002000, exiting: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if process.Exiting(tc.stat, tc.flags) != tc.exiting {
				t.Error("only a zombie or a process inside exit should read as on its way out")
			}
		})
	}
}

func TestTheTableNamesTheExecutableOfEachMatchingProcess(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("finding this test's executable: %v", err)
	}

	_, name := process.Table{}.ParentAndName(context.Background(), os.Getpid())

	paths, err := process.Table{}.Executables(context.Background(), name)
	if err != nil {
		t.Fatalf("listing executables: %v", err)
	}

	wanted, _ := filepath.EvalSymlinks(self)
	found := false

	for _, path := range paths {
		resolved, _ := filepath.EvalSymlinks(path)
		found = found || resolved == wanted
	}

	if !found {
		t.Errorf("the executable of this very process should be listed, got %v", paths)
	}

	none, err := process.Table{}.Executables(context.Background(), "no-such-process-name-")
	if err != nil || len(none) != 0 {
		t.Errorf("a prefix no process carries should list nothing, got %v %v", none, err)
	}
}

func TestProcfsNamesTheExecutableOfEachMatchingProcess(t *testing.T) {
	root := t.TempDir()
	test.FixtureTree(t, "process/agentic", root)

	for pid, target := range map[string]string{"5100": "/opt/old/agentic-memory", "5300": "/bin/bash"} {
		err := os.Symlink(target, filepath.Join(root, pid, "exe"))
		if err != nil {
			t.Fatalf("linking the executable of %s: %v", pid, err)
		}
	}

	paths, err := process.Procfs{Root: os.DirFS(root)}.Executables("agentic-")
	if err != nil {
		t.Fatalf("listing executables: %v", err)
	}

	if len(paths) != 1 || paths[0] != "/opt/old/agentic-memory" {
		t.Errorf("only a matching process whose executable reads should be listed, got %v", paths)
	}
}
