package pluginconfig_test

import (
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func defaultsWith(change func(options *pluginconfig.Options)) pluginconfig.Options {
	options := pluginconfig.Defaults()
	change(&options)

	return options
}

func TestRead(t *testing.T) {
	const own = `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":`

	cases := []struct {
		name    string
		user    string
		managed string
		want    pluginconfig.Options
		wantErr bool
	}{
		{
			name: "no settings files leave the defaults",
			want: pluginconfig.Defaults(),
		},
		{
			name: "user settings configure the embedder",
			user: own + `{
				"embedding_provider":"openai","embedding_model":"gemma","embedding_endpoint":"http://127.0.0.1:1235",
				"embedding_dimensions":256,"embedding_api_key_env":"OPENAI_API_KEY","entry_max_words":"700"}}}}`,
			want: defaultsWith(func(options *pluginconfig.Options) {
				options.EmbeddingProvider, options.EmbeddingModel = "openai", "gemma"
				options.EmbeddingEndpoint, options.EmbeddingDimensions = "http://127.0.0.1:1235", 256
				options.EmbeddingAPIKeyEnv, options.EntryMaxWords = "OPENAI_API_KEY", 700
			}),
		},
		{
			name:    "managed settings win per key",
			user:    own + `{"embedding_provider":"openai","embedding_model":"a"}}}}`,
			managed: own + `{"embedding_model":"b"}}}}`,
			want: defaultsWith(func(options *pluginconfig.Options) {
				options.EmbeddingProvider, options.EmbeddingModel = "openai", "b"
			}),
		},
		{
			name: "another plugin's options are ignored",
			user: `{"pluginConfigs":{"other@market":{"options":{"embedding_model":"x"}}}}`,
			want: pluginconfig.Defaults(),
		},
		{
			name: "a retired body keeps the days set",
			user: own + `{"retired_body_days":120}}}}`,
			want: defaultsWith(func(options *pluginconfig.Options) { options.RetiredBodyDays = 120 }),
		},
		{
			name: "a retired body is kept at least the revive window",
			user: own + `{"retired_body_days":"7"}}}}`,
			want: defaultsWith(func(options *pluginconfig.Options) { options.RetiredBodyDays = pluginconfig.MinRetiredBodyDays }),
		},
		{
			name: "an entry id takes the length set",
			user: own + `{"entry_id_length":8}}}}`,
			want: defaultsWith(func(options *pluginconfig.Options) { options.EntryIDLength = 8 }),
		},
		{
			name: "an entry id is never shorter than the minimum",
			user: own + `{"entry_id_length":3}}}}`,
			want: defaultsWith(func(options *pluginconfig.Options) { options.EntryIDLength = pluginconfig.MinEntryIDLength }),
		},
		{
			name: "an entry id is never longer than the maximum",
			user: own + `{"entry_id_length":"40"}}}}`,
			want: defaultsWith(func(options *pluginconfig.Options) { options.EntryIDLength = pluginconfig.MaxEntryIDLength }),
		},
		{
			name: "memory works only inside git unless switched",
			want: defaultsWith(func(options *pluginconfig.Options) { options.MemoryRequireGit = true }),
		},
		{
			name: "the git limit lifts",
			user: own + `{"memory_require_git":false}}}}`,
			want: defaultsWith(func(options *pluginconfig.Options) { options.MemoryRequireGit = false }),
		},
		{
			name: "the git limit lifts from a stored string",
			user: own + `{"memory_require_git":"false"}}}}`,
			want: defaultsWith(func(options *pluginconfig.Options) { options.MemoryRequireGit = false }),
		},
		{
			name: "an entry language",
			user: own + `{"memory_language":"de"}}}}`,
			want: defaultsWith(func(options *pluginconfig.Options) { options.MemoryLanguage = "de" }),
		},
		{
			name:    "a git limit that is no boolean is an error",
			user:    own + `{"memory_require_git":"sometimes"}}}}`,
			wantErr: true,
		},
		{
			name:    "a malformed settings file is an error",
			user:    `{`,
			wantErr: true,
		},
		{
			name:    "a non-numeric word limit is an error",
			user:    own + `{"entry_max_words":"many"}}}}`,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			user := filepath.Join(dir, "settings.json")
			managed := filepath.Join(dir, "managed-settings.json")

			if tc.user != "" {
				test.WriteFile(t, user, tc.user)
			}

			if tc.managed != "" {
				test.WriteFile(t, managed, tc.managed)
			}

			got, err := pluginconfig.Read([]string{user, managed})
			if tc.wantErr {
				if err == nil {
					t.Fatal("a bad settings file should be refused")
				}

				return
			}

			if err != nil {
				t.Fatalf("reading settings should succeed: %v", err)
			}

			if got != tc.want {
				t.Errorf("options should fold the settings in precedence order, got %+v", got)
			}
		})
	}
}

func TestPathsNeverIncludeProjectSettings(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		for _, path := range pluginconfig.Paths("/home/u", goos) {
			if filepath.Base(filepath.Dir(path)) == ".claude" && filepath.Dir(filepath.Dir(path)) != "/home/u" {
				t.Errorf("only user and managed settings should be read, got %s", path)
			}
		}

		if pluginconfig.Paths("/home/u", goos)[0] != "/home/u/.claude/settings.json" {
			t.Errorf("user settings should come first, with the lowest precedence")
		}
	}
}
