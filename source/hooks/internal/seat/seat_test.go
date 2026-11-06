package seat_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/process"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestOfSession(t *testing.T) {
	store := t.TempDir()
	test.ClaimSeat(t, store, "reviewer", test.Session)
	test.ClaimSeat(t, store, "drive", test.OtherSession)
	test.WriteFile(t, filepath.Join(store, "seats", "broken.json"), "{not json")
	test.WriteFile(t, filepath.Join(store, "seats", "released.json"), `{"session_id": ""}`)
	test.WriteFile(t, filepath.Join(store, "seats", "aide.json"), `{"session_id": "`+test.Session+`", "agent": "a1"}`)

	dir := seat.Dir(store)

	cases := []struct {
		name    string
		session string
		want    string
	}{
		{name: "holder", session: test.Session, want: "reviewer"},
		{name: "another holder", session: test.OtherSession, want: "drive"},
		{name: "no claim", session: "c0ffee00-0000-4000-8000-000000000000"},
		{name: "no session"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := seat.OfSession(dir, tc.session)
			if got != tc.want {
				t.Error("the session should read as the seat its claim names, and as none without one")
			}
		})
	}
}

func TestHeld(t *testing.T) {
	store := t.TempDir()
	test.ClaimSeat(t, store, "reviewer", test.Session)
	test.WriteFile(t, filepath.Join(store, "seats", "broken.json"), "{not json")
	test.WriteFile(t, filepath.Join(store, "seats", "released.json"), `{"session_id": ""}`)
	test.WriteFile(t, filepath.Join(store, "seats", "Bad Name.json"), `{"session_id": "x"}`)

	held := seat.Held(seat.Dir(store))

	if len(held) != 1 || held["reviewer"].SessionID != test.Session {
		t.Errorf("only a well-named claim naming a session should count as held, got: %v", held)
	}

	if len(seat.Held(filepath.Join(store, "missing"))) != 0 {
		t.Error("a store without claims should hold no seat")
	}
}

func TestOf(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name  string
		claim func(t *testing.T, store string)
		want  string
	}{
		{
			name:  "the owning process's live claim is its seat",
			claim: func(t *testing.T, store string) { test.ClaimSeat(t, store, "main", test.Session) },
			want:  "main",
		},
		{
			name:  "a claim whose holder is gone is no seat",
			claim: func(t *testing.T, store string) { test.ClaimDeadSeat(t, store, "main", test.Session) },
		},
		{
			name: "a live claim held for a subagent of the owner's session is no seat of the owner",
			claim: func(t *testing.T, store string) {
				test.WriteFile(t, filepath.Join(store, "seats", "helper.json"),
					`{"session_id":"`+test.Session+`","pid":200,"agent":"a1"}`)
			},
		},
		{
			name: "a claim naming the owner pid with another start time is a reused pid",
			claim: func(t *testing.T, store string) {
				test.WriteFile(t, filepath.Join(store, "seats", "main.json"),
					`{"session_id":"x","pid":200,"started":"Mon Jan  1 00:00:00 2024"}`)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := t.TempDir()
			tc.claim(t, store)

			got, _ := seat.Of(ctx, seat.Dir(store), test.NewProcesses(), "claude")
			if got != tc.want {
				t.Error("a process should hold exactly the seat its owner's live claim names")
			}
		})
	}
}

func TestOwnerWalksPastTheShell(t *testing.T) {
	if seat.Owner(context.Background(), test.NewProcesses(), "claude") != test.OwnerPID {
		t.Error("the owner should be the nearest claude ancestor, past the shell")
	}

	if seat.Owner(context.Background(), test.NewProcesses(), "nothing") != test.ShellPID {
		t.Error("without an owner ancestor the immediate parent should stand in")
	}
}

