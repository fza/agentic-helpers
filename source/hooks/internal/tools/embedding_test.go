package tools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/tools"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

// settledRun runs a verb as the binary does: the embedder settled from the
// options against sdd, which embeds nothing where none is settled.
func (h *harness) settledRun(sdd *test.SddEmbedding, stdin string, args ...string) ran {
	h.t.Helper()

	env := h.env()
	env.SddConfig = sdd
	env.Embedders = test.SettledEmbedders(h.embedder)

	var captured test.Captured

	code := tools.Main(context.Background(), args, env, captured.Streams(stdin))

	return ran{out: captured.Out.String(), err: captured.Err.String(), code: code}
}

func TestAnOffProviderOrOwnSourceLeavesTheLibraryWithoutAnEmbedderAndSdd(t *testing.T) {
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
			h.tool("line-count", "Count the lines of a file quickly")
			test.WriteFile(t, h.config, tc.config+"memory_language: en\n")

			sdd := &test.SddEmbedding{Block: block}

			// The offer comes first: it skips a tool find already printed.
			prompt, _ := json.Marshal(map[string]any{"session_id": h.session, "prompt": "count the lines of a file quickly"})

			offered := h.settledRun(sdd, string(prompt), "prompt")
			if strings.Contains(offered.out, "line-count") != tc.embedder {
				t.Errorf("a prompt should be offered near tools only with an embedder, got %q", offered.out)
			}

			found := h.settledRun(sdd, "", "find", "--query", "count the lines of a file")
			if (found.code == 0) != tc.embedder {
				t.Errorf("find --query should answer only with an embedder, exit %d: %s%s", found.code, found.out, found.err)
			}

			near := h.settledRun(sdd, "", "add", "lines", "--from", h.file("lines.py", "lines"), "--summary",
				"Count the lines of a file quickly", "--usage", "python3 line-count.py <file>")
			if (near.code != 0) != tc.embedder {
				t.Errorf("a near-duplicate description should be refused only with an embedder, exit %d: %s", near.code, near.err)
			}

			copied := h.settledRun(sdd, "", "add", "copied", "--from",
				h.file("copy.py", "# line-count\nprint('line-count')\n# Count the lines of a file quickly\n"),
				"--summary", "Something else entirely here", "--usage", "u")
			if copied.code != 1 || !strings.Contains(copied.err, "byte for byte") {
				t.Errorf("a byte-identical file should be refused with or without an embedder, exit %d: %s", copied.code, copied.err)
			}

			if (sdd.Asked() > 0) != tc.embedder {
				t.Errorf("sdd should run only where its embedder applies, ran %d times", sdd.Asked())
			}
		})
	}
}
