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

// The verdicts shell-guard's outside-project and find -delete guards take.
const (
	ShellGuardAsk  = "ask"
	ShellGuardDeny = "deny"
	ShellGuardOff  = "off"
)

// DefaultToolsDuplicateFloor is the cosine at or above which agentic-tools
// holds a new tool's description to be another tool's.
const DefaultToolsDuplicateFloor = 0.85

// DefaultToolsQueryFloor is the cosine at or above which agentic-tools counts
// a tool's description as near a need: a search's or a prompt's. Its right
// value depends on the embedder; this one sits between the unrelated needs and
// the weakest paraphrase measured with embeddinggemma-300m.
const DefaultToolsQueryFloor = 0.45

// DefaultRetiredBodyDays / MinRetiredBodyDays bound how long a retired entry
// keeps its body. The floor is the revive window: a body never goes before a
// revive could still want it.
const (
	DefaultRetiredBodyDays = 90
	MinRetiredBodyDays     = 14
)

// DefaultEntryIDLength / MinEntryIDLength / MaxEntryIDLength bound the
// characters after `m-` in a new entry id. Six give 36^6, about 2.2 billion
// ids; past sixteen an id is one nobody types.
const (
	DefaultEntryIDLength = 6
	MinEntryIDLength     = 6
	MaxEntryIDLength     = 16
)

type Options struct {
	EmbeddingProvider   string
	EmbeddingModel      string
	EmbeddingEndpoint   string
	EmbeddingDimensions int
	EmbeddingAPIKeyEnv  string
	EntryMaxWords       int
	RetiredBodyDays     int
	EntryIDLength       int
	MemoryLanguage      string
	MemoryRequireGit    bool
	// TemporarySeatLifetime is how long a closed temporary shelf keeps its
	// entries, as `lifetime` spells a span; empty takes agentic-memory's default.
	TemporarySeatLifetime string
	// ShellGuardOutsideProject / ShellGuardFindDelete are `ask`, `deny` or
	// `off`; the other shell-guard options switch a guard that denies when on.
	ShellGuardOutsideProject string
	ShellGuardFindDelete     string
	ShellGuardLoneRemoval    bool
	ShellGuardExpansion      bool
	ShellGuardNestedCode     bool
	ShellGuardMktemp         bool
	ToolsDuplicateFloor      float64
	ToolsQueryFloor          float64
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
	options := Defaults()

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

	options.RetiredBodyDays = max(options.RetiredBodyDays, MinRetiredBodyDays)
	options.EntryIDLength = min(max(options.EntryIDLength, MinEntryIDLength), MaxEntryIDLength)

	return options, nil
}

func Defaults() Options {
	return Options{
		EntryMaxWords: DefaultEntryMaxWords, RetiredBodyDays: DefaultRetiredBodyDays, EntryIDLength: DefaultEntryIDLength,
		MemoryRequireGit: true, ToolsDuplicateFloor: DefaultToolsDuplicateFloor,
		ToolsQueryFloor:          DefaultToolsQueryFloor,
		ShellGuardOutsideProject: ShellGuardAsk, ShellGuardFindDelete: ShellGuardAsk,
		ShellGuardLoneRemoval: true, ShellGuardExpansion: true, ShellGuardNestedCode: true, ShellGuardMktemp: true,
	}
}

func (options *Options) apply(values map[string]json.RawMessage) error {
	texts := map[string]*string{
		"embedding_provider":      &options.EmbeddingProvider,
		"embedding_model":         &options.EmbeddingModel,
		"embedding_endpoint":      &options.EmbeddingEndpoint,
		"embedding_api_key_env":   &options.EmbeddingAPIKeyEnv,
		"memory_language":         &options.MemoryLanguage,
		"temporary_seat_lifetime": &options.TemporarySeatLifetime,
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
		"retired_body_days":    &options.RetiredBodyDays,
		"entry_id_length":      &options.EntryIDLength,
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

	fractions := map[string]*float64{
		"tools_duplicate_floor": &options.ToolsDuplicateFloor,
		"tools_query_floor":     &options.ToolsQueryFloor,
	}

	for key, target := range fractions {
		raw, found := values[key]
		if !found {
			continue
		}

		value, err := fraction(raw, *target)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}

		*target = value
	}

	modes := map[string]*string{
		"shell_guard_outside_project": &options.ShellGuardOutsideProject,
		"shell_guard_find_delete":     &options.ShellGuardFindDelete,
	}

	for key, target := range modes {
		raw, found := values[key]
		if !found {
			continue
		}

		value, err := mode(raw, *target)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}

		*target = value
	}

	switches := map[string]*bool{
		"memory_require_git":       &options.MemoryRequireGit,
		"shell_guard_lone_removal": &options.ShellGuardLoneRemoval,
		"shell_guard_expansion":    &options.ShellGuardExpansion,
		"shell_guard_nested_code":  &options.ShellGuardNestedCode,
		"shell_guard_mktemp":       &options.ShellGuardMktemp,
	}

	for key, target := range switches {
		raw, found := values[key]
		if !found {
			continue
		}

		value, err := boolean(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}

		*target = value
	}

	return nil
}

// mode accepts `ask`, `deny` or `off`; an empty string keeps fallback.
func mode(raw json.RawMessage, fallback string) (string, error) {
	var value string

	err := json.Unmarshal(raw, &value)
	if err != nil {
		return "", fmt.Errorf("%s is not a string", raw)
	}

	switch value {
	case "":
		return fallback, nil
	case ShellGuardAsk, ShellGuardDeny, ShellGuardOff:
		return value, nil
	}

	return "", fmt.Errorf("%q is none of ask, deny, off", value)
}

// boolean accepts a JSON boolean or the string the dialog may store for one.
func boolean(raw json.RawMessage) (bool, error) {
	var value bool

	err := json.Unmarshal(raw, &value)
	if err == nil {
		return value, nil
	}

	var text string

	err = json.Unmarshal(raw, &text)
	if err == nil && (text == "true" || text == "false") {
		return text == "true", nil
	}

	return false, fmt.Errorf("%s is not a boolean", raw)
}

// number accepts a JSON number or a numeric string: the dialog stores what the
// user typed, and a hand-edited settings file may quote it.
// fraction accepts a number above 0 and at most 1, or the string the dialog
// may store for one; an empty string keeps fallback.
func fraction(raw json.RawMessage, fallback float64) (float64, error) {
	var value float64

	err := json.Unmarshal(raw, &value)
	if err != nil {
		var text string

		err = json.Unmarshal(raw, &text)
		if err != nil {
			return 0, fmt.Errorf("not a number: %s", raw)
		}

		if text == "" {
			return fallback, nil
		}

		value, err = strconv.ParseFloat(text, 64)
		if err != nil {
			return 0, fmt.Errorf("not a number: %q", text)
		}
	}

	if value <= 0 || value > 1 {
		return 0, fmt.Errorf("%v lies outside (0, 1]", value)
	}

	return value, nil
}

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
