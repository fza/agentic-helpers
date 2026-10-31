package pieces_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/pieces"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

// layeredEnv is a config.yaml and user settings under one folder, as Paths
// orders them.
func layeredEnv(t *testing.T, config string, options string) pieces.Env {
	t.Helper()

	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "config.yaml"), filepath.Join(dir, "settings.json")}

	if config != "" {
		test.WriteFile(t, paths[0], config)
	}

	test.WriteFile(t, paths[1], `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{`+options+`}}}}`)

	// timeout installed, so the session start names no substitution.
	return pieces.Env{Settings: paths, Getenv: test.Getenv(test.Stubs(t, "timeout"))}
}

func runConfig(t *testing.T, env pieces.Env, args ...string) (int, string, string) {
	t.Helper()

	var captured test.Captured

	code := pieces.Main(context.Background(), append([]string{"config"}, args...), env, captured.Streams(""))

	return code, captured.Out.String(), captured.Err.String()
}

func TestConfigListsEveryKeyWithItsLayer(t *testing.T) {
	env := layeredEnv(t, "entry_max_words: 600\nretired_body_lifetime: 120d\nbrief_note_limit: many\n", `"memory_language":"de"`)

	code, out, _ := runConfig(t, env)
	if code != 0 {
		t.Fatalf("config should exit 0, got %d", code)
	}

	for _, want := range []string{
		"entry_max_words                 600  (config.yaml)\n",
		"memory_language                 de  (user settings)\n",
		"retired_body_lifetime           120d  (config.yaml, never a plugin option)\n",
		"embedding_provider              auto  (default)\n",
		"embedding_model                 \"\"  (default)\n",
		"temporary_seat_lifetime         1d  (default, never a plugin option)\n",
		"orphan_store_grace              30d  (default, never a plugin option)\n",
		"problem: " + env.Settings[0] + ": brief_note_limit: not a number",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("config should list each key's value and layer and every problem, missing %q in:\n%s", want, out)
		}
	}

	code, out, _ = runConfig(t, env, "get", "memory_language")
	if code != 0 || out != "memory_language  de  (user settings)\n" {
		t.Errorf("config get should print one key's line, got %d %q", code, out)
	}

	code, _, errOut := runConfig(t, env, "get", "memory_languge")
	if code != 1 || !strings.Contains(errOut, "the nearest is memory_language") {
		t.Errorf("config get of an unknown key should be refused naming the nearest, got %d %q", code, errOut)
	}
}

func TestConfigSetWritesAndNamesAnOverridingLayer(t *testing.T) {
	env := layeredEnv(t, "# mine\n", `"memory_language":"de"`)

	code, out, _ := runConfig(t, env, "set", "entry_max_words", "650")
	if code != 0 || out != "entry_max_words set to 650 in "+env.Settings[0]+"\n" {
		t.Errorf("config set should say what it wrote where, got %d %q", code, out)
	}

	code, out, _ = runConfig(t, env, "set", "memory_language", "fr")
	if code != 0 || !strings.Contains(out, "user settings also sets memory_language and wins over config.yaml: de applies\n") {
		t.Errorf("config set should name the layer that overrides it, got %d %q", code, out)
	}

	written, _ := os.ReadFile(env.Settings[0])
	if string(written) != "# mine\nentry_max_words: 650\nmemory_language: \"fr\"\n" {
		t.Errorf("config set should write config.yaml, keeping its comment, got:\n%s", written)
	}

	code, _, errOut := runConfig(t, env, "set", "shell_guard_secrets", "sometimes")
	if code != 1 || !strings.Contains(errOut, "refused: shell_guard_secrets") {
		t.Errorf("config set should refuse a value the reader refuses, got %d %q", code, errOut)
	}

	code, _, errOut = runConfig(t, pieces.Env{Settings: env.Settings[1:]}, "set", "entry_max_words", "650")
	if code != 1 || !strings.Contains(errOut, "refused: no config.yaml to write") {
		t.Errorf("config set without a config.yaml path should be refused, got %d %q", code, errOut)
	}
}

