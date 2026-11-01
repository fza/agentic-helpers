package collapse_test

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/collapse"
	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
)

func TestParallelCallsPrintAnEntryOnce(t *testing.T) {
	const session = "11111111-2222-3333-4444-555555555555"

	const calls = 16

	root := t.TempDir()
	store := collapse.Store{Root: root, Dir: filepath.Join(root, "ledger")}
	store.Switch(session, true)

	payload := hookio.Payload{
		SessionID:    session,
		ToolName:     collapse.Tool + "search",
		ToolResponse: fixture(t, "search-more.json"),
	}

	var (
		running   sync.WaitGroup
		collapsed atomic.Int32
	)

	start := make(chan struct{})

	for range calls {
		running.Go(func() {
			<-start

			_, shortened := store.Collapse(context.Background(), payload)
			if shortened {
				collapsed.Add(1)
			}
		})
	}

	close(start)
	running.Wait()

	if collapsed.Load() != calls-1 {
		t.Errorf("only the first of a batch should print in full, got %d of %d collapsed", collapsed.Load(), calls)
	}
}
