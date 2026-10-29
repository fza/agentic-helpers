package pieces

import (
	"context"
	"fmt"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/alternatives"
	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/migration"
	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
)

const self = "agentic-helpers"

// Env says where the plugin's options are read, and whether the call is the
// SessionStart hook rather than a command an agent or the user runs.
// Migration is the machine `sweep` and the SessionEnd hook `end` work on.
// CacheRoot keeps each command's flavour, for the line naming what the
// machine needs.
type Env struct {
	Settings  []string
	Getenv    func(string) string
	Hook      bool
	Migration migration.Env
	CacheRoot string
}

const usage = "usage: " + self + " check | config [get <key> | set <key> <value>] | sweep | end"

// Main runs `agentic-helpers check` and `agentic-helpers config`. check first
// copies the answers for keys that left the plugin's dialog into
// `config.yaml`. As the SessionStart hook it then prints one line per broken
// tie and per `config.yaml` problem, then one naming the command
// substitutions the machine needs, nothing where none is; run as a command
// it lists every piece on or off and, under each, its features off through a
// tie. sweep and end run and start the sweep of the old layout
// (internal/migration).
func Main(ctx context.Context, args []string, env Env, streams hookio.Streams) int {
	if len(args) > 0 && args[0] == "config" {
		return configure(args[1:], env, streams)
	}

	if len(args) == 1 && (args[0] == "sweep" || args[0] == "end") {
		return sweep(ctx, args[0], env, streams)
	}

	if len(args) != 1 || args[0] != "check" {
		_, _ = fmt.Fprintln(streams.Err, usage)

		return 1
	}

	_, _ = pluginconfig.MigrateAgentAnswers(env.Settings)

	resolution, err := pluginconfig.Resolve(env.Settings, env.Getenv)
	if err != nil {
		_, _ = fmt.Fprintln(streams.Err, self+": "+err.Error())

		return 1
	}

	if env.Hook {
		for _, tie := range Broken(resolution.Options) {
			_, _ = fmt.Fprintln(streams.Out, tie.Line())
		}

		for _, problem := range resolution.Problems {
			_, _ = fmt.Fprintln(streams.Out, self+": "+problem)
		}

		line := machineLine(ctx, env, resolution.Options)
		if line != "" {
			_, _ = fmt.Fprintln(streams.Out, self+": "+line)
		}

		return 0
	}

	_, _ = fmt.Fprint(streams.Out, listing(resolution.Options))

	for _, problem := range resolution.Problems {
		_, _ = fmt.Fprintln(streams.Out, "problem: "+problem)
	}

	return 0
}

// sweep removes what the migration left for older releases once none runs;
// as the SessionEnd hook, end, it starts that in a process of its own and
// says nothing.
func sweep(ctx context.Context, name string, env Env, streams hookio.Streams) int {
	var err error

	if name == "end" {
		err = migration.StartSweep(ctx, env.Migration)
	} else {
		_, err = migration.Sweep(ctx, env.Migration)
	}

	if err != nil && name == "sweep" {
		_, _ = fmt.Fprintln(streams.Err, self+": "+err.Error())

		return 1
	}

	return 0
}

// machineLine names the substitutions the machine needs for the command
// alternatives shell-guard refuses: empty where it needs none, or where
// shell-guard or its alternatives are off.
func machineLine(ctx context.Context, env Env, options pluginconfig.Options) string {
	if !ShellGuard.On(options) || !options.ShellGuardAlternatives {
		return ""
	}

	path := ""
	if env.Getenv != nil {
		path = env.Getenv("PATH")
	}

	machine := alternatives.Machine{Path: path, CacheFile: alternatives.CacheFile(env.CacheRoot)}

	return machine.Line(ctx)
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
