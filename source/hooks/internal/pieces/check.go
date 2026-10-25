package pieces

import (
	"context"
	"fmt"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
)

const self = "agentic-helpers"

// Env says where the plugin's options are read, and whether the call is the
// SessionStart hook rather than a command an agent or the user runs.
type Env struct {
	Settings []string
	Getenv   func(string) string
	Hook     bool
}

// Main runs `agentic-helpers check`. As the SessionStart hook it prints one
// line per broken tie, nothing where none is; run as a command it lists every
// piece on or off and, under each, its features off through a tie.
func Main(_ context.Context, args []string, env Env, streams hookio.Streams) int {
	if len(args) != 1 || args[0] != "check" {
		_, _ = fmt.Fprintln(streams.Err, "usage: "+self+" check")

		return 1
	}

	options, err := pluginconfig.ReadWith(env.Settings, env.Getenv)
	if err != nil {
		_, _ = fmt.Fprintln(streams.Err, self+": "+err.Error())

		return 1
	}

	if env.Hook {
		for _, tie := range Broken(options) {
			_, _ = fmt.Fprintln(streams.Out, tie.Line())
		}

		return 0
	}

	_, _ = fmt.Fprint(streams.Out, listing(options))

	return 0
}

func listing(options pluginconfig.Options) string {
	width := 0
	for _, piece := range All() {
		width = max(width, len(piece.Name))
	}

	var lines []string

	for _, piece := range All() {
		state := "on"
		if !piece.On(options) {
			state = "off  (plugin option " + piece.Key + ")"
		}

		lines = append(lines, fmt.Sprintf("%-*s  %s", width, piece.Name, state))

		for _, tie := range Broken(options) {
			if tie.In.Key == piece.Key {
				lines = append(lines, fmt.Sprintf("  off: %s, as %s is off; turn %s on to restore it", tie.Feature,
					tie.On.Name, tie.On.Key))
			}
		}
	}

	return strings.Join(lines, "\n") + "\n"
}
