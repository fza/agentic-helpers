package pieces

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/alternatives"
	"github.com/fza/agentic-helpers/source/hooks/internal/dump"
	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
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
	// SddConfig reads sdd's embedding block in Dir, the project the session
	// works in, for the line saying an `sdd` embedding source finds none.
	SddConfig embed.Sdd
	Dir       string
}

const usage = "usage: " + self + " check | config [get <key> | set <key> <value>] | sweep | end | dump <file> | bound <duration> <command>"

// Main runs `agentic-helpers check` and `agentic-helpers config`. check first
// copies the answers for keys that left the plugin's dialog into
// `config.yaml`. As the SessionStart hook it then prints one line per broken
// tie and per `config.yaml` problem, then one naming the command
// substitutions the machine needs, nothing where none is; run as a command
// it lists every piece on or off and, under each, its features off through a
// tie. sweep and end run and start the sweep of the old layout
// (internal/migration). dump prints a data file kept in a binary format as
// JSON lines (internal/dump).
func Main(ctx context.Context, args []string, env Env, streams hookio.Streams) int {
	if len(args) > 0 && args[0] == "config" {
		return configure(args[1:], env, streams)
	}

	if len(args) == 1 && (args[0] == "sweep" || args[0] == "end") {
		return sweep(ctx, args[0], env, streams)
	}

	if len(args) > 0 && args[0] == "dump" {
		return dumpFile(ctx, args[1:], streams)
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

		for _, line := range embeddingLines(ctx, env, resolution) {
			_, _ = fmt.Fprintln(streams.Out, self+": "+line)
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

func dumpFile(ctx context.Context, args []string, streams hookio.Streams) int {
	if len(args) != 1 {
		_, _ = fmt.Fprintln(streams.Err, "usage: "+self+" dump <file>")

		return 1
	}

	err := dump.Dump(ctx, streams.Out, args[0])
	if err != nil {
		_, _ = fmt.Fprintln(streams.Err, self+": "+err.Error())

		return 1
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

// embeddingLines names an `embedding_source` some layer still sets, which the
// plugin's dialog no longer offers, and what an `sdd` embedding source leaves
// unused: the own embedding keys some layer sets, and sdd answering no
// embedding block, which leaves search by meaning off. `auto` and `own` say
// nothing more, nor does an `off` provider: falling back to no embedder is the
// state the options describe.
func embeddingLines(ctx context.Context, env Env, resolution pluginconfig.Resolution) []string {
	var lines []string

	layer := resolution.From["embedding_source"]
	if layer != "" {
		lines = append(lines, fmt.Sprintf("embedding_source is %s (%s) and still applies; embedding_provider replaces it: "+
			"auto, openai, ollama or off", resolution.Options.EmbeddingSource, layer))
	}

	if !resolution.Options.EmbeddingFromSdd() {
		return lines
	}

	underSdd := resolution.Options.EmbeddingSource == pluginconfig.EmbeddingSourceSdd

	ignored := resolution.IgnoredEmbedding()
	if underSdd && len(ignored) > 0 {
		lines = append(lines, "embedding_source is sdd; "+strings.Join(ignored, ", ")+" ignored")
	}

	settled, err := embed.Settle(ctx, embed.Config{}, true, env.SddConfig, env.Dir)

	switch {
	case errors.Is(err, embed.ErrRepositoryEndpoint):
		lines = append(lines, err.Error()+", which a clone can bring along; search by meaning stays off until "+
			"`sdd config set` sets it in sdd's global layer, or embedding_provider and embedding_endpoint set one here")
	case underSdd && settled.Provider == "":
		lines = append(lines, "embedding_source is sdd, but sdd sets no embedder here; search by meaning is off")
	}

	return lines
}