func TestTheOwnerIsClaudeCodeWhateverItsExecutableIsNamed(t *testing.T) {
	withOwner := func(name string, change func(owner *test.Process)) func() *test.Processes {
		return func() *test.Processes {
			processes := test.NewProcessesRunningAs(name)
			owner := processes.Table[test.OwnerPID]
			change(&owner)
			processes.Table[test.OwnerPID] = owner

			return processes
		}
	}

	cases := []struct {
		name  string
		tree  func() *test.Processes
		owner int
	}{
		{
			name:  "the default tree, Claude Code named claude",
			tree:  test.NewProcesses,
			owner: test.OwnerPID,
		},
		{
			name:  "the native installer's version-named executable",
			tree:  func() *test.Processes { return test.NewProcessesRunningAs(test.NativeVersion) },
			owner: test.OwnerPID,
		},
		{
			name:  "node running the npm package's script",
			tree:  func() *test.Processes { return test.NewProcessesRunningAs("node") },
			owner: test.OwnerPID,
		},
		{
			name: "node running the npm package's script after its own options",
			tree: withOwner("node", func(owner *test.Process) {
				owner.Args = []string{"node", "--no-warnings", test.NpmScript, "--resume"}
			}),
			owner: test.OwnerPID,
		},
		{
			name: "a version-named executable outside the native installer's versions",
			tree: withOwner(test.NativeVersion, func(owner *test.Process) {
				owner.Executable = "/opt/tools/" + test.NativeVersion
			}),
		},
		{
			name: "a version-named executable of another version than its name",
			tree: withOwner(test.NativeVersion, func(owner *test.Process) {
				owner.Executable = "/Users/dev/.local/share/claude/versions/2.1.300"
			}),
		},
		{
			name: "a name shaped like no version, run from the native installer's versions",
			tree: withOwner("latest", func(owner *test.Process) {
				owner.Executable = "/Users/dev/.local/share/claude/versions/latest"
			}),
		},
		{
			name: "node running another script",
			tree: withOwner("node", func(owner *test.Process) {
				owner.Args = []string{"node", "/usr/local/lib/node_modules/some-mcp-server/index.js"}
			}),
		},
		{
			name: "node running no script",
			tree: withOwner("node", func(owner *test.Process) {
				owner.Args = []string{"node", "--version"}
			}),
		},
		{
			name: "node whose command does not read",
			tree: withOwner("node", func(owner *test.Process) {
				owner.Hidden = true
			}),
		},
		{
			name: "a node MCP server between the caller and a claude Claude Code",
			tree: func() *test.Processes {
				processes := test.NewProcesses()
				shell := processes.Table[test.ShellPID]
				shell.Parent = test.OtherOwnerPID
				processes.Table[test.ShellPID] = shell
				processes.Table[test.OtherOwnerPID] = test.Process{
					Parent: test.OwnerPID, Name: "node", Args: []string{"node", "/opt/mcp/server.js"},
				}

				return processes
			},
			owner: test.OwnerPID,
		},
		{
			name: "a Claude Code started inside another Claude Code's session",
			tree: func() *test.Processes {
				processes := test.NewProcesses()
				owner := processes.Table[test.OwnerPID]
				owner.Parent = test.OtherOwnerPID
				processes.Table[test.OwnerPID] = owner
				processes.Table[test.OtherOwnerPID] = test.Process{Parent: 1, Name: "claude"}

				return processes
			},
			owner: test.OwnerPID,
		},
		{
			name: "no Claude Code above the caller",
			tree: test.NewProcessesOutsideClaude,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if seat.OwnerAncestor(context.Background(), tc.tree(), "claude") != tc.owner {
				t.Error("the owner should be the nearest ancestor that is Claude Code by name, by the native installer's executable or by the npm package's script")
			}
		})
	}
}

func TestTheOwnerIsFoundThroughTheLinksItRunsBy(t *testing.T) {
	root := t.TempDir()
	versions := filepath.Join(root, "share", "claude", "versions")
	npm := filepath.Join(root, "lib", "node_modules", "@anthropic-ai", "claude-code")
	bin := filepath.Join(root, "bin")

	test.WriteFile(t, filepath.Join(versions, test.NativeVersion), "")
	test.WriteFile(t, filepath.Join(npm, "cli.js"), "")

	err := os.Mkdir(bin, 0o755)
	if err != nil {
		t.Fatalf("creating %s: %v", bin, err)
	}

	for target, link := range map[string]string{
		filepath.Join(versions, test.NativeVersion): filepath.Join(bin, "claude"),
		filepath.Join(npm, "cli.js"):                filepath.Join(bin, "claude-npm"),
	} {
		err := os.Symlink(target, link)
		if err != nil {
			t.Fatalf("linking %s: %v", link, err)
		}
	}

	native := test.NewProcessesRunningAs(test.NativeVersion)
	owner := native.Table[test.OwnerPID]
	owner.Executable = filepath.Join(bin, "claude")
	native.Table[test.OwnerPID] = owner

	if seat.OwnerAncestor(context.Background(), native, "claude") != test.OwnerPID {
		t.Error("a version-named Claude Code started through a link should be found where the link leads")
	}

	node := test.NewProcessesRunningAs("node")
	owner = node.Table[test.OwnerPID]
	owner.Args = []string{"node", filepath.Join(bin, "claude-npm")}
	node.Table[test.OwnerPID] = owner

	if seat.OwnerAncestor(context.Background(), node, "claude") != test.OwnerPID {
		t.Error("node running npm's claude link should be found where the link leads")
	}
}

