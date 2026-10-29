//go:build darwin || linux

package hookio

import "golang.org/x/sys/unix"

// Interactive asks the descriptor for its terminal settings, which only a
// terminal has. A character-device check would take /dev/null, the standard
// input an agent's tool call is often handed, for a terminal.
func (terminal Terminal) Interactive() bool {
	_, err := unix.IoctlGetTermios(int(terminal.File.Fd()), termiosRequest)

	return err == nil
}
