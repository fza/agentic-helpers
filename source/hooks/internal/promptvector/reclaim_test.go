package promptvector_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/promptvector"
	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
)

func TestAReclaimDropsTheVectorsOfSessionsGoneAWeek(t *testing.T) {
	dir := promptvector.Dir(t.TempDir())

	err := promptvector.Save(context.Background(), dir, "recent", "count the words", "fake/64", []float32{1, 0})
	if err != nil {
		t.Fatalf("saving should succeed: %v", err)
	}

	file := slots.Open(filepath.Dir(dir), filepath.Join(dir, "sessions.slots"))
	file.Now = func() time.Time { return time.Now().Add(-slots.StaleAge - time.Hour) }

	err = file.Put(context.Background(), "crashed", []byte(`{}`))
	if err != nil {
		t.Fatalf("writing a session gone a week: %v", err)
	}

	err = promptvector.Reclaim(context.Background(), dir)
	if err != nil {
		t.Fatalf("reclaiming should succeed: %v", err)
	}

	_, crashed, err := file.Get(context.Background(), "crashed")
	if err != nil {
		t.Fatalf("reading the slot file: %v", err)
	}

	_, recent := promptvector.Load(context.Background(), dir, "recent", "count the words", "fake/64")

	if crashed || !recent {
		t.Error("a reclaim should drop a vector no write touched for a week and keep one written within it")
	}
}
