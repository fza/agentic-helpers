// Command agentic-helpers is one program answering to five names, the base of
// the path it runs under picking the command:
//
//   - agentic-memory, the agent memory store: the subcommands a session runs
//     to claim a seat and file, search and retire entries, the stdio MCP
//     server offering the same, and the hooks replaying what a fresh context
//     needs;
//   - agentic-tools, the library of agents' helper tools: find, take, add and
//     update, its MCP server, and the hooks offering tools;
//   - agentic-capture, turning a draft file into a captured sdd graph entry;
//   - agentic-scratch, the hook sweeping a project's `.tmp/seats/`: `end`
//     removes the ending session's own folder, and `start` reaps abandoned
//     ones and moves strays to `.tmp/keep/`;
//   - any other name, agentic-helpers itself: `check` names the features off
//     because a piece they rest on is off, `bound` runs a command for at
//     most a duration, standing in for a missing `timeout`, the hook `record`
//     keeps each session's record of where it runs and its last event, and
//     `sessions --json` prints those records.
//
// The release archive and the launcher link each of the four names to this
// program. The hook programs agentic-shell-guard and agentic-grounding-gate
// stay programs of their own, so a fault in this one's packages never refuses
// a Bash or graph call.
package main

import (
	"os"
	"path/filepath"

	"github.com/fza/agentic-helpers/source/hooks/internal/pieces"
)

var version = "dev"

var commands = map[string]func() int{
	pieces.Memory.Binary:  memoryMain,
	pieces.Tools.Binary:   toolsMain,
	pieces.Capture.Binary: captureMain,
	pieces.Scratch.Binary: scratchMain,
}

func main() {
	command, found := commands[filepath.Base(os.Args[0])]
	if !found {
		command = helpersMain
	}

	os.Exit(command())
}

// namedPath is this program's path under name: the link beside it that the
// release archive and the launcher put there. A process started from it runs
// as that command, where Linux resolves the program's own path to the file
// the links point to.
func namedPath(name string) string {
	executable, err := os.Executable()
	if err != nil {
		return ""
	}

	return filepath.Join(filepath.Dir(executable), name)
}
