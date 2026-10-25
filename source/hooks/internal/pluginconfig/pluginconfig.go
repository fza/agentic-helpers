// Package pluginconfig reads the answers a user gave the plugin's `userConfig`
// dialog. Claude Code exports them only to hook processes, so a command the
// Bash tool runs from the plugin's `bin/` reads them from the settings files
// themselves: user settings, then managed settings, which win.
package pluginconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
)

const PluginID = "agentic-helpers@agentic-helpers"

const DefaultEntryMaxWords = 500

type Options struct {
	EmbeddingProvider   string
	EmbeddingModel      string
	EmbeddingEndpoint   string
	EmbeddingDimensions int
	EmbeddingAPIKeyEnv  string
	EntryMaxWords       int
}

type settings struct {
	PluginConfigs map[string]struct {
		Options map[string]json.RawMessage `json:"options"`
	} `json:"pluginConfigs"`
}

// Paths is every settings file that can carry `pluginConfigs`, lowest
// precedence first. Project settings are never among them: one embedder serves
// every project on the machine.
func Paths(home string, goos string) []string {
	paths := []string{filepath.Join(home, ".claude", "settings.json")}

	switch goos {
	case "darwin":
		paths = append(paths, "/Library/Application Support/ClaudeCode/managed-settings.json")
	case "linux":
		paths = append(paths, "/etc/claude-code/managed-settings.json")
	}

	return paths
}

// Read folds the plugin's options from paths in order, a later file winning
// per key. A missing file contributes nothing; an unreadable one is an error.
func Read(paths []string) (Options, error) {
	options := Options{EntryMaxWords: DefaultEntryMaxWords}

	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}

		if err != nil {
			return Options{}, fmt.Errorf("reading %s: %w", path, err)
		}

		var parsed settings

		err = json.Unmarshal(raw, &parsed)
		if err != nil {
			return Options{}, fmt.Errorf("parsing %s: %w", path, err)
		}

		err = options.apply(parsed.PluginConfigs[PluginID].Options)
		if err != nil {
			return Options{}, fmt.Errorf("reading the %s options in %s: %w", PluginID, path, err)
		}
	}

	return options, nil
}

func (options *Options) apply(values map[string]json.RawMessage) error {
	texts := map[string]*string{
		"embedding_provider":    &options.EmbeddingProvider,
		"embedding_model":       &options.EmbeddingModel,
		"embedding_endpoint":    &options.EmbeddingEndpoint,
		"embedding_api_key_env": &options.EmbeddingAPIKeyEnv,
	}

	for key, target := range texts {
		raw, found := values[key]
		if !found {
			continue
		}

		err := json.Unmarshal(raw, target)
		if err != nil {
			return fmt.Errorf("%s is not a string: %w", key, err)
		}
	}

	numbers := map[string]*int{
		"embedding_dimensions": &options.EmbeddingDimensions,
		"entry_max_words":      &options.EntryMaxWords,
	}

	for key, target := range numbers {
		raw, found := values[key]
		if !found {
			continue
		}

		value, err := number(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}

		*target = value
	}

	return nil
}

// number accepts a JSON number or a numeric string: the dialog stores what the
// user typed, and a hand-edited settings file may quote it.
func number(raw json.RawMessage) (int, error) {
	var value float64

	err := json.Unmarshal(raw, &value)
	if err == nil {
		return int(value), nil
	}

	var text string

	err = json.Unmarshal(raw, &text)
	if err != nil {
		return 0, fmt.Errorf("not a number: %s", raw)
	}

	if text == "" {
		return 0, nil
	}

	parsed, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("not a number: %q", text)
	}

	return parsed, nil
}
