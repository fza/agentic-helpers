package test

import "context"

// The fake process tree: a hook (HookPID) runs under a shell (ShellPID)
// started by the Claude process (OwnerPID).
const (
	HookPID  = 100
	ShellPID = 150
	OwnerPID = 200
	DeadPID  = 999_999

	OtherHookPID  = 110
	OtherShellPID = 350
	OtherOwnerPID = 300
)

type Process struct {
	Parent  int
	Name    string
	Started string
}

type Processes struct {
	Table map[int]Process
	self  int
}

func NewProcesses() *Processes {
	return &Processes{self: HookPID, Table: map[int]Process{
		HookPID:  {Parent: ShellPID, Name: "agentic-memory", Started: "hook"},
		ShellPID: {Parent: OwnerPID, Name: "zsh", Started: "shell"},
		OwnerPID: {Parent: 1, Name: "claude", Started: OwnerStarted},
	}}
}

// Other is a second live session on the same machine: another hook process
// under another claude process, sharing this process table, so two seats can
// act in one test and a kill in either is seen by both.
func (fake *Processes) Other() *Processes {
	fake.Table[OtherHookPID] = Process{Parent: OtherShellPID, Name: "agentic-memory", Started: "hook"}
	fake.Table[OtherShellPID] = Process{Parent: OtherOwnerPID, Name: "zsh", Started: "shell"}
	fake.Table[OtherOwnerPID] = Process{Parent: 1, Name: "claude", Started: OtherOwnerStarted}

	return &Processes{Table: fake.Table, self: OtherHookPID}
}

// Kill takes a process out of the table, as if it exited.
func (fake *Processes) Kill(pid int) {
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
	held, found := fake.Table[pid]
	if !found {
		return 0, ""
	}

	return held.Parent, held.Name
}

func (fake *Processes) StartTime(_ context.Context, pid int) string {
	return fake.Table[pid].Started
}
