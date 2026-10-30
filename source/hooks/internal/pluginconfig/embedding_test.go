package pluginconfig_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const sddEmbeddingBlock = "embedding.provider  ollama  (global)\nembedding.model  sdd-model  (global)\n"

// settledEmbedder resolves config.yaml as written and settles the embedder
// the options choose against sdd printing block, as memory and the tool
// library do; it answers the embedder and how often sdd ran.
func settledEmbedder(t *testing.T, config string, block string) (embed.Config, int) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	test.WriteFile(t, path, config+"embedding_model: own-model\n")

	resolution, err := pluginconfig.Resolve([]string{path}, nil)
	if err != nil || len(resolution.Problems) != 0 {
		t.Fatalf("resolving should succeed without a problem: %v %v", err, resolution.Problems)
	}

	sdd := &test.SddEmbedding{Block: block}
	options := resolution.Options

	settled, _ := embed.Settle(context.Background(), options.OwnEmbedding(nil), options.EmbeddingFromSdd(), sdd, "/project")

	return settled, sdd.Asked()
}

func TestTheProviderAndASetSourceChooseTheEmbedder(t *testing.T) {
	const (
		unset = "(unset)"
		empty = `""`
	)

	// fromSdd: sdd's block applies; own: the own provider that applies where
	// it does not, empty for no embedder.
	cases := []struct {
		provider, source string
		fromSdd          bool
		own              string
	}{
		{provider: unset, source: unset, fromSdd: true},
		{provider: empty, source: unset, fromSdd: true},
		{provider: "auto", source: unset, fromSdd: true},
		{provider: "openai", source: unset, own: "openai"},
		{provider: "ollama", source: unset, own: "ollama"},
		{provider: "off", source: unset},
		{provider: unset, source: "auto", fromSdd: true},
		{provider: empty, source: "auto", fromSdd: true},
		{provider: "auto", source: "auto", fromSdd: true},
		{provider: "openai", source: "auto", own: "openai"},
		{provider: "ollama", source: "auto", own: "ollama"},
		{provider: "off", source: "auto"},
		{provider: unset, source: "sdd", fromSdd: true},
		{provider: empty, source: "sdd", fromSdd: true},
		{provider: "auto", source: "sdd", fromSdd: true},
		{provider: "openai", source: "sdd", fromSdd: true},
		{provider: "ollama", source: "sdd", fromSdd: true},
		{provider: "off", source: "sdd"},
		{provider: unset, source: "own"},
		{provider: empty, source: "own"},
		{provider: "auto", source: "own"},
		{provider: "openai", source: "own", own: "openai"},
		{provider: "ollama", source: "own", own: "ollama"},
		{provider: "off", source: "own"},
	}

	settledFor := map[string]embed.Config{}

	for _, tc := range cases {
		for _, block := range []string{sddEmbeddingBlock, ""} {
			name := "provider " + tc.provider + ", source " + tc.source + ", sdd block " + map[bool]string{true: "set", false: "none"}[block != ""]

			t.Run(name, func(t *testing.T) {
				var config strings.Builder

				if tc.provider != unset {
					config.WriteString("embedding_provider: " + tc.provider + "\n")
				}

				if tc.source != unset {
					config.WriteString("embedding_source: " + tc.source + "\n")
				}

				settled, asked := settledEmbedder(t, config.String(), block)
				settledFor[name] = settled

				want, wantModel, wantAsked := tc.own, "own-model", 0
				if tc.fromSdd {
					want, wantModel, wantAsked = "", "", 1
					if block != "" {
						want, wantModel = "ollama", "sdd-model"
					}
				}

				if settled.Provider != want || want != "" && settled.Model != wantModel {
					t.Errorf("the embedder should come from where the provider and source point, got %+v", settled)
				}

				if asked != wantAsked {
					t.Errorf("sdd should run only where its block applies, ran %d times", asked)
				}
			})
		}
	}

	for name, settled := range settledFor {
		if !strings.HasPrefix(name, "provider auto,") {
			continue
		}

		unsetName := "provider " + unset + strings.TrimPrefix(name, "provider auto")
		if settled != settledFor[unsetName] {
			t.Errorf("%s should settle exactly as an unset provider does, got %+v", name, settled)
		}
	}
}

func TestAProviderOutsideThePickerIsRefused(t *testing.T) {
	resolution := layered(t, "embedding_provider", layers{config: "gemini"})

	if got := resolution.Options.Value("embedding_provider"); got != "auto" {
		t.Errorf("an unknown provider in config.yaml should leave the layer below, got %q", got)
	}

	if len(resolution.Problems) != 1 || !strings.Contains(resolution.Problems[0], `"gemini" is none of auto, openai, ollama, off`) {
		t.Errorf("an unknown provider should be named with the accepted ones, got %v", resolution.Problems)
	}

	dir := t.TempDir()
	user := filepath.Join(dir, "settings.json")
	test.WriteFile(t, user, own("embedding_provider", `"gemini"`))

	_, err := pluginconfig.Resolve([]string{user}, nil)
	if err == nil || !strings.Contains(err.Error(), `"gemini" is none of auto, openai, ollama, off`) {
		t.Errorf("an unknown provider in a settings file should be refused, got %v", err)
	}

	_, err = pluginconfig.Resolve(nil, func(name string) string {
		if name == "CLAUDE_PLUGIN_OPTION_EMBEDDING_PROVIDER" {
			return "gemini"
		}

		return ""
	})
	if err == nil {
		t.Error("an unknown provider in the hook environment should be refused")
	}
}

func TestAnEmptyProviderReadsAsAuto(t *testing.T) {
	for _, value := range []layers{{config: `""`}, {user: `""`}, {managed: `""`}} {
		resolution := layered(t, "embedding_provider", value)

		if resolution.Options.Value("embedding_provider") != "auto" || resolution.From["embedding_provider"] != "" {
			t.Errorf("an empty provider should read as auto from no layer, got %q from %q",
				resolution.Options.Value("embedding_provider"), resolution.From["embedding_provider"])
		}
	}
}

// A layer that set `embedding_source` before it left the dialog keeps
// deciding: an answer saved in the dialog, the hook environment, managed
// settings and config.yaml alike.
func TestASetEmbeddingSourceKeepsApplyingFromEveryLayer(t *testing.T) {
	cases := []struct {
		name   string
		values layers
		layer  string
	}{
		{name: "config.yaml", values: layers{config: "own"}, layer: pluginconfig.LayerConfig},
		{name: "a saved dialog answer", values: layers{user: `"own"`}, layer: pluginconfig.LayerUser},
		{name: "the hook environment", values: layers{env: "own"}, layer: pluginconfig.LayerAgent},
		{name: "managed settings", values: layers{managed: `"own"`}, layer: pluginconfig.LayerManaged},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolution := layered(t, "embedding_source", tc.values)

			if resolution.Options.EmbeddingFromSdd() || resolution.From["embedding_source"] != tc.layer {
				t.Errorf("a set source should still keep sdd's block away, got from %q", resolution.From["embedding_source"])
			}
		})
	}
}
