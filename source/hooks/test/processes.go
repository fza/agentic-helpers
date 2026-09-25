package test

import "context"

// The fake process tree: a hook (HookPID) runs under a shell (ShellPID)
// started by the Claude process (OwnerPID).
const (
	HookPID  = 100
	ShellPID = 150
	OwnerPID = 200
	DeadPID  = 999_999
)

type Process struct {
	Parent  int
	Name    string
	Started string
}

type Processes struct {
	Table map[int]Process
}

func NewProcesses() *Processes {
	return &Processes{Table: map[int]Process{
		HookPID:  {Parent: ShellPID, Name: "agentic-carryforward", Started: "hook"},
		ShellPID: {Parent: OwnerPID, Name: "zsh", Started: "shell"},
		OwnerPID: {Parent: 1, Name: "claude", Started: "Thu Sep 25 14:00:00 2026"},
	}}
}

func (fake *Processes) Self() int {
	return HookPID
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
