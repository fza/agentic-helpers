package memory_test

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
	"github.com/fza/agentic-helpers/source/hooks/internal/vectorseg"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const sizedOptions = "embedding_provider: openai\nembedding_model: m\nmemory_language: en\n"

// limitedHarness is a harness whose embedder sits behind an endpoint
// enforcing limit, reached through embed.New as the real binary reaches it.
func limitedHarness(t *testing.T, limit *test.TokenLimit) (*harness, *test.Endpoint) {
	t.Helper()

	h := newHarness(t)
	test.WriteFile(t, h.config, sizedOptions)

	fake := &test.Endpoint{Answer: limit.Answer}
	h.embedders = func(config embed.Config) (embed.Embedder, error) {
		return embed.New(config, fake.Client(t))
	}

	h.must("", "claim", "main")

	return h, fake
}

// vectorStore is the segments of the store the openai model `m` embeds into.
func (h *harness) vectorStore() *vectorseg.Store {
	h.t.Helper()

	raw, err := embed.New(embed.Config{Provider: "openai", Model: "m"}, nil)
	if err != nil {
		h.t.Fatalf("the embedder should be created: %v", err)
	}

	sum := sha256.Sum256([]byte(embed.Template(raw, embed.Config{}).Fingerprint()))

	return vectorseg.Open(h.roots().Cache, filepath.Join(h.cacheDir(), hex.EncodeToString(sum[:])[:12], "segments"))
}

func (h *harness) storeCap() int {
	h.t.Helper()

	manifest, err := h.vectorStore().Manifest()
	if err != nil {
		h.t.Fatalf("the manifest should read: %v", err)
	}

	return manifest.MaxRunes
}

// fileEntries files the twenty live entries recall speaks from, and extra
// ones as title and body pairs, while no embedder is set: filing embeds
// nothing, and the store stays without vectors or a cap.
func (h *harness) fileEntries(extra ...string) []string {
	h.t.Helper()

	embedders := h.embedders
	h.embedders = func(embed.Config) (embed.Embedder, error) { return nil, embed.ErrNotConfigured }

	defer func() { h.embedders = embedders }()

	var ids []string

	for _, word := range unrelated {
		ids = append(ids, h.file("Finding about "+word, word+" "+word+" "+word))
	}

	for index := 0; index+1 < len(extra); index += 2 {
		ids = append(ids, h.file(extra[index], extra[index+1]))
	}

	return append(ids, h.file("Gate ignores MCP reads always", "only Bash reads count"))
}

// sent is every input the endpoint was sent but the calibration probes.
func sent(limit *test.TokenLimit) []string {
	var texts []string

	for _, text := range limit.Inputs() {
		if !test.IsProbe(text) {
			texts = append(texts, text)
		}
	}

	return texts
}

func TestAPreCapStoreFillsCalibratesAndReEmbedsSilently(t *testing.T) {
	limit := &test.TokenLimit{Limit: 2048}
	h, _ := limitedHarness(t, limit)
	ids := h.fileEntries()

	// Rows an earlier release embedded, hashed without a cap, and no manifest.
	rows := make([]vectorseg.Row, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, vectorseg.Row{ID: id, Hash: "0123456789abcdef", Chunks: [][]float32{{1, 0}}})
	}

	err := h.vectorStore().Write(rows, func(string, string) bool { return true })
	if err != nil {
		t.Fatalf("seeding the pre-cap store should work: %v", err)
	}

	got := h.run(hookPayload("", "", "why does the gate ignore MCP reads"), "prompt")
	if got.code != 0 || got.err != "" || strings.Contains(got.out, "failed") {
		t.Errorf("the upgrade fill should run without a word of its own, exit %d: %q %q", got.code, got.out, got.err)
	}

	if h.storeCap() != 1792 {
		t.Errorf("the fill should record the calibrated cap in the store, got %d", h.storeCap())
	}

	calibration := embed.CalibrationPath(h.roots().Cache, embed.CalibrationKey(h.vectorStoreFingerprint(), "https://api.openai.com", "/v1/embeddings"))

	_, err = os.Stat(calibration)
	if err != nil {
		t.Errorf("the calibration should be stored in the cache under its key: %v", err)
	}

	embedded := strings.Join(sent(limit), "\n")
	for _, word := range unrelated {
		if !strings.Contains(embedded, "Entry: Finding about "+word) {
			t.Errorf("every entry of the pre-cap store should be embedded again, missing %s", word)
		}
	}

	if len(limit.Truncated()) != 0 {
		t.Errorf("no input should reach the endpoint past its limit, %d did", len(limit.Truncated()))
	}
}