func TestAClaimRecordsTheNameItsHolderRunsUnder(t *testing.T) {
	ctx := context.Background()

	owner, process := seat.Holder(ctx, test.NewProcessesRunningAs("2.1.318"), "claude")
	if owner != test.OwnerPID || process != "2.1.318" {
		t.Errorf("a claim should name the Claude Code process with the name it runs under, got %d %q", owner, process)
	}

	owner, process = seat.Holder(ctx, test.NewProcessesOutsideClaude(), "claude")
	if owner != test.ShellPID || process != "" {
		t.Errorf("without an owner the parent should stand in under no name, got %d %q", owner, process)
	}
}

func TestAliveHoldsAClaimToTheNameItRecords(t *testing.T) {
	processes := test.NewProcessesRunningAs("2.1.318")

	cases := []struct {
		name    string
		process string
		start   string
		alive   bool
	}{
		{name: "the recorded name at the recorded start", process: "2.1.318", start: test.OwnerStarted, alive: true},
		{name: "the recorded name at another start", process: "2.1.318", start: "2026-09-25T12:00:01Z"},
		{name: "another recorded name at the recorded start", process: "node", start: test.OwnerStarted},
		{name: "no recorded name, held to the owner name", start: test.OwnerStarted},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			held := seat.Claim{SessionID: test.Session, PID: test.OwnerPID, Started: tc.start, Process: tc.process}

			if seat.Alive(context.Background(), processes, "claude", held) != tc.alive {
				t.Error("a claim should read alive only while its pid runs under the name it records, from the start it records")
			}
		})
	}
}

func TestUnderOwner(t *testing.T) {
	cases := []struct {
		name      string
		processes *test.Processes
		under     bool
	}{
		{name: "a call from a shell a claude process started", processes: test.NewProcesses(), under: true},
		{name: "a call from the user's own shell", processes: test.NewProcessesOutsideClaude()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if seat.UnderOwner(context.Background(), tc.processes, "claude") != tc.under {
				t.Error("only a call with a claude ancestor should count as under the owner")
			}
		})
	}
}

func TestRemoveKeepsTheLastClaim(t *testing.T) {
	store := t.TempDir()
	test.ClaimSeat(t, store, "main", test.Session)
	dir := seat.Dir(store)

	err := seat.Remove(store, dir, "main")
	if err != nil {
		t.Fatalf("removing a claim should succeed: %v", err)
	}

	_, held := seat.Read(dir, "main")
	if held {
		t.Error("a removed claim should leave the seat free")
	}

	last, found := seat.ReadLast(dir, "main")
	if !found || last.SessionID != test.Session {
		t.Error("a removed claim should survive as the seat's last claim")
	}

	if len(seat.Known(dir)) != 1 {
		t.Error("a seat with only a last claim should still be known")
	}
}

func TestValidName(t *testing.T) {
	cases := []struct {
		name  string
		valid bool
	}{
		{name: "main", valid: true},
		{name: "drive-2", valid: true},
		{name: "haupt", valid: true},
		{name: "главная", valid: true},
		{name: "メイン", valid: true},
		{name: "मुख्य", valid: true},
		{name: "café", valid: true},
		{name: "a" + strings.Repeat("b", 23), valid: true},
		{name: "a" + strings.Repeat("b", 24)},
		{name: "2main"},
		{name: "-main"},
		{name: "main seat"},
		{name: "main/x"},
		{name: "../main"},
		{name: "Main"},
		{name: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if seat.ValidName(tc.name) != tc.valid {
				t.Errorf("a seat name should be a letter then up to 23 letters, marks, digits or dashes in any script, spelled normal")
			}
		})
	}
}

