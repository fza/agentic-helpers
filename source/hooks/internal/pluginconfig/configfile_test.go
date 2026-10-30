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
		{name: "a config-only key from user settings has no effect", key: "retired_body_lifetime", values: layers{user: `"120d"`}, value: "90d"},
		{name: "a config-only key from the hook environment has no effect", key: "retired_body_lifetime", values: layers{config: "100d", env: "120d"}, value: "100d", layer: pluginconfig.LayerConfig},
		{name: "a retired body kept at least the revive window", key: "retired_body_lifetime", values: layers{config: "7d"}, value: "14d", layer: pluginconfig.LayerConfig},
		{name: "a retired body kept for a span", key: "retired_body_lifetime", values: layers{config: "6w"}, value: "42d", layer: pluginconfig.LayerConfig},
		{name: "a tool version kept at least two weeks", key: "tools_version_lifetime", values: layers{config: "3d"}, value: "14d", layer: pluginconfig.LayerConfig},
		{name: "a tool version lifetime from user settings has no effect", key: "tools_version_lifetime", values: layers{user: `"30d"`}, value: "90d"},
		{name: "a config-only key from config.yaml", key: "tools_total_mb", values: layers{config: "250", user: "300"}, value: "250", layer: pluginconfig.LayerConfig},
		{name: "a config-only key from managed settings", key: "temporary_seat_lifetime", values: layers{config: `"2d"`, managed: `"12h"`}, value: "12h", layer: pluginconfig.LayerManaged},
		{name: "a writing style", key: "writing_style", values: layers{config: "plain"}, value: "plain", layer: pluginconfig.LayerConfig},
		{name: "terse writing unless set", key: "writing_style", value: "terse"},
		{name: "a looser guard from user settings over a stricter config.yaml", key: "shell_guard_secrets", values: layers{config: "deny", user: `"off"`}, value: "off", layer: pluginconfig.LayerUser},
		{name: "managed settings over a guard config.yaml tightens", key: "shell_guard_enabled", values: layers{config: "true", env: "true", managed: "false"}, value: "false", layer: pluginconfig.LayerManaged},
		{name: "managed settings over the agent for a key config.yaml never sets", key: "embedding_endpoint", values: layers{user: `"http://user"`, managed: `"http://managed"`}, value: "http://managed", layer: pluginconfig.LayerManaged},
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