func (h *harness) vectorStoreFingerprint() string {
	h.t.Helper()

	raw, err := embed.New(embed.Config{Provider: "openai", Model: "m"}, nil)
	if err != nil {
		h.t.Fatalf("the embedder should be created: %v", err)
	}

	return embed.Template(raw, embed.Config{}).Fingerprint()
}

func TestEveryInputFitsTheEndpointsLimit(t *testing.T) {
	cases := []struct {
		name               string
		refuse, unreported bool
		maxRunes           int
	}{
		{name: "a provider truncating silently", maxRunes: 1792},
		{name: "a provider refusing", refuse: true, maxRunes: 1792},
		{name: "a provider reporting no count", unreported: true, maxRunes: embed.FallbackRunes},
	}

	// 40000 CJK runes count about 40000 tokens, past every limit here, as the
	// 33,356-token prompt rapid-mlx logged.
	longPrompt := strings.Repeat("门禁为何忽略读取", 5000)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			limit := test.TokenLimit{Limit: 2048, Refuse: tc.refuse, Unreported: tc.unreported}
			h, _ := limitedHarness(t, &limit)
			h.fileEntries("Dense notes", strings.Repeat("密集的笔记内容。", 1500))

			got := h.run(hookPayload("", "", longPrompt), "prompt")
			if got.code != 0 || strings.Contains(got.out, "failed") {
				t.Fatalf("recall should answer a long prompt, exit %d: %q", got.code, got.out)
			}

			got = h.run("", "search", "--query", longPrompt)
			if got.code != 0 {
				t.Fatalf("a long search phrase should be answered, exit %d: %s", got.code, got.err)
			}

			if h.storeCap() != tc.maxRunes {
				t.Errorf("the store should record the endpoint's cap, got %d", h.storeCap())
			}

			chunks := 0

			for _, text := range sent(&limit) {
				if utf8.RuneCountInString(text) > tc.maxRunes {
					t.Errorf("every input should be cut or chunked to the cap, one held %d runes", utf8.RuneCountInString(text))
				}

				if strings.HasPrefix(text, "Entry: Dense notes") {
					chunks++
				}
			}

			if chunks < 6 {
				t.Errorf("an entry past the cap should be embedded in chunks, got %d", chunks)
			}

			if len(limit.Truncated()) != 0 {
				t.Errorf("no input should reach the endpoint past its limit, %d did", len(limit.Truncated()))
			}
		})
	}
}

