package plugin_test

import (
	"encoding/json"
	"fmt"
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
		return slices.Contains(pluginconfig.ConfigOnly, key) || slices.Contains(pluginconfig.Undeclared, key)
	})
	slices.Sort(read)

	if !slices.Equal(read, userConfigKeys(t)) {
		t.Errorf("the plugin should declare every option it reads but those config.yaml alone sets and those it no longer offers, read %v", read)
	}
}

// embedding_source left the dialog for embedding_provider and is the one key
// read without being declared. Made ConfigOnly instead, its saved dialog
// answers would stop applying.
func TestOnlyTheEmbeddingSourceIsReadWithoutBeingDeclared(t *testing.T) {
	if !slices.Equal(pluginconfig.Undeclared, []string{"embedding_source"}) {
		t.Errorf("only embedding_source should be read without being declared, got %v", pluginconfig.Undeclared)
	}

	for _, key := range pluginconfig.Undeclared {
		if !slices.Contains(pluginconfig.Keys, key) || slices.Contains(pluginconfig.ConfigOnly, key) {
			t.Errorf("%s should stay read from every layer, a saved dialog answer included", key)
		}
	}
}

// Claude Code refuses a picker without a default, and its dialog saved untouched
// stores every boolean and picker: false for a boolean declaring no default. A
// boolean or picker therefore declares the built-in default, which the reader
// takes as unset; a text or number field is not stored empty and declares none.
func TestOnlyABooleanOrPickerDeclaresADefaultAndAPickerOffersWhatTheReaderTakes(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(pluginRoot, ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatalf("reading plugin.json: %v", err)
	}

	var manifest struct {
		UserConfig map[string]struct {
			Type    string          `json:"type"`
			Default json.RawMessage `json:"default"`
			Options []string        `json:"options"`
		} `json:"userConfig"`
	}

	err = json.Unmarshal(raw, &manifest)
	if err != nil {
		t.Fatalf("parsing plugin.json: %v", err)
	}

	builtIn := pluginconfig.Defaults()

	for key, declared := range manifest.UserConfig {
		t.Run(key, func(t *testing.T) {
			if declared.Options == nil && declared.Type != "boolean" {
				if declared.Default != nil {
					t.Error("a text or number option should name its default in its description and declare none")
				}

				return
			}

			var value any

			err := json.Unmarshal(declared.Default, &value)
			if err != nil || value == nil || fmt.Sprint(value) != builtIn.Value(key) {
				t.Errorf("a boolean or picker should declare the reader's built-in default, got %s", declaredText(declared.Default))
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

				// The refusal names every value the reader takes, so the picker
				// offering fewer shows here.
				_, accepted, named := strings.Cut(fmt.Sprint(err), " is none of ")
				if !offered && declared.Options != nil && (!named || accepted != strings.Join(declared.Options, ", ")) {
					t.Errorf("the picker should offer every value the reader takes, the reader said %v", err)
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

func declaredText(value json.RawMessage) string {
	if value == nil {
		return "(none)"
	}

	return string(value)
}