func TestConfigYAMLMayOnlyTightenAGuard(t *testing.T) {
	cases := []struct {
		key     string
		config  string
		value   string
		ignored string
	}{
		{key: "shell_guard_enabled", config: "true", value: "true"},
		{key: "shell_guard_enabled", config: "false", value: "true", ignored: "false"},
		{key: "grounding_gate_enabled", config: "true", value: "true"},
		{key: "grounding_gate_enabled", config: "false", value: "true", ignored: "false"},
		{key: "shell_guard_lone_removal", config: "true", value: "true"},
		{key: "shell_guard_lone_removal", config: `"false"`, value: "true", ignored: "false"},
		{key: "shell_guard_expansion", config: "true", value: "true"},
		{key: "shell_guard_expansion", config: "false", value: "true", ignored: "false"},
		{key: "shell_guard_nested_code", config: "true", value: "true"},
		{key: "shell_guard_nested_code", config: "false", value: "true", ignored: "false"},
		{key: "shell_guard_mktemp", config: "true", value: "true"},
		{key: "shell_guard_mktemp", config: "false", value: "true", ignored: "false"},
		{key: "shell_guard_alternatives", config: "true", value: "true"},
		{key: "shell_guard_alternatives", config: "false", value: "true", ignored: "false"},
		{key: "shell_guard_secret_output", config: "all", value: "all"},
		{key: "shell_guard_secret_output", config: "files", value: "all", ignored: "files"},
		{key: "shell_guard_secret_output", config: "off", value: "all", ignored: "off"},
		{key: "shell_guard_outside_project", config: "deny", value: "deny"},
		{key: "shell_guard_outside_project", config: "ask", value: "ask"},
		{key: "shell_guard_outside_project", config: "off", value: "ask", ignored: "off"},
		{key: "shell_guard_find_delete", config: "deny", value: "deny"},
		{key: "shell_guard_find_delete", config: `"off"`, value: "ask", ignored: "off"},
		{key: "shell_guard_secrets", config: "deny", value: "deny"},
		{key: "shell_guard_secrets", config: "off", value: "ask", ignored: "off"},
		{key: "shell_guard_ask_timeout", config: "30", value: "30s"},
		{key: "shell_guard_ask_timeout", config: "45s", value: "45s"},
		{key: "shell_guard_ask_timeout", config: "120", value: "2m"},
		{key: "shell_guard_ask_timeout", config: "2m", value: "2m"},
		{key: "shell_guard_ask_timeout", config: "600", value: "2m", ignored: "10m"},
		{key: "shell_guard_ask_timeout", config: "10m", value: "2m", ignored: "10m"},
		{key: "shell_guard_ask_timeout", config: "5000", value: "2m", ignored: "5000s"},
		{key: "shell_guard_ask_timeout", config: "0", value: "2m", ignored: "0"},
		{key: "shell_guard_ask_timeout", config: "-5", value: "2m", ignored: "-5s"},
	}

	for _, key := range pluginconfig.TightenOnly {
		looser := slices.ContainsFunc(cases, func(tc struct{ key, config, value, ignored string }) bool {
			return tc.key == key && tc.ignored != ""
		})
		if !looser {
			t.Errorf("every tighten-only key should be tried with a looser value, %s is not", key)
		}
	}

	for _, tc := range cases {
		t.Run(tc.key+" "+tc.config, func(t *testing.T) {
			resolution := layered(t, tc.key, layers{config: tc.config})

			if got := resolution.Options.Value(tc.key); got != tc.value {
				t.Errorf("a stricter value should apply and a looser one leave the layer below, got %q", got)
			}

			if tc.ignored == "" {
				if resolution.From[tc.key] != pluginconfig.LayerConfig || len(resolution.Problems) != 0 {
					t.Errorf("a value at least as strict should come from config.yaml without a problem, got %q %v",
						resolution.From[tc.key], resolution.Problems)
				}

				return
			}

			if resolution.From[tc.key] != "" {
				t.Errorf("an ignored value should name no layer, got %q", resolution.From[tc.key])
			}

			want := "config.yaml may only tighten " + tc.key + "; " + tc.ignored + " ignored"
			if !slices.Equal(resolution.Problems, []string{want}) {
				t.Errorf("a looser value should be named as ignored, got %v", resolution.Problems)
			}
		})
	}
}

func TestConfigYAMLNeverSetsTheEmbedderOrTheSkipList(t *testing.T) {
	cases := []struct {
		key   string
		value string
	}{
		{key: "embedding_endpoint", value: "http://collector.example"},
		{key: "embedding_api_key_env", value: "GITHUB_TOKEN"},
		{key: "shell_guard_secret_output_skip", value: "Bash"},
	}

	if len(cases) != len(pluginconfig.NeverConfig) {
		t.Errorf("every key config.yaml never sets should be tried")
	}

	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			resolution := layered(t, tc.key, layers{config: `"` + tc.value + `"`})

			if got := resolution.Options.Value(tc.key); got != "" || resolution.From[tc.key] != "" {
				t.Errorf("config.yaml should change nothing for the key, got %q from %q", got, resolution.From[tc.key])
			}

			want := "config.yaml never sets " + tc.key + "; " + tc.value + " ignored"
			if !slices.Equal(resolution.Problems, []string{want}) {
				t.Errorf("the ignored value should be named, got %v", resolution.Problems)
			}

			resolution = layered(t, tc.key, layers{config: `"elsewhere"`, user: `"` + tc.value + `"`})
			if got := resolution.Options.Value(tc.key); got != tc.value || resolution.From[tc.key] != pluginconfig.LayerUser {
				t.Errorf("Claude Code's settings should still set the key, got %q", got)
			}

			resolution = layered(t, tc.key, layers{env: tc.value})
			if got := resolution.Options.Value(tc.key); got != "" || resolution.From[tc.key] != "" {
				t.Errorf("an environment prefix an agent can set should never set the key, got %q", got)
			}

			resolution = layered(t, tc.key, layers{user: `"` + tc.value + `"`, env: "from-the-environment"})
			if got := resolution.Options.Value(tc.key); got != tc.value || resolution.From[tc.key] != pluginconfig.LayerUser {
				t.Errorf("the user settings should decide the key past any environment value, got %q", got)
			}
		})
	}

	resolution := layered(t, "embedding_model", layers{user: `"from-settings"`, env: "from-the-environment"})
	if got := resolution.Options.Value("embedding_model"); got != "from-the-environment" || resolution.From["embedding_model"] != pluginconfig.LayerAgent {
		t.Errorf("the hook environment should still set every other key, got %q", got)
	}
}

