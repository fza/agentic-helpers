package test

import (
	"context"
	"sync"
	"time"
)

// The fake process tree: a hook (HookPID) runs under a shell (ShellPID)
// started by the Claude process (OwnerPID).
const (
	HookPID  = 100
	ShellPID = 150
	OwnerPID = 200
	LoginPID = 250
	DeadPID  = 999_999

	OtherHookPID  = 110
	OtherShellPID = 350
	OtherOwnerPID = 300
)

type Process struct {
	Parent   int
	Name     string
	Started  time.Time
	Terminal string
	// Environment is what the process was started with; Hidden keeps it
	// unreadable, as the kernel keeps another user's.
	Environment []string
	Hidden      bool
}

// SessionVariable is the variable Claude Code starts every process of a
// session with, its value the session's id.
const SessionVariable = "CLAUDE_CODE_SESSION_ID"

// NewProcessesRunningAs is the tree of NewProcesses with Claude Code running
// under name, as the native installer's version-named executable or node runs
// it: the hook and its shell carry the session's variables, Claude Code itself
// none.
func NewProcessesRunningAs(name string) *Processes {
	session := []string{SessionVariable + "=" + Session, "CLAUDECODE=1"}

	return &Processes{self: HookPID, guard: &sync.RWMutex{}, Table: map[int]Process{
		HookPID:  {Parent: ShellPID, Name: "agentic-memory", Environment: session},
		ShellPID: {Parent: OwnerPID, Name: "zsh", Environment: session},
		OwnerPID: {Parent: 1, Name: name, Started: OwnerStart, Environment: []string{"HOME=/Users/dev"}},
	}}
}

type Processes struct {
	Table map[int]Process
	self  int
	// guard lets a test kill a process while a hook reads the table.
	guard *sync.RWMutex
}

func NewProcesses() *Processes {
	return &Processes{self: HookPID, guard: &sync.RWMutex{}, Table: map[int]Process{
		HookPID:  {Parent: ShellPID, Name: "agentic-memory"},
		ShellPID: {Parent: OwnerPID, Name: "zsh"},
		OwnerPID: {Parent: 1, Name: "claude", Started: OwnerStart},
	}}
}

// NewProcessesOutsideClaude is the user's own shell: the hook runs under a
// shell no claude process started.
func NewProcessesOutsideClaude() *Processes {
	return &Processes{self: HookPID, guard: &sync.RWMutex{}, Table: map[int]Process{
		HookPID:  {Parent: ShellPID, Name: "agentic-memory"},
		ShellPID: {Parent: LoginPID, Name: "zsh"},
		LoginPID: {Parent: 1, Name: "login"},
	}}
}

// Other is a second live session on the same machine: another hook process
// under another claude process, sharing this process table, so two seats can
// act in one test and a kill in either is seen by both.
func (fake *Processes) Other() *Processes {
	fake.Table[OtherHookPID] = Process{Parent: OtherShellPID, Name: "agentic-memory"}
	fake.Table[OtherShellPID] = Process{Parent: OtherOwnerPID, Name: "zsh"}
	fake.Table[OtherOwnerPID] = Process{Parent: 1, Name: "claude", Started: OtherOwnerStart}

	return &Processes{Table: fake.Table, self: OtherHookPID, guard: fake.guard}
}

// Kill takes a process out of the table, as if it exited.
func (fake *Processes) Kill(pid int) {
	fake.guard.Lock()
	defer fake.guard.Unlock()

	delete(fake.Table, pid)
}

func (fake *Processes) Self() int {
	if fake.self == 0 {
		return HookPID
	}

	return fake.self
}

func (fake *Processes) Parent() int {
	return ShellPID
}

func (fake *Processes) ParentAndName(_ context.Context, pid int) (int, string) {
	fake.guard.RLock()
	defer fake.guard.RUnlock()

	held, found := fake.Table[pid]
	if !found {
		return 0, ""
	}

	return held.Parent, held.Name
}

func (fake *Processes) StartTime(_ context.Context, pid int) (time.Time, bool) {
	fake.guard.RLock()
	defer fake.guard.RUnlock()

	held, found := fake.Table[pid]
	if !found || held.Started.IsZero() {
		return time.Time{}, false
	}

	return held.Started, true
}

func (fake *Processes) Environment(_ context.Context, pid int) ([]string, bool) {
	fake.guard.RLock()
	defer fake.guard.RUnlock()

	held, found := fake.Table[pid]
	if !found || held.Hidden {
		return nil, false
	}

	return held.Environment, true
}

func (fake *Processes) Terminal(_ context.Context, pid int) string {
	fake.guard.RLock()
	defer fake.guard.RUnlock()

	return fake.Table[pid].Terminal
}

// Restart gives pid a new start time, as a pid reused by another process has.
func (fake *Processes) Restart(pid int, started time.Time) {
	fake.guard.Lock()
	defer fake.guard.Unlock()

	held := fake.Table[pid]
	held.Started = started
	fake.Table[pid] = held
}