func TestTheSessionStartCheckMigratesAndNamesProblems(t *testing.T) {
	env := layeredEnv(t, "tools_query_floor: 3\n", `"tools_total_mb":250`)
	env.Hook = true

	var captured test.Captured

	code := pieces.Main(context.Background(), []string{"check"}, env, captured.Streams(""))
	if code != 0 {
		t.Fatalf("check should exit 0, got %d", code)
	}

	if out := captured.Out.String(); out != "agentic-helpers: "+env.Settings[0]+": tools_query_floor: 3 lies outside (0, 1]; the layer below applies\n" {
		t.Errorf("a session start should name each config.yaml problem and nothing else, got %q", out)
	}

	written, _ := os.ReadFile(env.Settings[0])
	if !strings.Contains(string(written), "tools_total_mb: 250\n") {
		t.Errorf("a session start should copy a config-only answer into config.yaml, got:\n%s", written)
	}
}

func TestConfigYAMLLoosensNoGuardAndSetsNoEmbedderKey(t *testing.T) {
	env := layeredEnv(t, "shell_guard_enabled: false\nshell_guard_find_delete: deny\nembedding_endpoint: \"http://collector.example\"\n", "")
	env.Hook = true

	var captured test.Captured

	code := pieces.Main(context.Background(), []string{"check"}, env, captured.Streams(""))
	if code != 0 {
		t.Fatalf("check should exit 0, got %d", code)
	}

	want := "agentic-helpers: config.yaml never sets embedding_endpoint; http://collector.example ignored\n" +
		"agentic-helpers: config.yaml may only tighten shell_guard_enabled; false ignored\n"
	if out := captured.Out.String(); out != want {
		t.Errorf("a session start should name each ignored value, got %q", out)
	}

	code, out, _ := runConfig(t, env)
	for _, line := range []string{
		"shell_guard_enabled             true  (default, config.yaml only tightens)\n",
		"shell_guard_find_delete         deny  (config.yaml, config.yaml only tightens)\n",
		"embedding_endpoint              \"\"  (default, never config.yaml)\n",
	} {
		if code != 0 || !strings.Contains(out, line) {
			t.Errorf("config should list what config.yaml never sets or only tightens, missing %q in:\n%s", line, out)
		}
	}

	before, _ := os.ReadFile(env.Settings[0])

	for _, refused := range []struct{ key, value, line string }{
		{key: "shell_guard_secrets", value: "off", line: "agentic-helpers: refused: config.yaml may only tighten shell_guard_secrets; loosen it in Claude Code's settings\n"},
		{key: "embedding_api_key_env", value: "GITHUB_TOKEN", line: "agentic-helpers: refused: config.yaml never sets embedding_api_key_env; set it in Claude Code's settings\n"},
	} {
		code, _, errOut := runConfig(t, env, "set", refused.key, refused.value)
		if code != 1 || errOut != refused.line {
			t.Errorf("config set should refuse a value config.yaml may not hold, got %d %q", code, errOut)
		}
	}

	after, _ := os.ReadFile(env.Settings[0])
	if string(after) != string(before) {
		t.Error("a refused config set should leave config.yaml as it was")
	}
}

const projectEndpointLine = "agentic-helpers: sdd's embedding endpoint is set in this repository's .sdd/ (layer project), " +
	"which a clone can bring along; search by meaning stays off until `sdd config set` sets it in sdd's global layer, " +
	"or embedding_provider and embedding_endpoint set one here\n"

const projectPathLine = "agentic-helpers: sdd's embedding path is set in this repository's .sdd/ (layer project), " +
	"which a clone can bring along; search by meaning stays off until `sdd config set` sets it in sdd's global layer, " +
	"or embedding_provider and embedding_endpoint set one here\n"

// sourceLine is the line naming an embedding_source a layer sets.
func sourceLine(value string, layer string) string {
	return "agentic-helpers: embedding_source is " + value + " (" + layer + ") and still applies; " +
		"embedding_provider replaces it: auto, openai, ollama or off\n"
}