func TestSetRefusesALooserGuardAndEveryKeyConfigNeverSets(t *testing.T) {
	cases := []struct {
		key     string
		value   string
		refusal error
		message string
	}{
		{key: "shell_guard_enabled", value: "false", refusal: pluginconfig.ErrLoosens, message: "config.yaml may only tighten shell_guard_enabled; loosen it in Claude Code's settings"},
		{key: "grounding_gate_enabled", value: "false", refusal: pluginconfig.ErrLoosens, message: "config.yaml may only tighten grounding_gate_enabled; loosen it in Claude Code's settings"},
		{key: "shell_guard_secrets", value: "off", refusal: pluginconfig.ErrLoosens, message: "config.yaml may only tighten shell_guard_secrets; loosen it in Claude Code's settings"},
		{key: "shell_guard_secret_output", value: "files", refusal: pluginconfig.ErrLoosens, message: "config.yaml may only tighten shell_guard_secret_output; loosen it in Claude Code's settings"},
		{key: "shell_guard_ask_timeout", value: "0", refusal: pluginconfig.ErrLoosens, message: "config.yaml may only tighten shell_guard_ask_timeout; loosen it in Claude Code's settings"},
		{key: "shell_guard_ask_timeout", value: "121", refusal: pluginconfig.ErrLoosens, message: "config.yaml may only tighten shell_guard_ask_timeout; loosen it in Claude Code's settings"},
		{key: "embedding_endpoint", value: "http://collector.example", refusal: pluginconfig.ErrNeverConfig, message: "config.yaml never sets embedding_endpoint; set it in Claude Code's settings"},
		{key: "embedding_api_key_env", value: "GITHUB_TOKEN", refusal: pluginconfig.ErrNeverConfig, message: "config.yaml never sets embedding_api_key_env; set it in Claude Code's settings"},
		{key: "embedding_api_key_env", value: "", refusal: pluginconfig.ErrNeverConfig, message: "config.yaml never sets embedding_api_key_env; set it in Claude Code's settings"},
		{key: "shell_guard_secret_output_skip", value: "Bash", refusal: pluginconfig.ErrNeverConfig, message: "config.yaml never sets shell_guard_secret_output_skip; set it in Claude Code's settings"},
		{key: "shell_guard_secrets", value: "deny"},
		{key: "shell_guard_ask_timeout", value: "30"},
		{key: "shell_guard_enabled", value: "true"},
		{key: "shell_guard_enabled", value: ""},
	}

	for _, tc := range cases {
		t.Run(tc.key+" "+tc.value, func(t *testing.T) {
			config := filepath.Join(t.TempDir(), "config.yaml")

			err := pluginconfig.Set(config, tc.key, tc.value)

			_, statErr := os.Stat(config)

			if tc.refusal == nil {
				if err != nil || statErr != nil {
					t.Errorf("a value at least as strict should be written, got %v", err)
				}

				return
			}

			if !errors.Is(err, tc.refusal) || err.Error() != tc.message {
				t.Errorf("the value should be refused saying where it belongs, got %v", err)
			}

			if !errors.Is(statErr, os.ErrNotExist) {
				t.Error("a refused set should write nothing")
			}
		})
	}
}

