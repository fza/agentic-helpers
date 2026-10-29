package pieces

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/levenshtein"
	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
)

// configure runs `config`: every key with its value and the layer it comes
// from, one key's, or a new value written into `config.yaml`.
func configure(args []string, env Env, streams hookio.Streams) int {
	resolution, err := pluginconfig.Resolve(env.Settings, env.Getenv)
	if err != nil {
		_, _ = fmt.Fprintln(streams.Err, self+": "+err.Error())

		return 1
	}

	switch {
	case len(args) == 0:
		_, _ = fmt.Fprint(streams.Out, settingsListing(resolution))

		return 0
	case len(args) == 2 && args[0] == "get":
		if !slices.Contains(pluginconfig.Keys, args[1]) {
			_, _ = fmt.Fprintf(streams.Err, "%s: refused: %s is no option; the nearest is %s\n", self, args[1],
				levenshtein.Nearest(args[1], pluginconfig.Keys))

			return 1
		}

		_, _ = fmt.Fprintln(streams.Out, settingLine(resolution, args[1], 0))

		return 0
	case len(args) == 3 && args[0] == "set":
		return set(args[1], args[2], env, streams)
	}

	_, _ = fmt.Fprintln(streams.Err, usage)

	return 1
}

func set(key string, value string, env Env, streams hookio.Streams) int {
	if len(env.Settings) == 0 || filepath.Ext(env.Settings[0]) != ".yaml" {
		_, _ = fmt.Fprintln(streams.Err, self+": refused: no config.yaml to write")

		return 1
	}

	path := env.Settings[0]

	err := pluginconfig.Set(path, key, value)
	if err != nil {
		_, _ = fmt.Fprintf(streams.Err, "%s: refused: %v\n", self, err)

		return 1
	}

	_, _ = fmt.Fprintf(streams.Out, "%s set to %s in %s\n", key, shown(value), path)

	resolution, err := pluginconfig.Resolve(env.Settings, env.Getenv)
	if err != nil {
		_, _ = fmt.Fprintln(streams.Err, self+": "+err.Error())

		return 1
	}

	layer := resolution.From[key]
	if layer != "" && layer != pluginconfig.LayerConfig {
		_, _ = fmt.Fprintf(streams.Out, "%s also sets %s and wins over config.yaml: %s applies\n", layer, key,
			shown(resolution.Options.Value(key)))
	}

	return 0
}

func settingsListing(resolution pluginconfig.Resolution) string {
	width := 0
	for _, key := range pluginconfig.Keys {
		width = max(width, len(key))
	}

	lines := make([]string, 0, len(pluginconfig.Keys)+len(resolution.Problems))
	for _, key := range pluginconfig.Keys {
		lines = append(lines, settingLine(resolution, key, width))
	}

	for _, problem := range resolution.Problems {
		lines = append(lines, "problem: "+problem)
	}

	return strings.Join(lines, "\n") + "\n"
}

// settingLine is `<key>  <value>  (<layer>)`, a key only config.yaml sets
// saying so.
func settingLine(resolution pluginconfig.Resolution, key string, width int) string {
	layer := resolution.From[key]
	if layer == "" {
		layer = pluginconfig.LayerDefault
	}

	if slices.Contains(pluginconfig.ConfigOnly, key) {
		layer += ", never a plugin option"
	}

	return fmt.Sprintf("%-*s  %s  (%s)", width, key, shown(resolution.Options.Value(key)), layer)
}

func shown(value string) string {
	if value == "" {
		return `""`
	}

	return value
}
