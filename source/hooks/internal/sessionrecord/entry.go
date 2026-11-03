package sessionrecord

import (
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
)

// Ceiling bounds a record's JSON in bytes, within what a 2 KiB class twin
// holds beside the longest session id.
const Ceiling = 1_700

// entry is a session's record as its slot holds it.
type entry struct {
	Schema int `json:"schema"`
	place
	Event        string    `json:"event,omitempty"`
	EventAt      time.Time `json:"event_at,omitzero"`
	WaitingSince time.Time `json:"waiting_since,omitzero"`
	WaitingFor   string    `json:"waiting_for,omitempty"`
	// WaitingAgent is the subagent whose permission dialog began the wait,
	// empty for the session's own agent.
	WaitingAgent string `json:"waiting_agent,omitempty"`
}

// place is where a session runs.
type place struct {
	PID              int       `json:"pid,omitempty"`
	Started          time.Time `json:"started,omitzero"`
	TTY              string    `json:"tty,omitempty"`
	TermProgram      string    `json:"term_program,omitempty"`
	ITermSessionID   string    `json:"iterm_session_id,omitempty"`
	TerminalEmulator string    `json:"terminal_emulator,omitempty"`
	TmuxPane         string    `json:"tmux_pane,omitempty"`
	BundleID         string    `json:"bundle_id,omitempty"`
	Cwd              string    `json:"cwd,omitempty"`
	GitCommonDir     string    `json:"git_common_dir,omitempty"`
}

// capped is a text field with the most JSON bytes it may take, quotes
// included. A value past its cap is dropped rather than cut: a cut path or
// terminal id names another one.
type capped struct {
	value *string
	cap   int
}

// fields lists every text field, the first dropped first when the record
// stays past its ceiling: what a jump to the terminal needs least goes
// first, and the event fields never.
func (held *entry) fields() []capped {
	return []capped{
		{&held.GitCommonDir, 400},
		{&held.Cwd, 400},
		{&held.ITermSessionID, 128},
		{&held.BundleID, 128},
		{&held.TerminalEmulator, 64},
		{&held.TermProgram, 64},
		{&held.TmuxPane, 32},
		{&held.TTY, 64},
		{&held.WaitingAgent, 2 + maxID},
		{&held.WaitingFor, 16},
		{&held.Event, 32},
	}
}

// droppable is how many of fields Shrink may drop: the event and wait
// fields stay.
const droppable = 8

func (*entry) Ceiling() int { return Ceiling }

func (held *entry) Limit() {
	for _, field := range held.fields() {
		if slots.EncodedLen(*field.value) > field.cap {
			*field.value = ""
		}
	}
}

func (held *entry) Shrink() bool {
	for _, field := range held.fields()[:droppable] {
		if *field.value != "" {
			*field.value = ""

			return true
		}
	}

	return false
}
