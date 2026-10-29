// Package pluginconfig reads the plugin's options in layers, lowest first: the
// built-in defaults, the agent-neutral `config.yaml`, the answers a user gave
// Claude Code's `userConfig` dialog, then Claude Code's managed settings,
// which win. Claude Code exports the answers only to hook processes, so a
// command the Bash tool runs from the plugin's `bin/` reads them from the
// settings files themselves.
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

const DefaultStateStaleAfterWrites = 5

// The verdicts shell-guard's outside-project and find -delete guards take.
const (
	ShellGuardAsk  = "ask"
	ShellGuardDeny = "deny"
	ShellGuardOff  = "off"
)

// SecretOutputAll / SecretOutputFiles are the settings of
// `shell_guard_secret_output` besides `off`: scan every tool's output from
// outside the project, or only that of Read, Grep, NotebookRead, Edit, Write,
// NotebookEdit and Bash.
const (
	SecretOutputAll   = "all"
	SecretOutputFiles = "files"
)

// StyleTerse / StylePlain are the settings of `writing_style`: how agents
// write what only agents read, memory entries and tool library notes.
const (
	StyleTerse = "terse"
	StylePlain = "plain"
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

// DefaultAttachmentMaxMB / DefaultAttachmentsTotalMB bound memory's
// attachments, per file and across every project on the machine, in megabytes
// of the original bytes.
const (
	DefaultAttachmentMaxMB    = 10
	DefaultAttachmentsTotalMB = 2048
)

// ToolMaxMB is the fixed size of one tool version; DefaultToolsTotalMB bounds
// every version of every tool library on the machine.
const (
	ToolMaxMB           = 1
	DefaultToolsTotalMB = 500
)

type Options struct {
	EmbeddingProvider   string
	EmbeddingModel      string
	EmbeddingEndpoint   string
	EmbeddingDimensions int
	EmbeddingAPIKeyEnv  string
	EntryMaxWords       int
	// StateStaleAfterWrites is how many file writes since a seat's last state
	// write hold the session's Stop for a new one.
	StateStaleAfterWrites int
	RetiredBodyDays       int
	EntryIDLength         int
	BriefNoteLimit        int
	AttachmentMaxMB       int
	AttachmentsTotalMB    int
	ToolsTotalMB          int
	MemoryLanguage        string
	WritingStyle          string
	MemoryRequireGit      bool
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
	// ShellGuardSecretOutput is `all`, `files` or `off`;
	// ShellGuardSecretOutputSkip the comma-separated tool names or patterns
	// whose output is never redacted.
	ShellGuardSecretOutput     string
	ShellGuardSecretOutputSkip string
	// ShellGuardAskTimeout is the seconds an ask waits in auto and
	// bypassPermissions mode before it denies; 0 waits without limit.
	ShellGuardAskTimeout int
	ToolsDuplicateFloor  float64
	ToolsQueryFloor      float64
	CaptureRemoveDraft   bool
	// MemoryEnabled ... ShellGuardEnabled switch each piece on or off for the
	// machine; every one is on unless set.
	MemoryEnabled        bool
	ToolsEnabled         bool
	ScratchEnabled       bool
	GroundingGateEnabled bool
	CaptureEnabled       bool
	ShellGuardEnabled    bool
}

// Keys is every option the plugin reads, as `config.yaml`, a settings file
// and the hook environment spell it.
var Keys = []string{
	"embedding_provider", "embedding_model", "embedding_endpoint", "embedding_dimensions", "embedding_api_key_env",
	"entry_max_words", "state_stale_after_writes", "retired_body_days", "entry_id_length", "brief_note_limit", "attachment_max_mb", "attachments_total_mb", "tools_total_mb", "memory_require_git", "memory_language", "writing_style",
	"memory_turns_off_auto",
	"temporary_seat_lifetime", "tools_query_floor", "tools_duplicate_floor", "capture_remove_draft",
	"shell_guard_outside_project", "shell_guard_find_delete", "shell_guard_lone_removal", "shell_guard_expansion",
	"shell_guard_nested_code", "shell_guard_mktemp", "shell_guard_ask_timeout", "shell_guard_secrets",
	"shell_guard_secret_output", "shell_guard_secret_output_skip",
	"memory_enabled", "tools_enabled", "scratch_enabled", "grounding_gate_enabled", "capture_enabled",
	"shell_guard_enabled",
}

// ConfigOnly is every key that decides when stored data every agent shares is
// deleted. An agent's own option layer never sets one: two agents set apart
// would leave the lowest value deciding for both, whichever process ran the
// cleanup. Only `config.yaml` and managed settings set them, and the plugin's
// `userConfig` declares none.
var ConfigOnly = []string{"retired_body_days", "temporary_seat_lifetime", "tools_total_mb"}

// LayerDefault ... LayerManaged name where an option's value comes from.
const (
	LayerDefault  = "default"
	LayerConfig   = "config.yaml"
	LayerUser     = "user settings"
	LayerAgent    = "plugin option"
	LayerManaged  = "managed settings"
	configFileExt = ".yaml"
)

type settings struct {
	PluginConfigs map[string]struct {
		Options map[string]json.RawMessage `json:"options"`
	} `json:"pluginConfigs"`
}

// Paths is every file that can set an option, lowest precedence first:
// `config.yaml`, then the user's settings, under CLAUDE_CONFIG_DIR where it is
// set, then the managed settings and their `managed-settings.d/` drop-ins in
// name order, as Claude Code merges them. Project settings are never among
// them: one embedder serves every project on the machine.
func Paths(home string, goos string, getenv func(string) string) []string {
	config := filepath.Join(home, ".claude")
	if getenv != nil && getenv("CLAUDE_CONFIG_DIR") != "" {
		config = getenv("CLAUDE_CONFIG_DIR")
	}

	managed := map[string]string{
		"darwin": "/Library/Application Support/ClaudeCode", "linux": "/etc/claude-code",
	}[goos]

	return append([]string{ConfigPath(home, getenv)}, pathsUnder(config, managed)...)
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

// Resolution is the options in effect, the layer each key set apart from its
// default comes from, and what in `config.yaml` was skipped and why.
type Resolution struct {
	Options  Options
	From     map[string]string
	Problems []string
}

// Resolve folds paths as Paths orders them: each leading `config.yaml`, then
// the user's settings, then the hook environment's
// CLAUDE_PLUGIN_OPTION_<KEY> variables, then the remaining, managed settings,
// which no environment overrides: they are how a machine locks a piece on. An
// invalid value in `config.yaml` is skipped, leaving the layer below it, and
// named among the problems; one in a settings file or the environment is an
// error. An empty value is unset.
func Resolve(paths []string, getenv func(string) string) (Resolution, error) {
	resolution := Resolution{Options: Defaults(), From: map[string]string{}}

	for len(paths) > 0 && filepath.Ext(paths[0]) == configFileExt {
		resolution.foldConfig(paths[0])
		paths = paths[1:]
	}

	user, managed := paths[:min(1, len(paths))], paths[min(1, len(paths)):]

	err := resolution.foldSettings(user, LayerUser, true)
	if err != nil {
		return Resolution{}, err
	}

	if getenv != nil {
		values := map[string]json.RawMessage{}

		for _, key := range Keys {
			value := getenv("CLAUDE_PLUGIN_OPTION_" + strings.ToUpper(key))
			if value == "" {
				continue
			}

			raw, err := json.Marshal(value)
			if err != nil {
				return Resolution{}, fmt.Errorf("encoding %s: %w", key, err)
			}

			values[key] = raw
		}

		err = resolution.take(values, LayerAgent, true)
		if err != nil {
			return Resolution{}, fmt.Errorf("reading the hook environment's options: %w", err)
		}
	}

	err = resolution.foldSettings(managed, LayerManaged, false)
	if err != nil {
		return Resolution{}, err
	}

	resolution.Options.clamp()

	return resolution, nil
}

// ReadWith is the options Resolve settles.
func ReadWith(paths []string, getenv func(string) string) (Options, error) {
	resolution, err := Resolve(paths, getenv)

	return resolution.Options, err
}

// Read folds the options from paths in order, a later file winning per key,
// without a hook environment. A missing file contributes nothing; an
// unreadable settings file is an error.
func Read(paths []string) (Options, error) {
	return ReadWith(paths, nil)
}

func (resolution *Resolution) foldSettings(paths []string, layer string, agent bool) error {
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

		err = resolution.take(parsed.PluginConfigs[PluginID].Options, layer, agent)
		if err != nil {
			return fmt.Errorf("reading the %s options in %s: %w", PluginID, path, err)
		}
	}

	return nil
}

// take applies one layer's values, all or none; an agent's layer never sets
// a ConfigOnly key.
func (resolution *Resolution) take(values map[string]json.RawMessage, layer string, agent bool) error {
	set := map[string]json.RawMessage{}

	for key, raw := range values {
		if unset(raw) || agent && slices.Contains(ConfigOnly, key) {
			continue
		}

		set[key] = raw
	}

	err := resolution.Options.apply(set)
	if err != nil {
		return err
	}

	for key := range set {
		if slices.Contains(Keys, key) {
			resolution.From[key] = layer
		}
	}

	return nil
}

func unset(raw json.RawMessage) bool {
	text := strings.TrimSpace(string(raw))

	return text == "" || text == "null" || text == `""`
}

func (options *Options) clamp() {
	options.RetiredBodyDays = max(options.RetiredBodyDays, MinRetiredBodyDays)
	options.EntryIDLength = min(max(options.EntryIDLength, MinEntryIDLength), MaxEntryIDLength)
	options.BriefNoteLimit = max(options.BriefNoteLimit, 0)
	options.StateStaleAfterWrites = max(options.StateStaleAfterWrites, 1)
	options.AttachmentMaxMB = max(options.AttachmentMaxMB, 1)
	// An overall limit below its per-item limit is raised to it: one item
	// must always fit.
	options.AttachmentsTotalMB = max(options.AttachmentsTotalMB, options.AttachmentMaxMB)
	options.ToolsTotalMB = max(options.ToolsTotalMB, ToolMaxMB)
	options.ShellGuardAskTimeout = min(max(options.ShellGuardAskTimeout, 0), MaxShellGuardAskTimeout)
}

func Defaults() Options {
	return Options{
		EntryMaxWords: DefaultEntryMaxWords, StateStaleAfterWrites: DefaultStateStaleAfterWrites, RetiredBodyDays: DefaultRetiredBodyDays, EntryIDLength: DefaultEntryIDLength,
		BriefNoteLimit: DefaultBriefNoteLimit, AttachmentMaxMB: DefaultAttachmentMaxMB, AttachmentsTotalMB: DefaultAttachmentsTotalMB,
		ToolsTotalMB: DefaultToolsTotalMB, MemoryRequireGit: true, MemoryTurnsOffAuto: true, ToolsDuplicateFloor: DefaultToolsDuplicateFloor,
		ToolsQueryFloor: DefaultToolsQueryFloor, CaptureRemoveDraft: true, WritingStyle: StyleTerse,
		ShellGuardOutsideProject: ShellGuardAsk, ShellGuardFindDelete: ShellGuardAsk, ShellGuardSecrets: ShellGuardAsk,
		ShellGuardLoneRemoval: true, ShellGuardExpansion: true, ShellGuardNestedCode: true, ShellGuardMktemp: true,
		ShellGuardAskTimeout: DefaultShellGuardAskTimeout, ShellGuardSecretOutput: SecretOutputAll,
		MemoryEnabled: true, ToolsEnabled: true, ScratchEnabled: true, GroundingGateEnabled: true, CaptureEnabled: true,
		ShellGuardEnabled: true,
	}
}

// fields is every option by its key, grouped by how its value reads.
type fields struct {
	texts     map[string]*string
	numbers   map[string]*int
	fractions map[string]*float64
	modes     map[string]*string
	outputs   map[string]*string
	styles    map[string]*string
	switches  map[string]*bool
}

func (options *Options) fields() fields {
	return fields{
		texts: map[string]*string{
			"embedding_provider":             &options.EmbeddingProvider,
			"embedding_model":                &options.EmbeddingModel,
			"embedding_endpoint":             &options.EmbeddingEndpoint,
			"embedding_api_key_env":          &options.EmbeddingAPIKeyEnv,
			"memory_language":                &options.MemoryLanguage,
			"temporary_seat_lifetime":        &options.TemporarySeatLifetime,
			"shell_guard_secret_output_skip": &options.ShellGuardSecretOutputSkip,
		},
		numbers: map[string]*int{
			"embedding_dimensions":     &options.EmbeddingDimensions,
			"entry_max_words":          &options.EntryMaxWords,
			"state_stale_after_writes": &options.StateStaleAfterWrites,
			"retired_body_days":        &options.RetiredBodyDays,
			"entry_id_length":          &options.EntryIDLength,
			"brief_note_limit":         &options.BriefNoteLimit,
			"attachment_max_mb":        &options.AttachmentMaxMB,
			"attachments_total_mb":     &options.AttachmentsTotalMB,
			"tools_total_mb":           &options.ToolsTotalMB,
			"shell_guard_ask_timeout":  &options.ShellGuardAskTimeout,
		},
		fractions: map[string]*float64{
			"tools_duplicate_floor": &options.ToolsDuplicateFloor,
			"tools_query_floor":     &options.ToolsQueryFloor,
		},
		modes: map[string]*string{
			"shell_guard_outside_project": &options.ShellGuardOutsideProject,
			"shell_guard_find_delete":     &options.ShellGuardFindDelete,
			"shell_guard_secrets":         &options.ShellGuardSecrets,
		},
		outputs: map[string]*string{
			"shell_guard_secret_output": &options.ShellGuardSecretOutput,
		},
		styles: map[string]*string{
			"writing_style": &options.WritingStyle,
		},
		switches: map[string]*bool{
			"memory_require_git":       &options.MemoryRequireGit,
			"memory_turns_off_auto":    &options.MemoryTurnsOffAuto,
			"shell_guard_lone_removal": &options.ShellGuardLoneRemoval,
			"shell_guard_expansion":    &options.ShellGuardExpansion,
			"shell_guard_nested_code":  &options.ShellGuardNestedCode,
			"shell_guard_mktemp":       &options.ShellGuardMktemp,
			"capture_remove_draft":     &options.CaptureRemoveDraft,
			"memory_enabled":           &options.MemoryEnabled,
			"tools_enabled":            &options.ToolsEnabled,
			"scratch_enabled":          &options.ScratchEnabled,
			"grounding_gate_enabled":   &options.GroundingGateEnabled,
			"capture_enabled":          &options.CaptureEnabled,
			"shell_guard_enabled":      &options.ShellGuardEnabled,
		},
	}
}

// Value is the option's value as `config set` takes it; empty for a key the
// plugin does not read.
func (options Options) Value(key string) string {
	field := options.fields()

	if target, found := field.texts[key]; found {
		return *target
	}

	if target, found := field.numbers[key]; found {
		return strconv.Itoa(*target)
	}

	if target, found := field.fractions[key]; found {
		return strconv.FormatFloat(*target, 'f', -1, 64)
	}

	if target, found := field.modes[key]; found {
		return *target
	}

	if target, found := field.outputs[key]; found {
		return *target
	}

	if target, found := field.styles[key]; found {
		return *target
	}

	if target, found := field.switches[key]; found {
		return strconv.FormatBool(*target)
	}

	return ""
}

func (options *Options) apply(values map[string]json.RawMessage) error {
	field := options.fields()

	for key, target := range field.texts {
		raw, found := values[key]
		if !found {
			continue
		}

		err := json.Unmarshal(raw, target)
		if err != nil {
			return fmt.Errorf("%s is not a string: %w", key, err)
		}
	}

	for key, target := range field.numbers {
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

	for key, target := range field.fractions {
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

	for key, target := range field.modes {
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

	for key, target := range field.outputs {
		raw, found := values[key]
		if !found {
			continue
		}

		value, err := outputMode(raw, *target)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}

		*target = value
	}

	for key, target := range field.styles {
		raw, found := values[key]
		if !found {
			continue
		}

		value, err := style(raw, *target)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}

		*target = value
	}

	for key, target := range field.switches {
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

// outputMode accepts `all`, `files` or `off`; an empty string keeps fallback.
func outputMode(raw json.RawMessage, fallback string) (string, error) {
	var value string

	err := json.Unmarshal(raw, &value)
	if err != nil {
		return "", fmt.Errorf("%s is not a string", raw)
	}

	switch value {
	case "":
		return fallback, nil
	case SecretOutputAll, SecretOutputFiles, ShellGuardOff:
		return value, nil
	}

	return "", fmt.Errorf("%q is none of all, files, off", value)
}

// style accepts `terse` or `plain`; an empty string keeps fallback.
func style(raw json.RawMessage, fallback string) (string, error) {
	var value string

	err := json.Unmarshal(raw, &value)
	if err != nil {
		return "", fmt.Errorf("%s is not a string", raw)
	}

	switch value {
	case "":
		return fallback, nil
	case StyleTerse, StylePlain:
		return value, nil
	}

	return "", fmt.Errorf("%q is none of terse, plain", value)
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
