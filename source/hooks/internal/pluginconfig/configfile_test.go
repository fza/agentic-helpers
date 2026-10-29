package pluginconfig_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

type layers struct {
	config, user, env, managed string
}

// layered writes each layer's value for key and resolves them all.
func layered(t *testing.T, key string, values layers) pluginconfig.Resolution {
	t.Helper()

	dir := t.TempDir()
	config := filepath.Join(dir, "config.yaml")
	user := filepath.Join(dir, "settings.json")
	managed := filepath.Join(dir, "managed-settings.json")

	if values.config != "" {
		test.WriteFile(t, config, key+": "+values.config+"\n")
	}

	if values.user != "" {
		test.WriteFile(t, user, own(key, values.user))
	}

	if values.managed != "" {
		test.WriteFile(t, managed, own(key, values.managed))
	}

	resolution, err := pluginconfig.Resolve([]string{config, user, managed}, func(name string) string {
		if name == "CLAUDE_PLUGIN_OPTION_"+strings.ToUpper(key) {
			return values.env
		}

		return ""
	})
	if err != nil {
		t.Fatalf("resolving the layers should succeed: %v", err)
	}

	return resolution
}

func own(key string, value string) string {
	return `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"` + key + `":` + value + `}}}}`
}

func TestEveryLayerResolvesInOrderManagedSettingsLast(t *testing.T) {
	cases := []struct {
		name   string
		key    string
		values layers
		value  string
		layer  string
	}{
		{name: "nothing set", key: "entry_max_words", value: "500"},
		{name: "config.yaml", key: "entry_max_words", values: layers{config: "600"}, value: "600", layer: pluginconfig.LayerConfig},
		{name: "user settings over config.yaml", key: "entry_max_words", values: layers{config: "600", user: "700"}, value: "700", layer: pluginconfig.LayerUser},
		{name: "the hook environment over user settings", key: "entry_max_words", values: layers{config: "600", user: "700", env: "800"}, value: "800", layer: pluginconfig.LayerAgent},
		{name: "managed settings over everything", key: "entry_max_words", values: layers{config: "600", user: "700", env: "800", managed: "900"}, value: "900", layer: pluginconfig.LayerManaged},
		{name: "an empty user answer is unset", key: "entry_max_words", values: layers{config: "600", user: `""`}, value: "600", layer: pluginconfig.LayerConfig},
		{name: "an empty config.yaml value is unset", key: "entry_max_words", values: layers{config: `""`}, value: "500"},
		{name: "a config-only key from user settings has no effect", key: "retired_body_days", values: layers{user: "120"}, value: "90"},
		{name: "a config-only key from the hook environment has no effect", key: "retired_body_days", values: layers{config: "100", env: "120"}, value: "100", layer: pluginconfig.LayerConfig},
		{name: "a retired body kept at least the revive window", key: "retired_body_days", values: layers{config: "7"}, value: "14", layer: pluginconfig.LayerConfig},
		{name: "a config-only key from config.yaml", key: "tools_total_mb", values: layers{config: "250", user: "300"}, value: "250", layer: pluginconfig.LayerConfig},
		{name: "a config-only key from managed settings", key: "temporary_seat_lifetime", values: layers{config: `"2d"`, managed: `"12h"`}, value: "12h", layer: pluginconfig.LayerManaged},
		{name: "a writing style", key: "writing_style", values: layers{config: "plain"}, value: "plain", layer: pluginconfig.LayerConfig},
		{name: "terse writing unless set", key: "writing_style", value: "terse"},
		{name: "a word YAML 1.1 would read as a boolean", key: "shell_guard_secrets", values: layers{config: "off"}, value: "off", layer: pluginconfig.LayerConfig},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolution := layered(t, tc.key, tc.values)

			if got := resolution.Options.Value(tc.key); got != tc.value {
				t.Errorf("the highest layer setting the key should decide its value, got %q", got)
			}

			if got := resolution.From[tc.key]; got != tc.layer {
				t.Errorf("the value should name the layer it comes from, got %q", got)
			}

			if len(resolution.Problems) != 0 {
				t.Errorf("valid layers should raise no problem, got %v", resolution.Problems)
			}
		})
	}
}

func TestAnInvalidConfigValueFallsBackAndIsNamed(t *testing.T) {
	cases := []struct {
		name    string
		config  string
		value   string
		problem string
	}{
		{name: "a word for a number", config: "entry_max_words: many\nbrief_note_limit: 4\n", value: "500", problem: "entry_max_words: not a number"},
		{name: "a floor outside its range", config: "tools_query_floor: 2\n", value: "500", problem: "tools_query_floor: 2 lies outside"},
		{name: "a writing style of another word", config: "writing_style: florid\n", value: "500", problem: `writing_style: "florid" is none of terse, plain`},
		{name: "a key that is no option", config: "entry_max_word: 600\n", value: "500", problem: "entry_max_word is no option; the nearest is entry_max_words"},
		{name: "a file that is no YAML map", config: "- entry_max_words\n", value: "500", problem: "is no YAML map of options"},
		{name: "a file past the size every hook reads", config: "entry_max_words: 600\n#" + strings.Repeat("x", 64<<10), value: "500", problem: "is over 64 KiB"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			config := filepath.Join(dir, "config.yaml")
			test.WriteFile(t, config, tc.config)

			resolution, err := pluginconfig.Resolve([]string{config, filepath.Join(dir, "settings.json")}, nil)
			if err != nil {
				t.Fatalf("an invalid config.yaml should never fail the read: %v", err)
			}

			if got := resolution.Options.Value("entry_max_words"); got != tc.value {
				t.Errorf("the layer below an invalid value should apply, got %q", got)
			}

			if len(resolution.Problems) != 1 || !strings.Contains(resolution.Problems[0], tc.problem) {
				t.Errorf("the problem should name the key and why, got %v", resolution.Problems)
			}
		})
	}

	resolution := layered(t, "brief_note_limit", layers{config: "4"})
	if resolution.Options.BriefNoteLimit != 4 {
		t.Error("a valid key beside an invalid one should still apply")
	}
}

