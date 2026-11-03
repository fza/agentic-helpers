package sessionrecord_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/sessionrecord"
	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

var startedAt = time.Date(2026, time.November, 3, 9, 0, 0, 0, time.UTC)

type machine struct {
	env       sessionrecord.Env
	processes *test.Processes
	clock     *test.CountdownClock
}

func newMachine(t *testing.T, variables map[string]string) *machine {
	t.Helper()

	processes := test.NewProcesses()
	owner := processes.Table[test.OwnerPID]
	owner.Terminal = "/dev/ttys004"
	processes.Table[test.OwnerPID] = owner

	clock := &test.CountdownClock{At: startedAt}

	return &machine{
		env: sessionrecord.Env{
			Dir:       sessionrecord.Dir(t.TempDir()),
			Getenv:    func(key string) string { return variables[key] },
			Processes: processes,
			Checkout:  test.CommonDir{Dir: "/work/project/.git"},
			OwnerName: "claude",
			Now:       clock.Now,
		},
		processes: processes,
		clock:     clock,
	}
}

func (m *machine) slots() *slots.File {
	return slots.Open(filepath.Dir(m.env.Dir), filepath.Join(m.env.Dir, "sessions.slots"))
}

func (m *machine) send(t *testing.T, session string, event string, agent string) string {
	t.Helper()

	payload, err := json.Marshal(map[string]string{
		"session_id": session, "hook_event_name": event, "agent_id": agent, "cwd": "/work/project",
	})
	if err != nil {
		t.Fatalf("encoding the payload: %v", err)
	}

	var captured test.Captured

	code := sessionrecord.Main(context.Background(), []string{"record"}, m.env, captured.Streams(string(payload)))
	if code != 0 || captured.Out.Len() != 0 {
		t.Fatalf("the record hook should exit 0 and print nothing, got %d %q", code, captured.Out.String())
	}

	return captured.Err.String()
}

type printed struct {
	Schema           int        `json:"schema"`
	SessionID        string     `json:"session_id"`
	PID              int        `json:"pid"`
	Started          *time.Time `json:"started"`
	TTY              string     `json:"tty"`
	TermProgram      string     `json:"term_program"`
	ITermSessionID   string     `json:"iterm_session_id"`
	TerminalEmulator string     `json:"terminal_emulator"`
	TmuxPane         string     `json:"tmux_pane"`
	BundleID         string     `json:"bundle_id"`
	Cwd              string     `json:"cwd"`
	GitCommonDir     string     `json:"git_common_dir"`
	Event            string     `json:"event"`
	EventAt          *time.Time `json:"event_at"`
	WaitingSince     *time.Time `json:"waiting_since"`
	WaitingFor       string     `json:"waiting_for"`
	Gone             bool       `json:"gone"`
}

func (m *machine) list(t *testing.T) []printed {
	t.Helper()

	var captured test.Captured

	code := sessionrecord.Main(context.Background(), []string{"sessions", "--json"}, m.env, captured.Streams(""))
	if code != 0 {
		t.Fatalf("sessions --json should succeed, got %d: %s", code, captured.Err.String())
	}

	var records []printed

	err := json.Unmarshal(captured.Out.Bytes(), &records)
	if err != nil {
		t.Fatalf("sessions --json should print a JSON array: %v\n%s", err, captured.Out.String())
	}

	return records
}

func (m *machine) only(t *testing.T) printed {
	t.Helper()

	records := m.list(t)
	if len(records) != 1 {
		t.Fatalf("one session should be listed, got %d", len(records))
	}

	return records[0]
}

