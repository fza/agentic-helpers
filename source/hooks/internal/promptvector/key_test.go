package promptvector_test

import (
	"context"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/promptvector"
)

func TestAPromptCutToAnotherCapIsNoHit(t *testing.T) {
	dir := t.TempDir()

	err := promptvector.Save(context.Background(), dir, "s1", "count the words", promptvector.Key("fake/64", 1792), []float32{1, 0})
	if err != nil {
		t.Fatalf("saving should succeed: %v", err)
	}

	_, hit := promptvector.Load(context.Background(), dir, "s1", "count the words", promptvector.Key("fake/64", 2048))
	if hit {
		t.Error("a vector of the prompt cut to another cap should not be reused")
	}

	_, hit = promptvector.Load(context.Background(), dir, "s1", "count the words", promptvector.Key("fake/64", 1792))
	if !hit {
		t.Error("a vector of the prompt cut to the same cap should be reused")
	}
}
