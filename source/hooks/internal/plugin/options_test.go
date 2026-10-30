package plugin_test

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
)

func userConfigKeys(t *testing.T) []string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(pluginRoot, ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatalf("reading plugin.json: %v", err)
	}

	var manifest struct {
		UserConfig map[string]json.RawMessage `json:"userConfig"`
	}

	err = json.Unmarshal(raw, &manifest)
	if err != nil {
		t.Fatalf("parsing plugin.json: %v", err)
	}

	keys := make([]string, 0, len(manifest.UserConfig))
	for key := range manifest.UserConfig {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}

func TestTheReadmeListsEveryOption(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join(repository, "README.md"))
	if err != nil {
		t.Fatalf("reading the README: %v", err)
	}

	_, after, found := strings.Cut(string(readme), "\n## Options\n")
	section, _, _ := strings.Cut(after, "\n## ")

	if !found {
		t.Fatal("the README should carry an Options section")
	}

	for _, key := range pluginconfig.Keys {
		if !strings.Contains(section, "`"+key+"`") {
			t.Errorf("the README's Options section should name the option %s", key)
		}
	}

	if !strings.Contains(string(readme), "\n## Dependencies\n") {
		t.Error("the README should carry a Dependencies section")
	}
}

func TestEveryDeclaredOptionIsReadAndNoConfigOnlyOneIsDeclared(t *testing.T) {
	read := slices.DeleteFunc(slices.Clone(pluginconfig.Keys), func(key string) bool {
		return slices.Contains(pluginconfig.ConfigOnly, key)
	})
	slices.Sort(read)

	if !slices.Equal(read, userConfigKeys(t)) {
		t.Errorf("the plugin should declare every option it reads but those config.yaml alone sets, read %v", read)
	}
}

// Claude Code refuses a picker without a default and hands a hook no value for
// an option nobody answered, so a picker's default is the built-in one and
// config.yaml still decides; every other option declares none.
func TestOnlyAPickerDeclaresADefaultAndOffersWhatTheReaderTakes(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(pluginRoot, ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatalf("reading plugin.json: %v", err)
	}

	var manifest struct {
		UserConfig map[string]struct {
			Default *string  `json:"default"`
			Options []string `json:"options"`
		} `json:"userConfig"`
	}

	err = json.Unmarshal(raw, &manifest)
	if err != nil {
		t.Fatalf("parsing plugin.json: %v", err)
	}

	builtIn := pluginconfig.Defaults()

	for key, declared := range manifest.UserConfig {
		t.Run(key, func(t *testing.T) {
			if declared.Options == nil {
				if declared.Default != nil {
					t.Error("an option offering no picker should name its default in its description and declare none")
				}

				return
			}

			if declared.Default == nil || *declared.Default != builtIn.Value(key) {
				t.Errorf("a picker should declare the reader's built-in default, got %q", declaredText(declared.Default))
			}

			for _, value := range append(slices.Clone(declared.Options), "not-a-choice") {
				options, err := pluginconfig.ReadWith(nil, func(name string) string {
					if name == "CLAUDE_PLUGIN_OPTION_"+strings.ToUpper(key) {
						return value
					}

					return ""
				})

				offered := slices.Contains(declared.Options, value)
				if offered && (err != nil || options.Value(key) != value) {
					t.Errorf("the reader should take every value the picker offers, %q gave %q, %v", value, options.Value(key), err)
				}

				if !offered && err == nil {
					t.Errorf("the reader should refuse a value outside the picker, got %q", options.Value(key))
				}
			}
		})
	}
}

type boundedOption struct {
	Type string   `json:"type"`
	Min  *float64 `json:"min"`
	Max  *float64 `json:"max"`
}

// readOption is the value the option reader settles on for key answered
// with value, and whether it accepted the answer at all.
func readOption(key string, value float64) (float64, bool) {
	options, err := pluginconfig.ReadWith(nil, func(name string) string {
		if name == "CLAUDE_PLUGIN_OPTION_"+strings.ToUpper(key) {
			return strconv.FormatFloat(value, 'f', -1, 64)
		}

		return ""
	})
	if err != nil {
		return 0, false
	}

	read, err := strconv.ParseFloat(options.Value(key), 64)

	return read, err == nil
}

// The dialog offers only what the option reader keeps: a bound accepted, a
// value beyond it refused or clamped back inside. A minimum is the static
// floor: a reader raising one option to another's value (attachments_total_mb
// to attachment_max_mb) keeps it at least there.
func TestEveryNumberOptionIsBoundedAsTheReaderBoundsIt(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(pluginRoot, ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatalf("reading plugin.json: %v", err)
	}

	var manifest struct {
		UserConfig map[string]boundedOption `json:"userConfig"`
	}

	err = json.Unmarshal(raw, &manifest)
	if err != nil {
		t.Fatalf("parsing plugin.json: %v", err)
	}

	for key, declared := range manifest.UserConfig {
		if declared.Type != "number" {
			continue
		}

		t.Run(key, func(t *testing.T) {
			if declared.Min == nil {
				t.Fatal("a number option should declare its lower bound")
			}

			step := 1.0
			if *declared.Min != math.Trunc(*declared.Min) {
				step = *declared.Min
			}

			bounds := []float64{*declared.Min}
			if declared.Max != nil {
				bounds = append(bounds, *declared.Max)
			}

			for index, bound := range bounds {
				read, accepted := readOption(key, bound)
				if !accepted || read < *declared.Min || (index == 1 && read != bound) {
					t.Errorf("the reader should keep a bound the dialog offers, got %v, accepted %t", read, accepted)
				}
			}

			read, accepted := readOption(key, *declared.Min-step)
			if accepted && read < *declared.Min {
				t.Errorf("the reader should refuse or raise a value below the declared minimum, got %v", read)
			}

			if declared.Max != nil {
				read, accepted = readOption(key, *declared.Max+step)
				if accepted && read > *declared.Max {
					t.Errorf("the reader should refuse or lower a value above the declared maximum, got %v", read)
				}
			}
		})
	}
}

func declaredText(value *string) string {
	if value == nil {
		return "(none)"
	}

	return *value
}
