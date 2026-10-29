// Package promptvector keeps the vector of a session's latest prompt, one slot
// per session, so agentic-memory and agentic-tools embed a prompt once between
// them where one finishes before the other asks. The hooks of one event run in
// parallel, so a miss is the common case and costs one embed. Both scan a
// prompt for credentials here before embedding it.
package promptvector

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/fza/agentic-helpers/source/hooks/internal/secrets"
)

// slotLimit caps a slot read: a vector of a few thousand floats is far below.
const slotLimit = 1 << 20

var sessionShape = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type slot struct {
	Prompt      string    `json:"prompt"`
	Fingerprint string    `json:"fingerprint"`
	Vector      []float32 `json:"vector"`
}

// Withheld is the notice for a prompt that looks like it holds a credential,
// or nothing for a clean one. Neither prompt hook embeds such a prompt, since
// the embedding endpoint may be remote; the prompt itself is the user's and is
// never refused. agentic-memory alone prints the notice, so the agent hears it
// once.
func Withheld(ctx context.Context, prompt string) string {
	found := secrets.Scan(ctx, prompt, "")
	if len(found) == 0 {
		return ""
	}

	rules, _ := secrets.Describe(found)

	return fmt.Sprintf("agentic-memory: this prompt looks like it holds a credential (%s); it was not sent to the embedding endpoint", rules)
}

// Dir is where prompt vectors live below the home's cache root
// (internal/home), shared by every store.
func Dir(cache string) string {
	return filepath.Join(cache, "prompt-vectors")
}

func hash(prompt string) string {
	sum := sha256.Sum256([]byte(prompt))

	return hex.EncodeToString(sum[:])
}

// Load is the vector a session's slot holds for prompt under the embedder
// fingerprint, if it holds one.
func Load(dir string, session string, prompt string, fingerprint string) ([]float32, bool) {
	if !sessionShape.MatchString(session) {
		return nil, false
	}

	file, err := os.Open(filepath.Join(dir, session+".json"))
	if err != nil {
		return nil, false
	}

	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > slotLimit {
		return nil, false
	}

	var held slot

	err = json.NewDecoder(file).Decode(&held)
	if err != nil || held.Prompt != hash(prompt) || held.Fingerprint != fingerprint || len(held.Vector) == 0 {
		return nil, false
	}

	return held.Vector, true
}

// Save fills a session's slot, replacing what it held.
func Save(dir string, session string, prompt string, fingerprint string, vector []float32) error {
	err := os.MkdirAll(dir, 0o700)
	if err != nil {
		return fmt.Errorf("creating the prompt vector cache: %w", err)
	}

	data, err := json.Marshal(slot{Prompt: hash(prompt), Fingerprint: fingerprint, Vector: vector})
	if err != nil {
		return fmt.Errorf("encoding the prompt vector: %w", err)
	}

	// Written relative to the folder's descriptor, through a fresh name: a
	// fixed one could be a planted link the write would follow.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("opening the prompt vector cache: %w", err)
	}

	defer func() { _ = root.Close() }()

	random := make([]byte, 8)

	_, err = rand.Read(random)
	if err != nil {
		return fmt.Errorf("naming the prompt vector: %w", err)
	}

	temporary := "." + session + "-" + hex.EncodeToString(random)

	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("writing the prompt vector: %w", err)
	}

	_, err = file.Write(data)
	closeErr := file.Close()

	if err == nil {
		err = closeErr
	}

	if err == nil {
		err = root.Rename(temporary, session+".json")
	}

	if err != nil {
		_ = root.Remove(temporary)

		return fmt.Errorf("writing the prompt vector: %w", err)
	}

	return nil
}

// Remove empties a session's slot.
func Remove(dir string, session string) {
	if sessionShape.MatchString(session) {
		_ = os.Remove(filepath.Join(dir, session+".json"))
	}
}
