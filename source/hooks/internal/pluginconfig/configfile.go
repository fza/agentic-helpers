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

var ErrUnknownKey = errors.New("no such option")

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
		return nil, fmt.Errorf("is no YAML map of options: %w", err)
	}

	return values, nil
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

	err = atomicfile.Write(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
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
		return "", fmt.Errorf("%w: %s; the nearest is %s", ErrUnknownKey, key, levenshtein.Nearest(key, Keys))
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

		_, named := present[key]
		if named {
			continue
		}

		var value string

		err = json.Unmarshal(answer, &value)
		if err != nil {
			value = strings.TrimSpace(string(answer))
		}

		// An answer the reader refuses never applied; it is left behind.
		_, err = configLine(key, value)
		if err != nil {
			continue
		}

		err = Set(config, key, value)
		if err != nil {
			return copied, err
		}

		copied = append(copied, key)
	}

	return copied, nil
}
