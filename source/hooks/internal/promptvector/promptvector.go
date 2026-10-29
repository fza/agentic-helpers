// Package promptvector keeps the vector of a session's latest prompt, one slot
// per session of `<cache>/prompt-vectors/sessions.slots` (internal/slots), so agentic-memory and agentic-tools embed a prompt once between
// them where one finishes before the other asks. The hooks of one event run in
// parallel, so a miss is the common case and costs one embed. Both scan a
// prompt for credentials here before embedding it.
package promptvector

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/secrets"
	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
)

const (
	slotsName = "sessions.slots"
	// slotWidth holds a vector of several thousand floats as JSON.
	slotWidth = 128 << 10
	// format is the prompt vectors' layout: one slot per session; 1 was a
	// JSON file per session.
	format = 2
)

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
func Load(ctx context.Context, dir string, session string, prompt string, fingerprint string) ([]float32, bool) {
	if !sessionShape.MatchString(session) {
		return nil, false
	}

	file, err := open(ctx, dir)
	if err != nil {
		return nil, false
	}

	var held slot
	if !file.GetJSON(ctx, session, &held) || held.Prompt != hash(prompt) || held.Fingerprint != fingerprint ||
		len(held.Vector) == 0 {
		return nil, false
	}

	return held.Vector, true
}

// Save fills a session's slot, replacing what it held.
func Save(ctx context.Context, dir string, session string, prompt string, fingerprint string, vector []float32) error {
	if !sessionShape.MatchString(session) {
		return fmt.Errorf("session %q names no slot", session)
	}

	file, err := open(ctx, dir)
	if err != nil {
		return err
	}

	err = file.PutJSON(ctx, session, slot{Prompt: hash(prompt), Fingerprint: fingerprint, Vector: vector})
	if err != nil {
		return fmt.Errorf("writing the prompt vector: %w", err)
	}

	return nil
}

// Remove empties a session's slot.
func Remove(ctx context.Context, dir string, session string) {
	if !sessionShape.MatchString(session) {
		return
	}

	file, err := open(ctx, dir)
	if err == nil {
		_ = file.Delete(ctx, session)
	}
}

// Adopt takes the per-session files an earlier release kept in dir into the
// slot file, for a machine where no hook has read a prompt vector since.
func Adopt(ctx context.Context, dir string) error {
	_, err := open(ctx, dir)

	return err
}

// open is the slot file of dir, once the per-session files an earlier
// release kept there are taken in.
func open(ctx context.Context, dir string) (*slots.File, error) {
	file := slots.Open(filepath.Join(dir, slotsName), slotWidth)

	err := file.Adopt(ctx, dir, format, func() []slots.Legacy {
		paths, _ := filepath.Glob(filepath.Join(dir, "*.json"))

		var legacy []slots.Legacy

		for _, path := range paths {
			session := strings.TrimSuffix(filepath.Base(path), ".json")
			if sessionShape.MatchString(session) {
				legacy = append(legacy, slots.Legacy{Path: path, Key: session})
			}
		}

		return legacy
	})
	if err != nil {
		return nil, fmt.Errorf("opening the prompt vectors: %w", err)
	}

	return file, nil
}
