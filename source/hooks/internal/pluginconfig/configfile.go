package pluginconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/fza/agentic-helpers/source/hooks/internal/atomicfile"
	"github.com/fza/agentic-helpers/source/hooks/internal/levenshtein"
)

// maxConfigBytes bounds the `config.yaml` every hook reads: a flat list of
// keys runs to a few kilobytes.
const maxConfigBytes = 64 << 10

var ErrUnknownKey = errors.New("is no option")

// ConfigPath is the agent-neutral options file every agent on the machine
// shares, under the XDG root meant for configuration.
func ConfigPath(home string, getenv func(string) string) string {
	root := filepath.Join(home, ".config")

	if getenv != nil {
		configured := getenv("XDG_CONFIG_HOME")
		if filepath.IsAbs(configured) {
			root = configured
		}
	}

	return filepath.Join(root, "agentic-helpers", "config.yaml")
}

func (resolution *Resolution) foldConfig(path string) {
	values, err := readConfig(path)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}

	if err != nil {
		resolution.Problems = append(resolution.Problems, fmt.Sprintf("%s: %v; none of its values apply", path, err))

		return
	}

	for _, problem := range renameOld(values, LayerConfig) {
		resolution.Problems = append(resolution.Problems, problem)
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	for _, key := range keys {
		if !slices.Contains(Keys, key) {
			resolution.Problems = append(resolution.Problems,
				fmt.Sprintf("%s: %s is no option; the nearest is %s", path, key, levenshtein.Nearest(key, Keys)))

			continue
		}

		raw, err := json.Marshal(values[key])
		if err != nil || unset(raw) {
			continue
		}

		if slices.Contains(NeverConfig, key) {
			resolution.Problems = append(resolution.Problems, fmt.Sprintf("%v %s; %v ignored", ErrNeverConfig, key, values[key]))

			continue
		}

		candidate := resolution.Options

		err = candidate.apply(map[string]json.RawMessage{key: raw})
		if err != nil {
			resolution.Problems = append(resolution.Problems, fmt.Sprintf("%s: %v; the layer below applies", path, err))

			continue
		}

		if candidate.loosens(key, resolution.Options) {
			resolution.Problems = append(resolution.Problems,
				fmt.Sprintf("%v %s; %s ignored", ErrLoosens, key, candidate.Value(key)))

			continue
		}

		resolution.Options = candidate
		resolution.From[key] = LayerConfig
	}
}

func readConfig(path string) (map[string]any, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	defer func() { _ = file.Close() }()

	raw, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil {
		return nil, fmt.Errorf("cannot be read: %w", err)
	}

	if len(raw) > maxConfigBytes {
		return nil, fmt.Errorf("is over %d KiB", maxConfigBytes>>10)
	}

	values := map[string]any{}

	err = yaml.Unmarshal(raw, &values)
	if err != nil {
		return nil, fmt.Errorf("is no YAML map of options: %w", parseError{err})
	}

	return values, nil
}

// parseError is the YAML parser's error without the source excerpt it prints
// beneath, so the problem stays the one line a hook prints.
type parseError struct{ err error }

func (parse parseError) Error() string {
	return strings.Join(strings.Fields(yaml.FormatError(parse.err, false, false)), " ")
}

func (parse parseError) Unwrap() error {
	return parse.err
}

