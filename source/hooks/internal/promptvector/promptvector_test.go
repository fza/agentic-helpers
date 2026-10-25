package promptvector_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/promptvector"
)

func TestASlotHoldsOnePromptPerSessionAndEmbedder(t *testing.T) {
	dir := t.TempDir()

	err := promptvector.Save(dir, "s1", "count the words", "fake/64", []float32{1, 0})
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
			_, hit := promptvector.Load(dir, tc.session, tc.prompt, tc.fingerprint)
			if hit != tc.hit {
				t.Errorf("a slot should answer only its own session, prompt and embedder")
			}
		})
	}

	promptvector.Remove(dir, "s1")

	_, hit := promptvector.Load(dir, "s1", "count the words", "fake/64")
	if hit {
		t.Error("a removed slot should answer nothing")
	}

	victim := filepath.Join(dir, "victim.json")
	_ = os.WriteFile(victim, []byte("untouched"), 0o600)
	_ = os.Symlink("victim.json", filepath.Join(dir, ".s1.tmp"))

	err = promptvector.Save(dir, "s1", "p", "f", []float32{1})
	data, _ := os.ReadFile(victim)

	if err != nil || string(data) != "untouched" {
		t.Error("a save should never write through a planted link")
	}

	if promptvector.Save(dir, "../escape", "p", "f", []float32{1}) == nil {
		t.Error("a session name leaving the folder should be refused")
	}

	err = promptvector.Save(dir, "s3", "outside", "f", []float32{1})
	if err != nil {
		t.Fatalf("saving should succeed: %v", err)
	}

	outside := filepath.Join(dir, "..", "s3.json")
	_ = os.Rename(filepath.Join(dir, "s3.json"), outside)

	_, hit = promptvector.Load(dir, "../s3", "outside", "f")
	if hit {
		t.Error("a session name leaving the folder should never read a slot outside it")
	}

	promptvector.Remove(dir, "../s3")

	_, err = os.Stat(outside)
	if err != nil {
		t.Error("a session name leaving the folder should never remove a file outside it")
	}
}
