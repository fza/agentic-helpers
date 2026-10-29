package hookio

import "os"

// Terminal is the call's standard input, asked whether a person types into
// it: the user at a shell outside Claude Code, where an agent's Bash tool and
// MCP calls hand over a pipe or /dev/null.
type Terminal struct {
	File *os.File
}
