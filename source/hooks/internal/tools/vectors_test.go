package tools_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/vectorseg"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

// vectorDir is where the fake embedder's vectors live, named by its
// fingerprint's hash.
func (h *harness) vectorDir() string {
	digest := sha256.Sum256([]byte(h.embedder.Fingerprint()))

	return filepath.Join(h.cache, "agentic-tools", "vectors", hex.EncodeToString(digest[:])[:32])
}

func (h *harness) segments() map[string][]byte {
	h.t.Helper()

	listed, err := os.ReadDir(h.vectorDir())
	if err != nil {
		h.t.Fatalf("listing the vector segments: %v", err)
	}

	held := map[string][]byte{}

	for _, entry := range listed {
		if !strings.HasSuffix(entry.Name(), ".seg") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(h.vectorDir(), entry.Name()))
		if err != nil {
			h.t.Fatalf("reading a vector segment: %v", err)
		}

		held[entry.Name()] = data
	}

	return held
}

func (h *harness) described(name string) string {
	h.t.Helper()

	row, _ := h.indexed()[name].(map[string]any)
	described, _ := row["described"].(string)

	return described
}

func (h *harness) holds(described string) bool {
	h.t.Helper()

	snapshot, err := vectorseg.Open(h.vectorDir()).Snapshot()
	if err != nil {
		h.t.Fatalf("taking a snapshot: %v", err)
	}

	defer func() { _ = snapshot.Close() }()

	return snapshot.Holds(described, described)
}

func TestAToolAddedFilesItsVectorWithoutRewritingAnother(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")

	before := h.segments()

	h.tool("line-count", "Count the lines of a file")

	after := h.segments()
	for name, data := range before {
		if !bytes.Equal(after[name], data) {
			t.Error("adding a tool should leave every segment already written as it was")
		}
	}

	if len(after) != len(before)+1 || !h.holds(h.described("line-count")) {
		t.Error("adding a tool should file its vector as one new segment")
	}

	index := h.read("index.json")
	if strings.Contains(index, `"vector"`) || strings.Contains(index, `"fingerprint"`) || !strings.Contains(index, `"schema":2`) {
		t.Error("index.json should carry no vector and no fingerprint, under schema 2")
	}
}

func TestAWholeFileVectorCacheMigratesWithoutEmbedding(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")

	described := h.described("word-count")
	vector := make([]float32, 64)
	vector[0] = 1

	data, err := json.Marshal(map[string][]float32{described: vector})
	if err != nil {
		t.Fatalf("encoding the legacy cache: %v", err)
	}

	for name := range h.segments() {
		err = os.Remove(filepath.Join(h.vectorDir(), name))
		if err != nil {
			t.Fatalf("clearing the segments: %v", err)
		}
	}

	legacy := h.vectorDir() + ".json"
	test.WriteFile(t, legacy, string(data))
	test.WriteFile(t, filepath.Join(h.store(), "index.json"), `{"schema":1,"tools":{}}`)

	embedded := h.embedder.Calls()

	h.must("find", "--term", "words")

	if h.embedder.Calls() != embedded {
		t.Error("a rebuild over a migrated cache should embed nothing")
	}

	if test.Exists(t, legacy) || !h.holds(described) {
		t.Error("the whole-file cache should become a segment and go")
	}
}

func TestAVectorNoStoreNamesMergesAway(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")

	stale := h.described("word-count")

	h.must("update", "word-count", "--summary", "Count the words of standard input quickly", "--why", "faster")

	if h.holds(stale) || !h.holds(h.described("word-count")) {
		t.Error("a description no store names any more should merge away, its successor kept")
	}
}

func TestAVectorAnotherStoreNamesStays(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")

	other := h.elsewhere("github.com/acme/other")
	other.tool("line-count", "Count the lines of a file")

	kept := other.described("line-count")

	// The fourth segment of a size class folds all four into one, the other
	// store's row among them.
	h.variables["AGENTIC_TOOLS_SCOPE"] = "project"
	h.tool("word-split", "Put each word on its own line")
	h.tool("char-count", "Count the characters of a file")

	if len(h.segments()) != 1 || !h.holds(kept) {
		t.Error("a merge should keep the vector of a description another store names, whatever the read scope")
	}
}

func TestTheOfferEmbedsNoPromptWhereNoOwnToolHasAVector(t *testing.T) {
	h := newHarness(t)

	h.embedding = false
	h.tool("word-split", "Put each word on its own line")
	h.embedding = true

	embedded := h.embedder.Calls()

	if h.hook("prompt", map[string]any{"prompt": "put each word on its own line"}) != "" || h.embedder.Calls() != embedded {
		t.Error("the offer should neither embed the prompt nor name a tool where no own tool has a vector")
	}
}
