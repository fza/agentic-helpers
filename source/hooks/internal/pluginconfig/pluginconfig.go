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
	"slices"
	"strconv"
	"strings"
)

const PluginID = "agentic-helpers@agentic-helpers"

const DefaultEntryMaxWords = 500

// The verdicts shell-guard's outside-project and find -delete guards take.
const (
	ShellGuardAsk  = "ask"
	ShellGuardDeny = "deny"
	ShellGuardOff  = "off"
)

// DefaultShellGuardAskTimeout / MaxShellGuardAskTimeout bound the seconds
// shell-guard's ask waits for an answer in auto and bypassPermissions mode. The
// ceiling stays under the PermissionRequest hook's own timeout in hooks.json,
// past which Claude Code stops the hook before it can deny.
const (
	DefaultShellGuardAskTimeout = 120
	MaxShellGuardAskTimeout     = 3600
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

// DefaultBriefNoteLimit is how many notes the brief prints, hottest first.
const DefaultBriefNoteLimit = 10

type Options struct {
	EmbeddingProvider   string
	EmbeddingModel      string
	EmbeddingEndpoint   string
	EmbeddingDimensions int
	EmbeddingAPIKeyEnv  string
	EntryMaxWords       int
	RetiredBodyDays     int
	EntryIDLength       int
	BriefNoteLimit      int
	MemoryLanguage      string
	MemoryRequireGit    bool
	// MemoryTurnsOffAuto has a seat claim turn Claude Code's own auto memory
	// off for the project through its `.claude/settings.local.json`.
	MemoryTurnsOffAuto bool
	// TemporarySeatLifetime is how long a closed temporary shelf keeps its
	// entries, as `lifetime` spells a span; empty takes agentic-memory's default.
	TemporarySeatLifetime string
	// ShellGuardOutsideProject / ShellGuardFindDelete / ShellGuardSecrets are
	// `ask`, `deny` or `off`; the other shell-guard options switch a guard that
	// denies when on.
	ShellGuardOutsideProject string
	ShellGuardFindDelete     string
	ShellGuardSecrets        string
	ShellGuardLoneRemoval    bool
	ShellGuardExpansion      bool
	ShellGuardNestedCode     bool
	ShellGuardMktemp         bool
	// ShellGuardAskTimeout is the seconds an ask waits in auto and
	// bypassPermissions mode before it denies; 0 waits without limit.
	ShellGuardAskTimeout int
	ToolsDuplicateFloor  float64
	ToolsQueryFloor      float64
	// MemoryEnabled ... ShellGuardEnabled switch each piece on or off for the
	// machine; every one is on unless set.
	MemoryEnabled        bool
	ToolsEnabled         bool
	ScratchEnabled       bool
	GroundingGateEnabled bool
	CaptureEnabled       bool
	ShellGuardEnabled    bool
}

// Keys is every option the plugin's userConfig declares, as a settings file
// and the hook environment spell it.
var Keys = []string{
	"embedding_provider", "embedding_model", "embedding_endpoint", "embedding_dimensions", "embedding_api_key_env",
	"entry_max_words", "retired_body_days", "entry_id_length", "brief_note_limit", "memory_require_git", "memory_language",
	"memory_turns_off_auto",
	"temporary_seat_lifetime", "tools_query_floor", "tools_duplicate_floor",
	"shell_guard_outside_project", "shell_guard_find_delete", "shell_guard_lone_removal", "shell_guard_expansion",
	"shell_guard_nested_code", "shell_guard_mktemp", "shell_guard_ask_timeout", "shell_guard_secrets",
	"memory_enabled", "tools_enabled", "scratch_enabled", "grounding_gate_enabled", "capture_enabled",
	"shell_guard_enabled",
}

type settings struct {
	PluginConfigs map[string]struct {
		Options map[string]json.RawMessage `json:"options"`
	} `json:"pluginConfigs"`
}

// Paths is every settings file that can carry `pluginConfigs`, lowest
// precedence first: the user's, under CLAUDE_CONFIG_DIR where it is set, then
// the managed settings and their `managed-settings.d/` drop-ins in name order,
// as Claude Code merges them. Project settings are never among them: one
// embedder serves every project on the machine.
func Paths(home string, goos string, getenv func(string) string) []string {
	config := filepath.Join(home, ".claude")
	if getenv != nil && getenv("CLAUDE_CONFIG_DIR") != "" {
		config = getenv("CLAUDE_CONFIG_DIR")
	}

	managed := map[string]string{
		"darwin": "/Library/Application Support/ClaudeCode", "linux": "/etc/claude-code",
	}[goos]

	return pathsUnder(config, managed)
}

func pathsUnder(config string, managed string) []string {
	paths := []string{filepath.Join(config, "settings.json")}

	if managed == "" {
		return paths
	}

	paths = append(paths, filepath.Join(managed, "managed-settings.json"))

	dropIns, err := filepath.Glob(filepath.Join(managed, "managed-settings.d", "*.json"))
	if err == nil {
		slices.Sort(dropIns)
		paths = append(paths, dropIns...)
	}

	return paths
}

// ReadWith folds the user's settings, paths[0] as Paths orders them, then the
// hook environment's CLAUDE_PLUGIN_OPTION_<KEY> variables, then the managed
// settings, paths[1:], which no environment overrides: they are how a machine
// locks a piece on.
func ReadWith(paths []string, getenv func(string) string) (Options, error) {
	if getenv == nil {
		return Read(paths)
	}

	user, managed := paths[:min(1, len(paths))], paths[min(1, len(paths)):]
	options := Defaults()

	err := options.fold(user)
	if err != nil {
		return Options{}, err
	}

	values := map[string]json.RawMessage{}

	for _, key := range Keys {
		value := getenv("CLAUDE_PLUGIN_OPTION_" + strings.ToUpper(key))
		if value == "" {
			continue
		}

		raw, err := json.Marshal(value)
		if err != nil {
			return Options{}, fmt.Errorf("encoding %s: %w", key, err)
		}

		values[key] = raw
	}

	err = options.apply(values)
	if err != nil {
		return Options{}, fmt.Errorf("reading the hook environment's options: %w", err)
	}

	err = options.fold(managed)
	if err != nil {
		return Options{}, err
	}

	options.clamp()

	return options, nil
}

// Read folds the plugin's options from paths in order, a later file winning
// per key. A missing file contributes nothing; an unreadable one is an error.
func Read(paths []string) (Options, error) {
	options := Defaults()

	err := options.fold(paths)
	if err != nil {
		return Options{}, err
	}

	options.clamp()

	return options, nil
}

func (options *Options) fold(paths []string) error {
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}

		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}

		var parsed settings

		err = json.Unmarshal(raw, &parsed)
		if err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}

		err = options.apply(parsed.PluginConfigs[PluginID].Options)
		if err != nil {
			return fmt.Errorf("reading the %s options in %s: %w", PluginID, path, err)
		}
	}

	return nil
}

