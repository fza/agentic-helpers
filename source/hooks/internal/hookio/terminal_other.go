//go:build !darwin && !linux

package hookio

// Interactive knows no terminal on this platform: every call counts as an
// agent's.
func (Terminal) Interactive() bool { return false }