func TestSessionStartNamesTheTerminalVariablesEachTerminalSets(t *testing.T) {
	cases := []struct {
		name      string
		variables map[string]string
		want      printed
	}{
		{
			name: "iTerm",
			variables: map[string]string{
				"TERM_PROGRAM": "iTerm.app", "ITERM_SESSION_ID": "w0t1p0:6C1F3E2A-1B2C-4D5E-8F90-0123456789AB",
				"__CFBundleIdentifier": "com.googlecode.iterm2",
			},
			want: printed{
				TermProgram: "iTerm.app", ITermSessionID: "w0t1p0:6C1F3E2A-1B2C-4D5E-8F90-0123456789AB",
				BundleID: "com.googlecode.iterm2",
			},
		},
		{
			name: "a JetBrains terminal",
			variables: map[string]string{
				"TERMINAL_EMULATOR": "JetBrains-JediTerm", "__CFBundleIdentifier": "com.jetbrains.goland",
			},
			want: printed{TerminalEmulator: "JetBrains-JediTerm", BundleID: "com.jetbrains.goland"},
		},
		{
			name:      "Terminal.app",
			variables: map[string]string{"TERM_PROGRAM": "Apple_Terminal", "__CFBundleIdentifier": "com.apple.Terminal"},
			want:      printed{TermProgram: "Apple_Terminal", BundleID: "com.apple.Terminal"},
		},
		{
			name: "tmux inside iTerm",
			variables: map[string]string{
				"TERM_PROGRAM": "tmux", "TMUX_PANE": "%3", "__CFBundleIdentifier": "com.googlecode.iterm2",
			},
			want: printed{TermProgram: "tmux", TmuxPane: "%3", BundleID: "com.googlecode.iterm2"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine(t, tc.variables)
			m.send(t, test.Session, "SessionStart", "")

			got := m.only(t)

			terminal := printed{
				TermProgram: got.TermProgram, ITermSessionID: got.ITermSessionID, TerminalEmulator: got.TerminalEmulator,
				TmuxPane: got.TmuxPane, BundleID: got.BundleID,
			}
			if terminal != tc.want {
				t.Errorf("the record should name the terminal variables the session runs under, got %+v", terminal)
			}

			if got.SessionID != test.Session || got.PID != test.OwnerPID || got.Started == nil ||
				!got.Started.Equal(test.OwnerStart) || got.TTY != "/dev/ttys004" {
				t.Errorf("the record should name the Claude Code process, its start and its terminal, got %+v", got)
			}

			if got.Cwd != "/work/project" || got.GitCommonDir != "/work/project/.git" {
				t.Errorf("the record should name the session's folder and repository, got %q %q", got.Cwd, got.GitCommonDir)
			}

			if got.Event != "SessionStart" || got.EventAt == nil || !got.EventAt.Equal(startedAt) || got.Gone {
				t.Errorf("the record should hold its last event and its process should be running, got %+v", got)
			}
		})
	}
}

func TestAStopBeginsTheWaitAndThePromptAfterItEndsIt(t *testing.T) {
	m := newMachine(t, nil)
	m.send(t, test.Session, "SessionStart", "")
	m.send(t, test.Session, "UserPromptSubmit", "")

	if got := m.only(t); got.WaitingSince != nil || got.WaitingFor != "" {
		t.Errorf("a session working on a prompt should not wait, got %+v", got)
	}

	m.clock.At = startedAt.Add(time.Minute)
	stopped := m.clock.At
	m.send(t, test.Session, "Stop", "")

	m.clock.At = startedAt.Add(5 * time.Minute)
	m.send(t, test.Session, "Stop", "")

	got := m.only(t)
	if got.WaitingSince == nil || !got.WaitingSince.Equal(stopped) || got.WaitingFor != "prompt" {
		t.Errorf("a Stop with no prompt after it should set the wait's start, kept by a later Stop, got %+v", got)
	}

	if got.Event != "Stop" || !got.EventAt.Equal(m.clock.At) {
		t.Errorf("the last event should be the latest Stop, got %+v", got)
	}

	m.send(t, test.Session, "PostToolUse", "a1b2c3")

	if got := m.only(t); got.WaitingSince == nil {
		t.Error("a background subagent's tool call should not end the wait for the user's prompt")
	}

	m.send(t, test.Session, "UserPromptSubmit", "")

	got = m.only(t)
	if got.WaitingSince != nil || got.WaitingFor != "" || got.Event != "UserPromptSubmit" {
		t.Errorf("the next prompt should clear the wait, got %+v", got)
	}
}

