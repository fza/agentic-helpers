package tools_test

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/vectorseg"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

// limitedHarness is a harness whose embedder sits behind an endpoint
// enforcing limit, reached through embed.New as the real binary reaches it.
func limitedHarness(t *testing.T, limit *test.TokenLimit) *harness {
	t.Helper()

	h := newHarness(t)
	test.WriteFile(t, h.config, "embedding_provider: openai\nembedding_model: m\n")

	fake := &test.Endpoint{Answer: limit.Answer}
	h.embedders = func(config embed.Config) (embed.Embedder, error) {
		return embed.New(config, fake.Client(t))
	}

	return h
}

// unembedded adds a tool while no embedder is set, so it lands without a
// vector and without a calibration.
func (h *harness) unembedded(name string, summary string) {
	h.t.Helper()

	embedders := h.embedders
	h.embedders = func(embed.Config) (embed.Embedder, error) { return nil, embed.ErrNotConfigured }
	h.tool(name, summary)
	h.embedders = embedders
}

func (h *harness) limitedFingerprint() string {
	h.t.Helper()

	raw, err := embed.New(embed.Config{Provider: "openai", Model: "m"}, nil)
	if err != nil {
		h.t.Fatalf("the embedder should be created: %v", err)
	}

	return embed.Template(raw, embed.Config{}).Fingerprint()
}

// limitedStore is the vector store the openai model `m` embeds tools into.
func (h *harness) limitedStore() *vectorseg.Store {
	digest := sha256.Sum256([]byte(h.limitedFingerprint()))
	dir := filepath.Join(h.roots().SharedCache(home.Tools), "vectors", hex.EncodeToString(digest[:])[:32])

	return vectorseg.Open(h.roots().Cache, dir)
}

func (h *harness) limitedCap() int {
	h.t.Helper()

	manifest, err := h.limitedStore().Manifest()
	if err != nil {
		h.t.Fatalf("the manifest should read: %v", err)
	}

	return manifest.MaxRunes
}

func (h *harness) limitedHolds(described string, maxRunes int) bool {
	h.t.Helper()

	snapshot, err := h.limitedStore().Snapshot()
	if err != nil {
		h.t.Fatalf("taking a snapshot: %v", err)
	}

	defer func() { _ = snapshot.Close() }()

	return snapshot.Holds(described, embed.RowHash(described, maxRunes))
}

func sentTexts(limit *test.TokenLimit) []string {
	var texts []string

	for _, text := range limit.Inputs() {
		if !test.IsProbe(text) {
			texts = append(texts, text)
		}
	}

	return texts
}

func TestAToolTextPastTheCapIsEmbeddedInPieces(t *testing.T) {
	limit := &test.TokenLimit{Limit: 2048}
	h := limitedHarness(t, limit)

	h.tool("dense-notes", "Summarise dense notes", "--outputs", strings.Repeat("密集的笔记内容。", 600))

	if h.limitedCap() != 1792 {
		t.Errorf("the first write should record the calibrated cap, got %d", h.limitedCap())
	}

	pieces := 0

	for _, text := range sentTexts(limit) {
		if utf8.RuneCountInString(text) > 1792 {
			t.Errorf("every piece of a tool text should fit the cap, one held %d runes", utf8.RuneCountInString(text))
		}

		pieces++
	}

	if pieces < 3 || len(limit.Truncated()) != 0 {
		t.Errorf("a tool text past the limit should go out in pieces none of which is cut, %d pieces, %d cut", pieces, len(limit.Truncated()))
	}

	if !h.limitedHolds(h.described("dense-notes"), 1792) {
		t.Error("the tool's row should be filed under its hash at the cap")
	}
}

func TestALongFindQueryIsCutToTheCap(t *testing.T) {
	limit := &test.TokenLimit{Limit: 2048}
	h := limitedHarness(t, limit)
	h.tool("word-count", "Count the words of standard input")

	before := len(sentTexts(limit))
	h.must("find", "--query", strings.Repeat("统计标准输入的单词", 5000))

	queries := sentTexts(limit)[before:]
	if len(queries) != 1 || utf8.RuneCountInString(queries[0]) > 1792 || len(limit.Truncated()) != 0 {
		t.Errorf("a find query past the limit should be cut to the cap, sent %d texts, %d cut", len(queries), len(limit.Truncated()))
	}
}

func TestAPreCapToolStoreFillsOnFindSilently(t *testing.T) {
	limit := &test.TokenLimit{Limit: 2048}
	h := limitedHarness(t, limit)
	h.unembedded("word-count", "Count the words of standard input")

	described := h.described("word-count")

	// What an earlier release left: rows hashed by the description alone, no
	// manifest.
	store := h.limitedStore()

	err := store.Write([]vectorseg.Row{{ID: described, Hash: described, Chunks: [][]float32{{1, 0}}}}, func(string, string) bool { return true })
	if err != nil {
		t.Fatalf("seeding the pre-cap store should work: %v", err)
	}

	before := len(sentTexts(limit))

	got := h.run("", "find", "--query", "count words")
	if got.code != 0 || got.err != "" || !strings.Contains(got.out, "word-count") {
		t.Errorf("a find over a pre-cap store should fill it and answer without a word of its own, exit %d: %s%s", got.code, got.out, got.err)
	}

	if len(sentTexts(limit))-before != 2 || h.limitedCap() != 1792 || !h.limitedHolds(described, 1792) {
		t.Errorf("the fill should embed the description again at the recorded cap, sent %d texts", len(sentTexts(limit))-before)
	}
}

func TestLoudFailuresRefuseAFindAndAReindex(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(h *harness)
		says    string
	}{
		{
			name: "a failed probe",
			prepare: func(h *harness) {
				h.embedders = func(config embed.Config) (embed.Embedder, error) {
					down := &test.Endpoint{Answer: func(test.EmbedRequest) (int, string) { return http.StatusServiceUnavailable, "" }}

					return embed.New(config, down.Client(h.t))
				}
			},
			says: "calibration latin probe: ",
		},
		{
			name: "a calibration that does not read",
			prepare: func(h *harness) {
				test.WriteFile(h.t, embed.CalibrationPath(h.roots().Cache,
					embed.CalibrationKey(h.limitedFingerprint(), "https://api.openai.com", "/v1/embeddings")), "{")
			},
			says: "reading calibration ",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := limitedHarness(t, &test.TokenLimit{Limit: 2048})
			h.unembedded("word-count", "Count the words of standard input")

			tc.prepare(h)

			h.refused(tc.says, "find", "--query", "count words")
			h.refused(tc.says, "reindex")
		})
	}
}

func TestAFindRefusesWhileTheFirstFillRunsElsewhere(t *testing.T) {
	limit := &test.TokenLimit{Limit: 2048}
	h := limitedHarness(t, limit)
	h.unembedded("word-count", "Count the words of standard input")

	release, taken, err := h.limitedStore().TryFill()
	if err != nil || !taken {
		t.Fatalf("the test should hold the store's fill: %v", err)
	}

	defer release()

	h.refused("the tool vectors record no input cap yet", "find", "--query", "count words")
}
