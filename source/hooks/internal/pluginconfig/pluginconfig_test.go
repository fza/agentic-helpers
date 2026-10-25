package pluginconfig_test

import (
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestRead(t *testing.T) {
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
			user: `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{
				"embedding_provider":"openai","embedding_model":"gemma","embedding_endpoint":"http://127.0.0.1:1235",
				"embedding_dimensions":256,"embedding_api_key_env":"OPENAI_API_KEY","entry_max_words":"700"}}}}`,
			want: pluginconfig.Options{
				EmbeddingProvider: "openai", EmbeddingModel: "gemma", EmbeddingEndpoint: "http://127.0.0.1:1235",
				EmbeddingDimensions: 256, EmbeddingAPIKeyEnv: "OPENAI_API_KEY", EntryMaxWords: 700,
				RetiredBodyDays: pluginconfig.DefaultRetiredBodyDays,
			},
		},
		{
			name:    "managed settings win per key",
			user:    `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"embedding_provider":"openai","embedding_model":"a"}}}}`,
			managed: `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"embedding_model":"b"}}}}`,
			want: pluginconfig.Options{
				EmbeddingProvider: "openai", EmbeddingModel: "b", EntryMaxWords: pluginconfig.DefaultEntryMaxWords,
				RetiredBodyDays: pluginconfig.DefaultRetiredBodyDays,
			},
		},
		{
			name: "another plugin's options are ignored",
			user: `{"pluginConfigs":{"other@market":{"options":{"embedding_model":"x"}}}}`,
			want: pluginconfig.Defaults(),
		},
		{
			name: "a retired body keeps the days set",
			user: `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"retired_body_days":120}}}}`,
			want: pluginconfig.Options{EntryMaxWords: pluginconfig.DefaultEntryMaxWords, RetiredBodyDays: 120},
		},
		{
			name: "a retired body is kept at least the revive window",
			user: `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"retired_body_days":"7"}}}}`,
			want: pluginconfig.Options{
				EntryMaxWords: pluginconfig.DefaultEntryMaxWords, RetiredBodyDays: pluginconfig.MinRetiredBodyDays,
			},
		},
		{
			name:    "a malformed settings file is an error",
			user:    `{`,
			wantErr: true,
		},
		{
			name:    "a non-numeric word limit is an error",
			user:    `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"entry_max_words":"many"}}}}`,
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