func TestAChangedPathMeasuresAgainAndReEmbedsEveryEntryOnce(t *testing.T) {
	limit := &test.TokenLimit{Limit: 2048, Unreported: true}
	h, fake := limitedHarness(t, limit)
	h.fileEntries()

	h.must("", "search", "--query", "gate reads")

	if h.storeCap() != embed.FallbackRunes {
		t.Fatalf("a provider reporting no count should leave the fallback cap, got %d", h.storeCap())
	}

	// The gateway counts; its route is a new endpoint to measure.
	limit.Unreported = false
	test.WriteFile(t, h.settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"embedding_path":"/gw/embeddings"}}}}`)

	before := len(sent(limit))
	h.must("", "search", "--query", "gate reads")

	if !strings.HasSuffix(fake.Requests[len(fake.Requests)-1].Address, "/gw/embeddings") {
		t.Errorf("the configured path should reach the request, got %s", fake.Requests[len(fake.Requests)-1].Address)
	}

	if h.storeCap() != 1792 {
		t.Errorf("the store should move to the newly measured cap, got %d", h.storeCap())
	}

	reembedded := sent(limit)[before:]
	if len(reembedded) < len(unrelated)+1 {
		t.Errorf("a changed cap should embed every entry again, sent %d texts", len(reembedded))
	}

	before = len(sent(limit))
	h.must("", "search", "--query", "gate reads")

	if len(sent(limit))-before != 1 {
		t.Errorf("a store at its cap should embed only the query, sent %d texts", len(sent(limit))-before)
	}
}

func TestAFailedProbeIsToldOnceThroughTheRecallLine(t *testing.T) {
	h, _ := limitedHarness(t, &test.TokenLimit{Limit: 2048})
	h.embedders = func(config embed.Config) (embed.Embedder, error) {
		down := &test.Endpoint{Answer: func(test.EmbedRequest) (int, string) { return http.StatusServiceUnavailable, "" }}

		return embed.New(config, down.Client(t))
	}

	h.fileEntries()

	got := h.run(hookPayload("", "", "why does the gate ignore MCP reads"), "prompt")
	if !strings.Contains(got.out, "agentic-memory: prompt recall failed, retried each prompt: ") ||
		!strings.Contains(got.out, "calibration latin probe: ") {
		t.Errorf("a failed probe should be told through the recall failure line, got %q", got.out)
	}

	got = h.run(hookPayload("", "", "and again"), "prompt")
	if strings.Contains(got.out, "calibration") {
		t.Errorf("the same outage should be told once, got %q", got.out)
	}

	got = h.run("", "search", "--query", "gate reads")
	if got.code == 0 || !strings.Contains(got.err, "calibration latin probe: ") {
		t.Errorf("a search by meaning should refuse naming the failed probe, exit %d: %s", got.code, got.err)
	}
}

func TestAMalformedCalibrationRefusesASearchNamingTheFile(t *testing.T) {
	limit := &test.TokenLimit{Limit: 2048}
	h, _ := limitedHarness(t, limit)
	h.fileEntries()

	calibration := embed.CalibrationPath(h.roots().Cache, embed.CalibrationKey(h.vectorStoreFingerprint(), "https://api.openai.com", "/v1/embeddings"))
	test.WriteFile(t, calibration, "{not json")

	got := h.run("", "search", "--query", "gate reads")
	if got.code == 0 || !strings.Contains(got.err, "reading calibration "+calibration+": ") {
		t.Errorf("a search should refuse naming the calibration that does not read, exit %d: %s", got.code, got.err)
	}

	if len(limit.Inputs()) != 0 {
		t.Errorf("a calibration that does not read should never be measured over, sent %d inputs", len(limit.Inputs()))
	}
}

func TestAStoreWithoutACapRefusesASearchWhileAnotherFillRuns(t *testing.T) {
	limit := &test.TokenLimit{Limit: 2048}
	h, _ := limitedHarness(t, limit)
	h.fileEntries()

	release, taken, err := h.vectorStore().TryFill()
	if err != nil || !taken {
		t.Fatalf("the test should hold the store's fill: %v", err)
	}

	defer release()

	got := h.run("", "search", "--query", "gate reads")
	if got.code == 0 || !strings.Contains(got.err, "the vector store records no input cap yet") {
		t.Errorf("a search should refuse while the store records no cap, exit %d: %s", got.code, got.err)
	}

	got = h.run(hookPayload("", "", "why does the gate ignore MCP reads"), "prompt")
	if !strings.Contains(got.out, "agentic-memory: prompt recall failed, retried each prompt: ") {
		t.Errorf("recall should tell a store without a cap through its failure line, got %q", got.out)
	}

	if len(sent(limit)) != 0 {
		t.Errorf("a store without a cap should embed no query, sent %d texts", len(sent(limit)))
	}
}
