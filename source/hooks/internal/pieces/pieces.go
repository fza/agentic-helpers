// Package pieces is every agentic-helpers piece with the plugin option that
// switches it on or off for the machine, and every tie between pieces: a
// feature of one piece that rests on another and goes off with it. Every
// binary reads its own switch and its features' ties here, so the feature that
// goes off and the line that reports it never disagree.
package pieces

import (
	"fmt"

	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
)

type Piece struct {
	Name   string
	Binary string
	Key    string
	on     func(options pluginconfig.Options) bool
}

func (piece Piece) On(options pluginconfig.Options) bool {
	return piece.on(options)
}

// Off is what a command of an off piece answers on stderr, exiting 1.
func (piece Piece) Off() string {
	return fmt.Sprintf("%s: off on this machine (plugin option %s)", piece.Binary, piece.Key)
}

// Instructions is what an off piece's MCP server tells the agent instead of
// listing tools.
func (piece Piece) Instructions() string {
	return fmt.Sprintf("%s is off on this machine; plugin option %s turns it on", piece.Binary, piece.Key)
}

var (
	Memory = Piece{Name: "memory", Binary: "agentic-memory", Key: "memory_enabled",
		on: func(options pluginconfig.Options) bool { return options.MemoryEnabled }}
	Tools = Piece{Name: "tools", Binary: "agentic-tools", Key: "tools_enabled",
		on: func(options pluginconfig.Options) bool { return options.ToolsEnabled }}
	Scratch = Piece{Name: "scratch", Binary: "agentic-scratch", Key: "scratch_enabled",
		on: func(options pluginconfig.Options) bool { return options.ScratchEnabled }}
	GroundingGate = Piece{Name: "grounding gate", Binary: "agentic-grounding-gate", Key: "grounding_gate_enabled",
		on: func(options pluginconfig.Options) bool { return options.GroundingGateEnabled }}
	Capture = Piece{Name: "capture", Binary: "agentic-capture", Key: "capture_enabled",
		on: func(options pluginconfig.Options) bool { return options.CaptureEnabled }}
	ShellGuard = Piece{Name: "shell-guard", Binary: "agentic-shell-guard", Key: "shell_guard_enabled",
		on: func(options pluginconfig.Options) bool { return options.ShellGuardEnabled }}
)

func All() []Piece {
	return []Piece{Memory, Tools, Scratch, GroundingGate, Capture, ShellGuard}
}

// Tie is a feature of a piece, In, that rests on another, On, and goes off
// while that one is.
type Tie struct {
	In      Piece
	On      Piece
	Feature string
	Why     string
}

var (
	MktempBan = Tie{In: ShellGuard, On: Scratch, Feature: "shell-guard's mktemp ban",
		Why: "its refusal sends scratch files to a .tmp/ nothing sweeps"}
	CaptureGrounded = Tie{In: Capture, On: GroundingGate, Feature: "agentic-capture",
		Why: "its grounding check is the gate's hook; without it a draft lands ungrounded"}
	ExemptSeats = Tie{In: GroundingGate, On: Memory, Feature: "the grounding gate's exempt_seats",
		Why: "a seat is an agentic-memory claim; a claim left from before the switch must not exempt anything"}
	ToolsSeatName = Tie{In: Tools, On: Memory, Feature: "the seat name agentic-tools stamps on what it files",
		Why: "it reads the name from agentic-memory's seat registry"}
)

func Ties() []Tie {
	return []Tie{MktempBan, CaptureGrounded, ExemptSeats, ToolsSeatName}
}

// Holds is whether the feature runs: the piece it rests on is on.
func (tie Tie) Holds(options pluginconfig.Options) bool {
	return tie.On.On(options)
}

// Off is what a command whose whole work is the tie's feature answers on
// stderr while the tie is broken, exiting 1.
func (tie Tie) Off() string {
	return fmt.Sprintf("%s: off while %s is off (plugin option %s)", tie.In.Binary, tie.On.Name, tie.On.Key)
}

// Line is how a broken tie reads at session start.
func (tie Tie) Line() string {
	return fmt.Sprintf("agentic-helpers: %s is off, so %s is off; turn %s on to restore it", tie.On.Name, tie.Feature,
		tie.On.Key)
}

// Broken is every tie whose feature is off because the piece it rests on is,
// while the piece the feature belongs to is on.
func Broken(options pluginconfig.Options) []Tie {
	var broken []Tie

	for _, tie := range Ties() {
		if tie.In.On(options) && !tie.Holds(options) {
			broken = append(broken, tie)
		}
	}

	return broken
}
