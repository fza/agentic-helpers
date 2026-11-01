package test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
)

// Format1Store lays a memory store of the first format into store: a segment
// and a tail naming seats, the tail times in Unix nanoseconds, a manifest at
// format 1, and claim, last-claim and slice files named for their seats.
func Format1Store(t *testing.T, store string) {
	t.Helper()

	sealed := []memorylog.Event{{
		Kind: memorylog.KindNew, Entry: "m-cccccc", Time: time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC), Seat: "drive",
		Payload: []byte(`{"title":"An older drive finding sealed","body":"sealed in a segment","topics":["release"],"retention":"normal","from":"agent"}`),
	}}

	err := memorylog.Build(context.Background(), store, filepath.Join(store, "log"), sealed)
	if err != nil {
		t.Fatalf("building the segment: %v", err)
	}

	manifestPath := filepath.Join(store, "log", "manifest.json")

	var held map[string]any

	raw, err := os.ReadFile(manifestPath)
	if err == nil {
		err = json.Unmarshal(raw, &held)
	}

	if err != nil {
		t.Fatalf("reading the built manifest: %v", err)
	}

	held["format"] = 1
	raw, _ = json.Marshal(held)
	WriteFile(t, manifestPath, string(raw))

	FixtureTree(t, "memory/format-1", store)
}