func TestSetWritesOneLineAndKeepsTheRest(t *testing.T) {
	config := filepath.Join(t.TempDir(), "agentic-helpers", "config.yaml")

	err := pluginconfig.Set(config, "entry_max_words", "600")
	if err != nil {
		t.Fatalf("setting a key into a missing file should create it: %v", err)
	}

	test.WriteFile(t, config, "# machine options\nentry_max_words: 600\nmemory_language: \"de\"\n")

	for _, set := range [][2]string{{"entry_max_words", "700"}, {"shell_guard_secrets", "off"}, {"memory_enabled", "false"}, {"tools_query_floor", "0.5"}} {
		err = pluginconfig.Set(config, set[0], set[1])
		if err != nil {
			t.Fatalf("setting %s should succeed: %v", set[0], err)
		}
	}

	written, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}

	want := "# machine options\nentry_max_words: 700\nmemory_language: \"de\"\nshell_guard_secrets: \"off\"\nmemory_enabled: false\ntools_query_floor: 0.5\n"
	if string(written) != want {
		t.Errorf("set should replace the key's line or append one, keeping every other line, got:\n%s", written)
	}

	resolution, err := pluginconfig.Resolve([]string{config}, nil)
	if err != nil || len(resolution.Problems) != 0 {
		t.Fatalf("what set writes should read back cleanly: %v %v", err, resolution.Problems)
	}

	options := resolution.Options
	if options.EntryMaxWords != 700 || options.ShellGuardSecrets != "off" || options.MemoryEnabled || options.MemoryLanguage != "de" || options.ToolsQueryFloor != 0.5 {
		t.Errorf("the reader should take every value set wrote, got %+v", options)
	}
}

func TestSetRefusesWhatTheReaderWould(t *testing.T) {
	config := filepath.Join(t.TempDir(), "config.yaml")

	err := pluginconfig.Set(config, "entry_max_word", "600")
	if !errors.Is(err, pluginconfig.ErrUnknownKey) || !strings.Contains(err.Error(), "the nearest is entry_max_words") {
		t.Errorf("an unknown key should be refused naming the nearest, got %v", err)
	}

	err = pluginconfig.Set(config, "shell_guard_secrets", "sometimes")
	if err == nil {
		t.Error("a value the reader refuses should be refused")
	}

	_, err = os.Stat(config)
	if !errors.Is(err, os.ErrNotExist) {
		t.Error("a refused set should write nothing")
	}
}

func TestMigrationCopiesConfigOnlyAnswersAndTouchesNoSettings(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.yaml")
	user := filepath.Join(dir, "settings.json")
	answers := `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"retired_body_days":120,"tools_total_mb":"250","temporary_seat_lifetime":"","entry_max_words":700}}},"env":{"X":"y"}}`
	test.WriteFile(t, user, answers)
	test.WriteFile(t, config, "tools_total_mb: 300\n")

	copied, err := pluginconfig.MigrateAgentAnswers([]string{config, user})
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(copied, []string{"retired_body_days"}) {
		t.Errorf("only a config-only key answered and absent from config.yaml should be copied, got %v", copied)
	}

	written, _ := os.ReadFile(config)
	if string(written) != "tools_total_mb: 300\nretired_body_days: 120\n" {
		t.Errorf("config.yaml should gain the answer and keep its own value, got:\n%s", written)
	}

	settings, _ := os.ReadFile(user)
	if string(settings) != answers {
		t.Error("the migration should leave the settings file as it was")
	}

	again, err := pluginconfig.MigrateAgentAnswers([]string{config, user})
	if err != nil || len(again) != 0 {
		t.Errorf("a second run should copy nothing, got %v %v", again, err)
	}

	refused := filepath.Join(t.TempDir(), "config.yaml")
	test.WriteFile(t, user, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"tools_total_mb":"many"}}}}`)

	copied, err = pluginconfig.MigrateAgentAnswers([]string{refused, user})
	if err != nil || len(copied) != 0 {
		t.Errorf("an answer the reader refuses should stay behind, got %v %v", copied, err)
	}
}

func TestTheConfigFileSitsUnderTheXDGConfigRoot(t *testing.T) {
	cases := []struct {
		name string
		xdg  string
		want string
	}{
		{name: "unset", want: "/home/u/.config/agentic-helpers/config.yaml"},
		{name: "set", xdg: "/xdg", want: "/xdg/agentic-helpers/config.yaml"},
		{name: "relative, which the XDG rules ignore", xdg: "xdg", want: "/home/u/.config/agentic-helpers/config.yaml"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(name string) string { return map[string]string{"XDG_CONFIG_HOME": tc.xdg}[name] }

			if got := pluginconfig.ConfigPath("/home/u", getenv); got != tc.want {
				t.Errorf("config.yaml should sit under the XDG config root, got %s", got)
			}

			if got := pluginconfig.Paths("/home/u", "linux", getenv)[0]; got != tc.want {
				t.Errorf("config.yaml should come first in the paths, with the lowest precedence, got %s", got)
			}
		})
	}
}
