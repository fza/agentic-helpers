package memory_test

import (
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/vectorseg"
)

// storeDir is the fake embedder's vector store: the first 12 hex digits of its
// fingerprint's sha256.
func (h *harness) storeDir() string {
	sum := sha256.Sum256([]byte(h.embedder.Space()))

	return filepath.Join(h.cacheDir(), hex.EncodeToString(sum[:])[:12])
}

func TestAWholeFileVectorStoreGoesAndItsEntriesAreEmbeddedAgain(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	h.embedding = false
	title, body := "Seal writes the manifest atomically", "rename then fsync of the directory"
	id := h.file(title, body)
	h.embedding = true

	sum := sha256.Sum256([]byte(title + "\n" + body))
	vector := make([]float32, 64)
	vector[0] = 1

	type legacyRow struct {
		Hash   string
		Chunks [][]float32
	}

	legacy := filepath.Join(h.storeDir(), "vectors.gob")

	err := os.MkdirAll(h.storeDir(), 0o700)
	if err != nil {
		t.Fatalf("creating the store: %v", err)
	}

	file, err := os.Create(legacy)
	if err != nil {
		t.Fatalf("planting vectors.gob: %v", err)
	}

	err = gob.NewEncoder(file).Encode(map[string]legacyRow{id: {Hash: hex.EncodeToString(sum[:])[:16], Chunks: [][]float32{vector}}})
	_ = file.Close()

	if err != nil {
		t.Fatalf("encoding vectors.gob: %v", err)
	}

	h.must("", "search", "--query", "manifest rename fsync")

	// Its rows were embedded uncut, so none counts under a cap.
	embedded := false

	for _, text := range h.embedder.Texts() {
		embedded = embedded || strings.Contains(text, title)
	}

	if !embedded {
		t.Error("an entry vectors.gob held should be embedded again, cut to the cap")
	}

	if _, err := os.Stat(legacy); err == nil {
		t.Error("vectors.gob should be removed")
	}
}

func TestAChangedEntryIsTheOnlyOneEmbeddedAgain(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	changed := h.file("Gate reads Bash only today", "the hook matches the Bash tool")
	h.must("", "search", "--query", "gate")
	h.file("Seal writes the manifest atomically", "rename then fsync of the directory")
	h.file("Brief orders entries by heat", "the hottest entry comes first")
	h.must("", "search", "--query", "manifest")

	before := len(h.embedder.Texts())

	h.must("", "retitle", changed, "Gate reads Bash and MCP calls")
	h.must("", "search", "--query", "gate")

	var embedded []string

	for _, text := range h.embedder.Texts()[before:] {
		if strings.HasPrefix(text, "Entry: ") {
			embedded = append(embedded, text)
		}
	}

	for _, text := range embedded {
		if !strings.Contains(text, "Gate reads Bash and MCP calls") {
			t.Errorf("only the changed entry's chunks should be embedded again, got %q", text)
		}
	}

	if len(embedded) == 0 {
		t.Error("the changed entry should be embedded again")
	}

	stale := sha256.Sum256([]byte("Gate reads Bash only today\nthe hook matches the Bash tool"))

	snapshot, err := vectorseg.Open(h.storeDir(), filepath.Join(h.storeDir(), "segments")).Snapshot()
	if err != nil {
		t.Fatalf("reading the segments: %v", err)
	}
	defer snapshot.Close()

	if snapshot.Holds(changed, hex.EncodeToString(stale[:])[:16]) {
		t.Error("a merge should drop the segment whose only row was embedded from the entry's old content")
	}
}
