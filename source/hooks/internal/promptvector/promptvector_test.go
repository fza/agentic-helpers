package promptvector_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/promptvector"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestASlotHoldsOnePromptPerSessionAndEmbedder(t *testing.T) {
	dir := t.TempDir()

	err := promptvector.Save(context.Background(), dir, "s1", "count the words", "fake/64", []float32{1, 0})
	if err != nil {
		t.Fatalf("saving should succeed: %v", err)
	}

	cases := []struct {
		name        string
		session     string
		prompt      string
		fingerprint string
		hit         bool
	}{
		{"the same prompt and embedder", "s1", "count the words", "fake/64", true},
		{"another prompt", "s1", "count the lines", "fake/64", false},
		{"another embedder", "s1", "count the words", "other/8", false},
		{"another session", "s2", "count the words", "fake/64", false},
		{"a session name leaving the folder", "../s1", "count the words", "fake/64", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, hit := promptvector.Load(context.Background(), dir, tc.session, tc.prompt, tc.fingerprint)
			if hit != tc.hit {
				t.Errorf("a slot should answer only its own session, prompt and embedder")
			}
		})
	}

	promptvector.Remove(context.Background(), dir, "s1")

	_, hit := promptvector.Load(context.Background(), dir, "s1", "count the words", "fake/64")
	if hit {
		t.Error("a removed slot should answer nothing")
	}

	if promptvector.Save(context.Background(), dir, "../escape", "p", "f", []float32{1}) == nil {
		t.Error("a session name leaving the folder should be refused")
	}
}

func TestASaveNeverWritesThroughAPlantedLink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim")
	_ = os.WriteFile(victim, []byte("untouched"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "format.json"), []byte(`{"format":2}`), 0o600)
	_ = os.Symlink(victim, filepath.Join(dir, "sessions.slots"))

	err := promptvector.Save(context.Background(), dir, "s1", "p", "f", []float32{1})
	data, _ := os.ReadFile(victim)

	if err == nil || string(data) != "untouched" {
		t.Error("a save should never write through a planted link")
	}
}

func TestPerSessionFilesOfAnEarlierReleaseAreTakenIn(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "s1.json")
	_ = os.WriteFile(legacy, []byte(test.Fixture(t, "promptvector/legacy-slot.json")), 0o600)

	_, hit := promptvector.Load(context.Background(), dir, "s1", "count the words", "fake/64")
	if !hit {
		t.Error("a vector an earlier release kept should still answer")
	}

	_, err := os.Stat(legacy)
	if !errors.Is(err, os.ErrNotExist) {
		t.Error("the per-session file should be removed once taken in")
	}
}