// Set writes key's value into the `config.yaml` at path, through a temporary
// file renamed into place: the key's line replaced where the file has one,
// else appended, every other line and comment kept. The value is checked as
// the reader checks it, a NeverConfig key and a guard value looser than its
// default refused; an empty one unsets the key.
func Set(path string, key string, value string) error {
	line, err := configLine(key, value)
	if err != nil {
		return err
	}

	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	var lines []string

	text := strings.TrimSuffix(string(raw), "\n")
	if text != "" {
		lines = strings.Split(text, "\n")
	}

	own := regexp.MustCompile(`^` + regexp.QuoteMeta(key) + `\s*:`)

	index := slices.IndexFunc(lines, own.MatchString)
	if index < 0 {
		lines = append(lines, line)
	} else {
		lines[index] = line
	}

	err = os.MkdirAll(filepath.Dir(path), 0o755)
	if err != nil {
		return fmt.Errorf("making the folder of %s: %w", path, err)
	}

	err = atomicfile.Write(filepath.Dir(path), path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}

// configLine is key's `config.yaml` line: numbers and switches bare, every
// other value double-quoted, so YAML never reads a word such as `off` as
// anything but the word.
func configLine(key string, value string) (string, error) {
	if !slices.Contains(Keys, key) {
		return "", fmt.Errorf("%s %w; the nearest is %s", key, ErrUnknownKey, levenshtein.Nearest(key, Keys))
	}

	if slices.Contains(NeverConfig, key) {
		return "", fmt.Errorf("%w %s; set it in Claude Code's settings", ErrNeverConfig, key)
	}

	if value == "" {
		return key + `: ""`, nil
	}

	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encoding %s: %w", key, err)
	}

	checked := Defaults()

	err = checked.apply(map[string]json.RawMessage{key: raw})
	if err != nil {
		return "", err
	}

	if checked.loosens(key, Defaults()) {
		return "", fmt.Errorf("%w %s; loosen it in Claude Code's settings", ErrLoosens, key)
	}

	field := checked.fields()

	_, numeric := field.numbers[key]
	_, fractional := field.fractions[key]
	_, switched := field.switches[key]

	if numeric || fractional || switched {
		return key + ": " + checked.Value(key), nil
	}

	return key + ": " + strconv.Quote(value), nil
}

// MigrateAgentAnswers copies each ConfigOnly key the user's settings still
// answer into `config.yaml`, where the file names no value for it, so a value
// set before the key left `userConfig` keeps applying. paths are as Paths
// orders them; no settings file is written. It returns the keys copied.
func MigrateAgentAnswers(paths []string) ([]string, error) {
	if len(paths) < 2 || filepath.Ext(paths[0]) != configFileExt {
		return nil, nil
	}

	config, user := paths[0], paths[1]

	raw, err := os.ReadFile(user)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", user, err)
	}

	var parsed settings

	err = json.Unmarshal(raw, &parsed)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", user, err)
	}

	present, err := readConfig(config)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("reading %s: %w", config, err)
	}

	var copied []string

	for _, key := range ConfigOnly {
		answer, found := parsed.PluginConfigs[PluginID].Options[key]
		if !found || unset(answer) {
			continue
		}

		var value string

		err = json.Unmarshal(answer, &value)
		if err != nil {
			value = strings.TrimSpace(string(answer))
		}

		// An answer under a renamed key lands under its new name, in the
		// new key's form.
		target := key

		renamed, old := Renamed[key]
		if old {
			target = renamed.Key

			_, err = strconv.ParseUint(value, 10, 32)
			if err == nil {
				value += renamed.Unit
			}
		}

		_, named := present[key]
		_, namedTarget := present[target]

		if named || namedTarget {
			continue
		}

		// An answer the reader refuses never applied; it is left behind.
		_, err = configLine(target, value)
		if err != nil {
			continue
		}

		err = Set(config, target, value)
		if err != nil {
			return copied, err
		}

		present[target] = value
		copied = append(copied, target)
	}

	return copied, nil
}

// renameOld moves each Renamed key in values to its new name, its bare number
// given the old unit, unless the new name is set too, and names every old key
// it met. layer is where the old key should be renamed.
func renameOld(values map[string]any, layer string) []string {
	var problems []string

	olds := make([]string, 0, len(Renamed))
	for old := range Renamed {
		olds = append(olds, old)
	}

	slices.Sort(olds)

	for _, old := range olds {
		value, found := values[old]
		if !found {
			continue
		}

		delete(values, old)

		renamed := Renamed[old]
		text := strings.TrimSpace(fmt.Sprint(value))

		_, err := strconv.ParseUint(text, 10, 32)
		if err == nil {
			text += renamed.Unit
		}

		_, taken := values[renamed.Key]
		if !taken {
			values[renamed.Key] = text
		}

		problems = append(problems, fmt.Sprintf("%s is now %s (%s); rename it in %s", old, renamed.Key, text, layer))
	}

	return problems
}