func (options *Options) clamp() {
	options.RetiredBodyDays = max(options.RetiredBodyDays, MinRetiredBodyDays)
	options.EntryIDLength = min(max(options.EntryIDLength, MinEntryIDLength), MaxEntryIDLength)
	options.BriefNoteLimit = max(options.BriefNoteLimit, 0)
	options.ShellGuardAskTimeout = min(max(options.ShellGuardAskTimeout, 0), MaxShellGuardAskTimeout)
}

func Defaults() Options {
	return Options{
		EntryMaxWords: DefaultEntryMaxWords, RetiredBodyDays: DefaultRetiredBodyDays, EntryIDLength: DefaultEntryIDLength,
		BriefNoteLimit:   DefaultBriefNoteLimit,
		MemoryRequireGit: true, MemoryTurnsOffAuto: true, ToolsDuplicateFloor: DefaultToolsDuplicateFloor,
		ToolsQueryFloor:          DefaultToolsQueryFloor,
		ShellGuardOutsideProject: ShellGuardAsk, ShellGuardFindDelete: ShellGuardAsk, ShellGuardSecrets: ShellGuardAsk,
		ShellGuardLoneRemoval: true, ShellGuardExpansion: true, ShellGuardNestedCode: true, ShellGuardMktemp: true,
		ShellGuardAskTimeout: DefaultShellGuardAskTimeout,
		MemoryEnabled:        true, ToolsEnabled: true, ScratchEnabled: true, GroundingGateEnabled: true, CaptureEnabled: true,
		ShellGuardEnabled: true,
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
		"embedding_dimensions":    &options.EmbeddingDimensions,
		"entry_max_words":         &options.EntryMaxWords,
		"retired_body_days":       &options.RetiredBodyDays,
		"entry_id_length":         &options.EntryIDLength,
		"brief_note_limit":        &options.BriefNoteLimit,
		"shell_guard_ask_timeout": &options.ShellGuardAskTimeout,
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
		"shell_guard_secrets":         &options.ShellGuardSecrets,
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
		"memory_turns_off_auto":    &options.MemoryTurnsOffAuto,
		"shell_guard_lone_removal": &options.ShellGuardLoneRemoval,
		"shell_guard_expansion":    &options.ShellGuardExpansion,
		"shell_guard_nested_code":  &options.ShellGuardNestedCode,
		"shell_guard_mktemp":       &options.ShellGuardMktemp,
		"memory_enabled":           &options.MemoryEnabled,
		"tools_enabled":            &options.ToolsEnabled,
		"scratch_enabled":          &options.ScratchEnabled,
		"grounding_gate_enabled":   &options.GroundingGateEnabled,
		"capture_enabled":          &options.CaptureEnabled,
		"shell_guard_enabled":      &options.ShellGuardEnabled,
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
