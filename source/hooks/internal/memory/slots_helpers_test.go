package memory_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
)

func slotFileIn(cacheDir string) *slots.File {
	return slots.Open(filepath.Join(cacheDir, "sessions.slots"), 128<<10)
}

// slots is every key of the store's slot file under prefix with its value
// and generation.
func (h *harness) slots(prefix string) map[string]string {
	h.t.Helper()

	held, err := slotFileIn(h.cacheDir()).All(context.Background())
	if err != nil {
		h.t.Fatalf("reading the slot file: %v", err)
	}

	values := map[string]string{}

	for _, slot := range held {
		if strings.HasPrefix(slot.Key, prefix) {
			values[slot.Key] = string(slot.Value)
		}
	}

	return values
}

// slot is key's value in the store's slot file, empty where it holds none.
func (h *harness) slot(key string) string {
	h.t.Helper()

	value, _, err := slotFileIn(h.cacheDir()).Get(context.Background(), key)
	if err != nil {
		h.t.Fatalf("reading %s: %v", key, err)
	}

	return string(value)
}

func (h *harness) putSlot(key string, value string) {
	h.t.Helper()

	err := slotFileIn(h.cacheDir()).Put(context.Background(), key, []byte(value))
	if err != nil {
		h.t.Fatalf("writing %s: %v", key, err)
	}
}

// ageSlots writes every key under prefix in the slot file of cacheDir again,
// as written at at.
func ageSlots(t *testing.T, cacheDir string, prefix string, at time.Time) {
	t.Helper()

	file := slotFileIn(cacheDir)
	file.Now = func() time.Time { return at }

	held, err := file.All(context.Background())
	if err != nil {
		t.Fatalf("reading the slot file: %v", err)
	}

	for _, slot := range held {
		if strings.HasPrefix(slot.Key, prefix) {
			err = file.Put(context.Background(), slot.Key, slot.Value)
			if err != nil {
				t.Fatalf("aging %s: %v", slot.Key, err)
			}
		}
	}
}

// generations is the generation of every key under prefix: a key written
// again, even with the same value, moves on.
func (h *harness) generations(prefix string) map[string]uint64 {
	h.t.Helper()

	held, err := slotFileIn(h.cacheDir()).All(context.Background())
	if err != nil {
		h.t.Fatalf("reading the slot file: %v", err)
	}

	found := map[string]uint64{}

	for _, slot := range held {
		if strings.HasPrefix(slot.Key, prefix) {
			found[slot.Key] = slot.Generation
		}
	}

	return found
}