func TestAPermissionDialogWaitsUntilItsAgentsToolRuns(t *testing.T) {
	cases := []struct {
		name  string
		agent string
		other string
	}{
		{name: "the session's own agent", agent: "", other: "a1b2c3"},
		{name: "a subagent", agent: "a1b2c3", other: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine(t, nil)
			m.send(t, test.Session, "SessionStart", "")
			m.send(t, test.Session, "PreToolUse", tc.agent)

			asked := startedAt.Add(time.Minute)
			m.clock.At = asked
			m.send(t, test.Session, "PermissionRequest", tc.agent)

			got := m.only(t)
			if got.WaitingSince == nil || !got.WaitingSince.Equal(asked) || got.WaitingFor != "permission" {
				t.Errorf("a permission dialog should begin a wait for its answer, got %+v", got)
			}

			m.send(t, test.Session, "PostToolUse", tc.other)

			if got := m.only(t); got.WaitingSince == nil {
				t.Error("another agent's tool call should not end the wait for the dialog's answer")
			}

			m.send(t, test.Session, "PostToolUse", tc.agent)

			if got := m.only(t); got.WaitingSince != nil || got.WaitingFor != "" {
				t.Errorf("the asking agent's tool running should end the wait, got %+v", got)
			}
		})
	}
}

func TestAStopDuringASubagentsDialogWaitsForThePrompt(t *testing.T) {
	m := newMachine(t, nil)
	m.send(t, test.Session, "SessionStart", "")

	asked := startedAt.Add(time.Minute)
	m.clock.At = asked
	m.send(t, test.Session, "PermissionRequest", "a1b2c3")

	m.clock.At = startedAt.Add(2 * time.Minute)
	m.send(t, test.Session, "Stop", "")
	m.send(t, test.Session, "PostToolUse", "a1b2c3")

	got := m.only(t)
	if got.WaitingSince == nil || !got.WaitingSince.Equal(asked) || got.WaitingFor != "prompt" {
		t.Errorf("a Stop should leave the session waiting for a prompt from the wait's first start, got %+v", got)
	}
}

func TestADialogDuringTheWaitForThePromptKeepsThatWait(t *testing.T) {
	m := newMachine(t, nil)
	m.send(t, test.Session, "SessionStart", "")

	stopped := startedAt.Add(time.Minute)
	m.clock.At = stopped
	m.send(t, test.Session, "Stop", "")

	m.clock.At = startedAt.Add(2 * time.Minute)
	m.send(t, test.Session, "PermissionRequest", "a1b2c3")
	m.send(t, test.Session, "PostToolUse", "a1b2c3")

	got := m.only(t)
	if got.WaitingSince == nil || !got.WaitingSince.Equal(stopped) || got.WaitingFor != "prompt" {
		t.Errorf("a background subagent's dialog should leave the wait for the prompt as it began, got %+v", got)
	}
}

func TestAnAgentIDOutsideTheShapeBeginsNoWait(t *testing.T) {
	m := newMachine(t, nil)
	m.send(t, test.Session, "SessionStart", "")
	m.send(t, test.Session, "PermissionRequest", "../agent")

	got := m.only(t)
	if got.WaitingSince != nil || got.Event != "PermissionRequest" {
		t.Errorf("an agent id outside the shape should be recorded as the last event and begin no wait, got %+v", got)
	}
}