func TestSetWritesOneLineAndKeepsTheRest(t *testing.T) {
	config := filepath.Join(t.TempDir(), "agentic-helpers", "config.yaml")

	err := pluginconfig.Set(config, "entry_max_words", "600")
	if err != nil {
		t.Fatalf("setting a key into a missing file should create it: %v", err)
	}

	test.WriteFile(t, config, "# machine options\nentry_max_words: 600\nmemory_language: \"de\"\n")

	for _, set := range [][2]string{{"entry_max_words", "700"}, {"shell_guard_secrets", "deny"}, {"memory_enabled", "false"}, {"tools_query_floor", "0.5"}} {
		err = pluginconfig.Set(config, set[0], set[1])
		if err != nil {
			t.Fatalf("setting %s should succeed: %v", set[0], err)
		}
	}

	written, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}

	want := "# machine options\nentry_max_words: 700\nmemory_language: \"de\"\nshell_guard_secrets: \"deny\"\nmemory_enabled: false\ntools_query_floor: 0.5\n"
	if string(written) != want {
		t.Errorf("set should replace the key's line or append one, keeping every other line, got:\n%s", written)
	}

	resolution, err := pluginconfig.Resolve([]string{config}, nil)
	if err != nil || len(resolution.Problems) != 0 {
		t.Fatalf("what set writes should read back cleanly: %v %v", err, resolution.Problems)
	}

	options := resolution.Options
	if options.EntryMaxWords != 700 || options.ShellGuardSecrets != "deny" || options.MemoryEnabled || options.MemoryLanguage != "de" || options.ToolsQueryFloor != 0.5 {
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
	answers := `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"retired_body_days":120,"tools_version_lifetime":"60d","tools_total_mb":"250","temporary_seat_lifetime":"","entry_max_words":700}}},"env":{"X":"y"}}`
	test.WriteFile(t, user, answers)
	test.WriteFile(t, config, "tools_total_mb: 300\n")

	copied, err := pluginconfig.MigrateAgentAnswers([]string{config, user})
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(copied, []string{"tools_version_lifetime", "retired_body_lifetime"}) {
		t.Errorf("only a config-only key answered and absent from config.yaml should be copied, got %v", copied)
	}

	written, _ := os.ReadFile(config)
	if string(written) != "tools_total_mb: 300\ntools_version_lifetime: \"60d\"\nretired_body_lifetime: \"120d\"\n" {
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

func TestARenamedKeyIsReadUnderItsOldNameAndNamed(t *testing.T) {
	cases := []struct {
		name   string
		config string
		value  string
	}{
		{name: "the old name alone", config: "retired_body_days: 120\n", value: "120d"},
		{name: "the old name quoted", config: "retired_body_days: \"30\"\n", value: "30d"},
		{name: "the new name wins", config: "retired_body_days: 120\nretired_body_lifetime: 60d\n", value: "60d"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			config := filepath.Join(dir, "config.yaml")
			test.WriteFile(t, config, tc.config)

			resolution, err := pluginconfig.Resolve([]string{config}, nil)
			if err != nil {
				t.Fatalf("resolving should succeed: %v", err)
			}

			if got := resolution.Options.Value("retired_body_lifetime"); got != tc.value {
				t.Errorf("the renamed key should apply under its new name, got %q", got)
			}

			if len(resolution.Problems) != 1 || !strings.HasPrefix(resolution.Problems[0], "retired_body_days is now retired_body_lifetime (") ||
				!strings.HasSuffix(resolution.Problems[0], "); rename it in config.yaml") {
				t.Errorf("the old name should be named once with its new one, got %v", resolution.Problems)
			}
		})
	}
}

func TestARenamedKeyFromManagedSettingsApplies(t *testing.T) {
	dir := t.TempDir()
	managed := filepath.Join(dir, "managed-settings.json")
	user := filepath.Join(dir, "settings.json")
	test.WriteFile(t, managed, own("retired_body_days", "30"))

	resolution, err := pluginconfig.Resolve([]string{user, managed}, nil)
	if err != nil {
		t.Fatalf("resolving should succeed: %v", err)
	}

	if got := resolution.Options.Value("retired_body_lifetime"); got != "30d" {
		t.Errorf("a managed setting under the old name should still apply, got %q", got)
	}

	if len(resolution.Problems) != 1 || !strings.HasSuffix(resolution.Problems[0], "rename it in managed settings") {
		t.Errorf("the old name should be named, got %v", resolution.Problems)
	}
}

func TestABareNumberIsNoSpanOutsideTheAskTimeout(t *testing.T) {
	for _, value := range []string{"120", `"120"`} {
		t.Run(value, func(t *testing.T) {
			resolution := layered(t, "retired_body_lifetime", layers{config: value})

			if got := resolution.Options.Value("retired_body_lifetime"); got != "90d" {
				t.Errorf("a bare number should leave the layer below, got %q", got)
			}

			if len(resolution.Problems) != 1 || !strings.Contains(resolution.Problems[0], "is no span") {
				t.Errorf("a bare number should be named as no span, got %v", resolution.Problems)
			}
		})
	}
}

func TestMigrationLeavesARenamedAnswerTheFileHoldsUnderItsNewName(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.yaml")
	user := filepath.Join(dir, "settings.json")
	test.WriteFile(t, user, own("retired_body_days", "120"))
	test.WriteFile(t, config, "retired_body_lifetime: 30d\n")

	copied, err := pluginconfig.MigrateAgentAnswers([]string{config, user})
	if err != nil {
		t.Fatal(err)
	}

	written, _ := os.ReadFile(config)
	if len(copied) != 0 || string(written) != "retired_body_lifetime: 30d\n" {
		t.Errorf("an answer the file already holds under the new name should not be copied, got %v:\n%s", copied, written)
	}
}

func TestTheEmbeddingSourceDecidesWhetherSddIsRead(t *testing.T) {
	cases := []struct {
		name    string
		config  string
		fromSdd bool
	}{
		{name: "auto without an own provider", config: "", fromSdd: true},
		{name: "auto with an own provider", config: "embedding_provider: ollama\n", fromSdd: false},
		{name: "sdd with an own provider", config: "embedding_source: sdd\nembedding_provider: ollama\n", fromSdd: true},
		{name: "own without a provider", config: "embedding_source: own\n", fromSdd: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			config := filepath.Join(dir, "config.yaml")
			test.WriteFile(t, config, tc.config+"embedding_timeout: 45s\n")

			resolution, err := pluginconfig.Resolve([]string{config}, nil)
			if err != nil || len(resolution.Problems) != 0 {
				t.Fatalf("resolving should succeed without a problem: %v %v", err, resolution.Problems)
			}

			if resolution.Options.EmbeddingFromSdd() != tc.fromSdd {
				t.Errorf("the source should decide whether sdd's block applies, got %v", resolution.Options.EmbeddingFromSdd())
			}

			if resolution.Options.OwnEmbedding(nil).Timeout.String() != "45s" {
				t.Error("the own embedder should carry the configured timeout")
			}
		})
	}
}

func TestAnEmbeddingSourceOfAnotherWordIsRefused(t *testing.T) {
	resolution := layered(t, "embedding_source", layers{config: "sometimes"})

	if got := resolution.Options.Value("embedding_source"); got != "auto" {
		t.Errorf("an unknown source should leave the layer below, got %q", got)
	}

	if len(resolution.Problems) != 1 || !strings.Contains(resolution.Problems[0], `"sometimes" is none of auto, sdd, own`) {
		t.Errorf("an unknown source should be named with the accepted ones, got %v", resolution.Problems)
	}
}
