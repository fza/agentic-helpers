package test

// Terminal stands for the call's standard input: true is the user at a
// shell, false an agent's tool call.
type Terminal bool

func (terminal Terminal) Interactive() bool { return bool(terminal) }