func TestARecordOfAnotherSchemaReadsAsNone(t *testing.T) {
	m := newMachine(t, map[string]string{"TERM_PROGRAM": "iTerm.app"})
	err := m.slots().Put(context.Background(), test.Session, []byte(`{"schema":2,"pid":1,"event":"Stop"}`))
	if err != nil {
		t.Fatalf("writing a record of another schema: %v", err)
	}

	if got := m.list(t); len(got) != 0 {
		t.Fatalf("a record of another schema should not be printed, got %+v", got)
	}

	m.send(t, test.Session, "UserPromptSubmit", "")

	got := m.only(t)
	if got.PID != test.OwnerPID || got.TermProgram != "iTerm.app" {
		t.Errorf("the next event should write a fresh record of this schema, got %+v", got)
	}
}

func TestAKeyOutsideTheSessionShapeIsNotPrinted(t *testing.T) {
	m := newMachine(t, nil)

	err := m.slots().Put(context.Background(), "../escape", []byte(`{"schema":1,"pid":1,"event":"Stop"}`))
	if err != nil {
		t.Fatalf("writing a key outside the shape: %v", err)
	}

	if got := m.list(t); len(got) != 0 {
		t.Errorf("a key outside the session shape should not be printed, got %+v", got)
	}
}

func TestNoCacheRootRecordsNothing(t *testing.T) {
	working := t.TempDir()
	t.Chdir(working)

	m := newMachine(t, nil)
	m.env.Dir = ""
	m.send(t, test.Session, "SessionStart", "")

	if got := m.list(t); len(got) != 0 {
		t.Errorf("without a cache root nothing should be printed, got %+v", got)
	}

	if tree := test.Tree(t, working); tree != "" {
		t.Errorf("without a cache root nothing should be written, got %q", tree)
	}
}

func TestAnEndedSessionDropsOutAndAGoneOneIsMarked(t *testing.T) {
	m := newMachine(t, nil)
	m.send(t, test.Session, "SessionStart", "")

	other := m.processes.Other()
	m.env.Processes = other
	m.send(t, test.OtherSession, "SessionStart", "")
	m.env.Processes = m.processes

	if got := m.list(t); len(got) != 2 || got[0].Gone || got[1].Gone {
		t.Fatalf("two running sessions should both be listed, neither gone, got %+v", got)
	}

	m.send(t, test.Session, "SessionEnd", "")

	got := m.list(t)
	if len(got) != 1 || got[0].SessionID != test.OtherSession {
		t.Fatalf("a session ended by SessionEnd should drop out of the output, got %+v", got)
	}

	m.processes.Restart(test.OtherOwnerPID, test.OtherOwnerStart.Add(time.Hour))

	if got := m.only(t); !got.Gone {
		t.Error("a session whose pid now runs a process of another start should be marked gone")
	}

	m.processes.Kill(test.OtherOwnerPID)

	if got := m.only(t); !got.Gone {
		t.Error("a session whose pid is gone should be marked gone")
	}
}

func TestASessionOutsideClaudeIsMarkedGone(t *testing.T) {
	m := newMachine(t, nil)
	m.env.Processes = test.NewProcessesOutsideClaude()
	m.send(t, test.Session, "SessionStart", "")

	if got := m.only(t); got.PID != 0 || !got.Gone {
		t.Errorf("a record naming no Claude Code process cannot be matched to one, so it reads as gone, got %+v", got)
	}
}

func TestTheOutputCarriesItsSchema(t *testing.T) {
	m := newMachine(t, nil)
	m.send(t, test.Session, "SessionStart", "")

	if got := m.only(t); got.Schema != sessionrecord.Schema {
		t.Errorf("every printed record should carry the schema version, got %d", got.Schema)
	}
}

func TestNoRecordsPrintAnEmptyArray(t *testing.T) {
	m := newMachine(t, nil)

	var captured test.Captured

	code := sessionrecord.Main(context.Background(), []string{"sessions", "--json"}, m.env, captured.Streams(""))
	if code != 0 || strings.TrimSpace(captured.Out.String()) != "[]" {
		t.Errorf("a machine with no records should print an empty array, got %d %q", code, captured.Out.String())
	}
}