func TestNormalSpellsANameOneWay(t *testing.T) {
	decomposed := "cafe\u0301"

	if seat.Normal(decomposed) != "café" || seat.Normal("  Café ") != "café" || seat.Normal("ГЛАВНАЯ") != "главная" {
		t.Error("a name typed decomposed, padded or in capitals should normalise to one spelling")
	}

	if !seat.ValidName(seat.Normal(decomposed)) {
		t.Error("a decomposed name should be valid once normalised")
	}
}

func TestANameOutsideTheSeatsDirectoryIsRefused(t *testing.T) {
	store := t.TempDir()
	dir := seat.Dir(store)
	test.WriteFile(t, filepath.Join(store, "outside.json"), `{"session_id":"x"}`)

	_, found := seat.Read(dir, "../outside")
	if found {
		t.Error("a name escaping the seats directory should read as no claim")
	}

	if seat.Write(store, dir, "../outside", seat.Claim{SessionID: "y"}) == nil {
		t.Error("a name escaping the seats directory should not be written")
	}

	if seat.Remove(store, dir, "../outside") == nil || !test.Exists(t, filepath.Join(store, "outside.json")) {
		t.Error("a name escaping the seats directory should not remove anything")
	}
}

// The earlier releases read `started` from `ps -o lstart=`, whole seconds of
// the same kernel start time: RFC 3339 in UTC, and before that C locale text.
func TestAClaimOfAnEarlierReleaseReadsAgainstTheKernel(t *testing.T) {
	ctx := context.Background()
	processes := process.Table{}
	self := processes.Self()

	_, name := processes.ParentAndName(ctx, self)

	started, found := processes.StartTime(ctx, self)
	if !found {
		t.Fatal("the kernel should report this process's start")
	}

	cases := []struct {
		name  string
		pid   int
		start string
		alive bool
	}{
		{name: "RFC 3339 at the process start", pid: self, start: started.UTC().Format(time.RFC3339), alive: true},
		{name: "C locale ps text at the process start", pid: self, start: started.Local().Format(process.LstartLayout), alive: true},
		{name: "RFC 3339 a second later", pid: self, start: started.Add(time.Second).UTC().Format(time.RFC3339)},
		{name: "C locale ps text a second earlier", pid: self, start: started.Add(-time.Second).Local().Format(process.LstartLayout)},
		{name: "ps text in another locale", pid: self, start: "Do. 25 Sep. 14:00:00 2026", alive: true},
		{name: "the pid gone", pid: test.DeadPID, start: started.UTC().Format(time.RFC3339)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			held := seat.Claim{SessionID: test.Session, PID: tc.pid, Started: tc.start}

			if seat.Alive(ctx, processes, name, held) != tc.alive {
				t.Error("a claim an earlier release wrote should read alive exactly while its process runs from that start")
			}
		})
	}
}

func TestAliveReadsStartedAsAnInstant(t *testing.T) {
	processes := test.NewProcesses()
	inC := test.OwnerStart.In(time.Local).Format(process.LstartLayout)
	otherInC := test.OwnerStart.Add(time.Minute).In(time.Local).Format(process.LstartLayout)

	cases := []struct {
		name  string
		pid   int
		start string
		alive bool
	}{
		{name: "RFC 3339 at the process start", pid: test.OwnerPID, start: test.OwnerStarted, alive: true},
		{name: "RFC 3339 at another instant", pid: test.OwnerPID, start: "2026-09-25T12:00:01Z"},
		{name: "RFC 3339 in another zone at the process start", pid: test.OwnerPID, start: "2026-09-25T14:00:00+02:00", alive: true},
		{name: "C locale ps text at the process start", pid: test.OwnerPID, start: inC, alive: true},
		{name: "C locale ps text at another instant", pid: test.OwnerPID, start: otherInC},
		{name: "ps text in another locale, the pid running claude", pid: test.OwnerPID, start: "Do. 25 Sep. 14:00:00 2026", alive: true},
		{name: "no start recorded", pid: test.OwnerPID, alive: true},
		{name: "the pid running something else", pid: test.ShellPID, start: "Do. 25 Sep. 14:00:00 2026"},
		{name: "the pid gone", pid: test.DeadPID, start: test.OwnerStarted},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			held := seat.Claim{SessionID: test.Session, PID: tc.pid, Started: tc.start}

			if seat.Alive(context.Background(), processes, "claude", held) != tc.alive {
				t.Errorf("liveness should follow the process and its start instant, never the locale of its text")
			}
		})
	}
}
