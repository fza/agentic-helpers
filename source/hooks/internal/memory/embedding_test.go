package memory_test

import (
	"context"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/memory"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestAnOffProviderOrOwnSourceLeavesMemoryWithoutAnEmbedderAndSdd(t *testing.T) {
	const block = "embedding.provider  ollama  (global)\nembedding.model  sdd-model  (global)\n"

	cases := []struct {
		name     string
		config   string
		embedder bool
	}{
		{name: "an auto provider follows sdd", config: "", embedder: true},
		{name: "an off provider", config: "embedding_provider: off\n"},
		{name: "an off provider over an sdd source", config: "embedding_provider: off\nembedding_source: sdd\n"},
		{name: "an own source with the provider at auto", config: "embedding_source: own\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")

			// Recall speaks from 20 live entries on.
			for _, word := range unrelated[:19] {
				h.file("Unrelated finding about "+word, word+" "+word+" "+word)
			}

			test.WriteFile(t, h.config, tc.config+"memory_language: en\n")

			sdd := &test.SddEmbedding{Block: block}

			run := func(stdin string, args ...string) (int, string, string) {
				env := h.env()
				env.SddConfig = sdd
				env.Embedders = test.SettledEmbedders(h.embedder)

				var captured test.Captured

				code := memory.Main(context.Background(), args, env, captured.Streams(stdin))

				return code, captured.Out.String(), captured.Err.String()
			}

			code, out, errOut := run("only Bash reads count", "new", "--title", "Gate ignores MCP reads always", "--topic", "gate")
			if code != 0 {
				t.Fatalf("filing should work with or without an embedder, exit %d: %s%s", code, out, errOut)
			}

			target := idPattern.FindString(out)

			// Recall comes first: a search printing the entry marks it seen.
			code, out, _ = run(hookPayload("", "", "why does the gate ignore MCP reads"), "prompt")
			if code != 0 || strings.Contains(out, target) != tc.embedder {
				t.Errorf("a prompt should recall entries only with an embedder, exit %d: %q", code, out)
			}

			if !tc.embedder && out != "" {
				t.Errorf("a prompt without an embedder configured should print nothing, got %q", out)
			}

			code, out, _ = run("", "search", "--term", "MCP")
			if code != 0 || !strings.Contains(out, "Gate ignores MCP reads always") {
				t.Errorf("a keyword search should answer with or without an embedder, exit %d: %s", code, out)
			}

			code, _, errOut = run("", "search", "--query", "gate blind to tool calls")
			if (code == 0) != tc.embedder {
				t.Errorf("a search by meaning should answer only with an embedder, exit %d: %s", code, errOut)
			}

			if (sdd.Asked() > 0) != tc.embedder {
				t.Errorf("sdd should run only where its embedder applies, ran %d times", sdd.Asked())
			}
		})
	}
}
