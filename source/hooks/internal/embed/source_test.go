package embed_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
)

// sddAnswer is an sdd that prints a fixed embedding block, or none.
type sddAnswer struct {
	text  string
	asked int
}

func (sdd *sddAnswer) EmbeddingConfig(_ context.Context, _ string) string {
	sdd.asked++

	return sdd.text
}

const sddBlock = `embedding.provider           openai                               (global)
embedding.model              embeddinggemma-300m-6bit             (global)
embedding.endpoint           http://127.0.0.1:1235                (global)
embedding.api_keys.openai    ••••••                               (global)
embedding.timeout            1m30s                                (local)
embedding.query_template     task: search result | query: {text}  (global)
embedding.document_template  title: none | text: {text}           (global)
`

func TestSettleTakesSddsWholeBlockWithoutAKey(t *testing.T) {
	sdd := &sddAnswer{text: sddBlock}
	own := embed.Config{Provider: "ollama", Model: "own", APIKey: "own-key", Timeout: 2 * time.Minute}

	settled, _ := embed.Settle(context.Background(), own, true, sdd, "/project")

	want := embed.Config{
		Provider: "openai", Model: "embeddinggemma-300m-6bit", Endpoint: "http://127.0.0.1:1235", Timeout: 90 * time.Second,
		QueryTemplate: "task: search result | query: {text}", DocumentTemplate: "title: none | text: {text}",
	}
	if settled != want {
		t.Errorf("sdd's block should apply whole, with no key and no own value, got %+v", settled)
	}
}

func TestSettleKeepsOwnKeysAndAsksSddNothing(t *testing.T) {
	sdd := &sddAnswer{text: sddBlock}
	own := embed.Config{Provider: "ollama", Model: "own", APIKey: "own-key"}

	settled, _ := embed.Settle(context.Background(), own, false, sdd, "/project")
	if settled != own || sdd.asked != 0 {
		t.Errorf("own keys should apply unchanged without running sdd, got %+v after %d calls", settled, sdd.asked)
	}
}

func TestSettleFindsNoEmbedderWhereSddSetsNone(t *testing.T) {
	cases := []struct {
		name string
		sdd  embed.Sdd
	}{
		{name: "no sdd", sdd: nil},
		{name: "sdd answers nothing", sdd: &sddAnswer{}},
		{name: "a provider agentic-helpers cannot use", sdd: &sddAnswer{text: "embedding.provider  voyage  (global)\nembedding.model  v3  (global)\n"}},
		{name: "an endpoint the repository sets", sdd: &sddAnswer{text: strings.Replace(sddBlock, "1235                (global)", "1235                (project)", 1)}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			settled, _ := embed.Settle(context.Background(), embed.Config{Timeout: time.Minute}, true, tc.sdd, "/project")
			if settled.Provider != "" {
				t.Errorf("no usable block should leave no embedder, got %+v", settled)
			}
		})
	}
}

func TestSettleKeepsTheOwnTimeoutWhereSddSetsNone(t *testing.T) {
	sdd := &sddAnswer{text: "embedding.provider  ollama  (global)\nembedding.model  nomic  (global)\n"}

	settled, _ := embed.Settle(context.Background(), embed.Config{Timeout: 45 * time.Second}, true, sdd, "/project")
	if settled.Timeout != 45*time.Second {
		t.Errorf("a block without a timeout should keep the configured one, got %v", settled.Timeout)
	}
}

func TestSettleTakesTheEndpointOnlyFromALayerOutsideTheRepository(t *testing.T) {
	const block = "embedding.provider  ollama  (global)\nembedding.model  nomic  (project)\n"

	cases := []struct {
		layer   string
		refused bool
	}{
		{layer: "global"},
		{layer: "default"},
		{layer: "project", refused: true},
		{layer: "local", refused: true},
		{layer: "graph", refused: true},
	}

	for _, tc := range cases {
		t.Run(tc.layer, func(t *testing.T) {
			sdd := &sddAnswer{text: block + "embedding.endpoint  http://example.org:9  (" + tc.layer + ")\n"}

			settled, err := embed.Settle(context.Background(), embed.Config{}, true, sdd, "/project")
			if errors.Is(err, embed.ErrRepositoryEndpoint) != tc.refused || (settled.Provider == "") != tc.refused {
				t.Errorf("an endpoint should apply only from a layer outside the repository, got %+v, %v", settled, err)
			}
		})
	}
}

func TestSettleTakesNoBlockPrintingAKeyTwice(t *testing.T) {
	sdd := &sddAnswer{text: sddBlock + "embedding.endpoint  http://example.org:9  (global)\n"}

	settled, _ := embed.Settle(context.Background(), embed.Config{}, true, sdd, "/project")
	if settled.Provider != "" {
		t.Errorf("a block naming a key twice should leave no embedder, got %+v", settled)
	}
}

func TestSettleTakesNoBlockHoldingALineOfAnotherShape(t *testing.T) {
	sdd := &sddAnswer{text: sddBlock + "embedding.endpoint\n"}

	settled, _ := embed.Settle(context.Background(), embed.Config{}, true, sdd, "/project")
	if settled.Provider != "" {
		t.Errorf("a block holding a line sdd never prints should leave no embedder, got %+v", settled)
	}
}

func TestAValueCarryingALayerMarkKeepsTheLineItsOwn(t *testing.T) {
	sdd := &sddAnswer{text: "embedding.provider  ollama  (global)\n" +
		"embedding.endpoint  http://example.org:9  (global)  (project)\n"}

	_, err := embed.Settle(context.Background(), embed.Config{}, true, sdd, "/project")
	if !errors.Is(err, embed.ErrRepositoryEndpoint) {
		t.Errorf("only the last parenthesis should name the layer, got %v", err)
	}
}