func TestTheSessionStartNamesWhatAnSddEmbeddingSourceLeavesUnused(t *testing.T) {
	const block = "embedding.provider  ollama  (global)\nembedding.model  nomic  (global)\n"

	cases := []struct {
		name    string
		config  string
		options string
		sdd     string
		want    string
		asked   int
	}{
		{name: "auto says nothing", options: `"embedding_model":"own"`, want: "", asked: 1},
		{name: "auto with no sdd block says nothing", options: `"memory_language":"de"`, want: "", asked: 1},
		{
			name: "auto on an endpoint the repository sets", options: `"memory_language":"de"`,
			sdd: block + "embedding.endpoint  http://example.org:9  (project)\n", want: projectEndpointLine, asked: 1,
		},
		{
			name: "auto on a path the repository sets", options: `"memory_language":"de"`,
			sdd: block + "embedding.path  /collect  (project)\n", want: projectPathLine, asked: 1,
		},
		{
			name: "sdd on an endpoint the repository sets", config: "embedding_source: sdd\n", options: `"memory_language":"de"`,
			sdd: block + "embedding.endpoint  http://example.org:9  (project)\n", want: sourceLine("sdd", "config.yaml") + projectEndpointLine,
			asked: 1,
		},
		{
			name: "own on an endpoint the repository sets", config: "embedding_source: own\n", options: `"memory_language":"de"`,
			sdd: block + "embedding.endpoint  http://example.org:9  (project)\n", want: sourceLine("own", "config.yaml"),
		},
		{
			name: "sdd with sdd's block and no own key", config: "embedding_source: sdd\n", options: `"memory_language":"de"`, sdd: block,
			want: sourceLine("sdd", "config.yaml"), asked: 1,
		},
		{
			name: "sdd with own keys set", config: "embedding_source: sdd\nembedding_timeout: 30s\n", options: `"embedding_model":"own"`, sdd: block,
			want:  sourceLine("sdd", "config.yaml") + "agentic-helpers: embedding_source is sdd; embedding_model, embedding_timeout ignored\n",
			asked: 1,
		},
		{
			name: "sdd with an own path set", config: "embedding_source: sdd\n", options: `"embedding_path":"/v3/compat/embeddings"`,
			sdd: block, want: sourceLine("sdd", "config.yaml") + "agentic-helpers: embedding_source is sdd; embedding_path ignored\n",
			asked: 1,
		},
		{
			name: "sdd with an own provider set", config: "embedding_source: sdd\nembedding_provider: openai\n", options: `"memory_language":"de"`,
			sdd: block, want: sourceLine("sdd", "config.yaml") + "agentic-helpers: embedding_source is sdd; embedding_provider ignored\n",
			asked: 1,
		},
		{
			name: "sdd with the provider at auto", config: "embedding_source: sdd\nembedding_provider: auto\n", options: `"memory_language":"de"`,
			sdd: block, want: sourceLine("sdd", "config.yaml"), asked: 1,
		},
		{
			name: "sdd answering no block", config: "embedding_source: sdd\n", options: `"memory_language":"de"`,
			want:  sourceLine("sdd", "config.yaml") + "agentic-helpers: embedding_source is sdd, but sdd sets no embedder here; search by meaning is off\n",
			asked: 1,
		},
		{
			name: "sdd under an off provider", config: "embedding_source: sdd\nembedding_provider: off\n", options: `"embedding_model":"own"`,
			sdd: block, want: sourceLine("sdd", "config.yaml"),
		},
		{
			name: "an off provider on an endpoint the repository sets", config: "embedding_provider: off\n", options: `"memory_language":"de"`,
			sdd: block + "embedding.endpoint  http://example.org:9  (project)\n", want: "",
		},
		{
			name: "a source the dialog saved", options: `"embedding_source":"own"`, sdd: block, want: sourceLine("own", "user settings"),
		},
		{
			name: "a source the dialog saved untouched", options: `"embedding_source":"auto"`, sdd: block, want: "", asked: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := layeredEnv(t, tc.config, tc.options)
			env.Hook = true
			sdd := &test.SddEmbedding{Block: tc.sdd}
			env.SddConfig = sdd

			var captured test.Captured

			code := pieces.Main(context.Background(), []string{"check"}, env, captured.Streams(""))
			if code != 0 {
				t.Fatalf("check should exit 0, got %d", code)
			}

			if out := captured.Out.String(); out != tc.want {
				t.Errorf("a session start should name a set source and exactly what an sdd source leaves unused, got %q", out)
			}

			if sdd.Asked() != tc.asked {
				t.Errorf("the session start should run sdd only where its block applies, ran %d times", sdd.Asked())
			}
		})
	}
}