func TestARecordWithHugeVariablesStaysWithinItsCeiling(t *testing.T) {
	huge := strings.Repeat("<", 4_000)
	m := newMachine(t, map[string]string{
		"TERM_PROGRAM": huge, "ITERM_SESSION_ID": huge, "TERMINAL_EMULATOR": huge, "TMUX_PANE": huge,
		"__CFBundleIdentifier": huge,
	})
	m.env.Checkout = test.CommonDir{Dir: "/" + huge}

	stderr := m.send(t, test.Session, "SessionStart", "")
	if stderr != "" {
		t.Fatalf("a record past its caps should still be written, got %q", stderr)
	}

	held, found, err := m.slots().Get(context.Background(), test.Session)
	if err != nil || !found {
		t.Fatalf("the record should be stored: %v %v", found, err)
	}

	if len(held) > sessionrecord.Ceiling {
		t.Errorf("a stored record should never pass its ceiling, %d bytes", len(held))
	}

	got := m.only(t)
	if got.TermProgram != "" || got.GitCommonDir != "" || got.PID != test.OwnerPID || got.Event != "SessionStart" {
		t.Errorf("a field past its cap should be dropped and the rest kept, got %+v", got)
	}
}

func TestAPayloadOutsideTheSessionShapeRecordsNothing(t *testing.T) {
	cases := []struct {
		name    string
		session string
	}{
		{name: "a path", session: "../escape"},
		{name: "too long", session: strings.Repeat("s", 129)},
		{name: "empty", session: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine(t, nil)
			m.send(t, tc.session, "SessionStart", "")

			held, err := m.slots().All(context.Background())
			if err != nil || len(held) != 0 {
				t.Errorf("a session id outside the shape should write no key, got %d %v", len(held), err)
			}
		})
	}
}

func TestAnEventBeforeAnySessionStartLearnsWhereTheSessionRuns(t *testing.T) {
	m := newMachine(t, map[string]string{"TERM_PROGRAM": "iTerm.app"})
	m.send(t, test.Session, "PostToolUse", "")

	got := m.only(t)
	if got.PID != test.OwnerPID || got.TermProgram != "iTerm.app" || got.Event != "PostToolUse" {
		t.Errorf("a session's first event should record where it runs, got %+v", got)
	}
}

func TestARecordNoEventTouchedForAWeekIsReclaimed(t *testing.T) {
	m := newMachine(t, nil)
	m.send(t, test.Session, "SessionStart", "")

	m.clock.At = startedAt.Add(slots.StaleAge + time.Hour)
	m.send(t, test.OtherSession, "SessionStart", "")

	got := m.list(t)
	if len(got) != 1 || got[0].SessionID != test.OtherSession {
		t.Errorf("the next SessionStart should reclaim a record no event touched for a week, got %+v", got)
	}
}

func TestAFailedWriteIsReported(t *testing.T) {
	m := newMachine(t, nil)
	test.WriteFile(t, m.env.Dir, "a file where the folder goes")

	stderr := m.send(t, test.Session, "UserPromptSubmit", "")
	if !strings.Contains(stderr, "agentic-helpers: writing the session record:") {
		t.Errorf("a write that fails should be reported, got %q", stderr)
	}
}

func TestOnlyTheTwoCommandsAreAnswered(t *testing.T) {
	m := newMachine(t, nil)

	for _, args := range [][]string{{"sessions"}, {"record", "extra"}, {"sessions", "--text"}} {
		var captured test.Captured

		code := sessionrecord.Main(context.Background(), args, m.env, captured.Streams(""))
		if code != 1 || !strings.HasPrefix(captured.Err.String(), "usage: ") {
			t.Errorf("%v should be refused with the usage, got %d %q", args, code, captured.Err.String())
		}
	}
}
