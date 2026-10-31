package memorylog_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
)

// keepAll passes every event through a rewrite unchanged.
func keepAll(rows []memorylog.Row, payload func(int) ([]byte, error)) ([]memorylog.Event, error) {
	kept := make([]memorylog.Event, 0, len(rows))

	for index, row := range rows {
		body, err := payload(index)
		if err != nil {
			return nil, err
		}

		kept = append(kept, memorylog.Event{Kind: row.Kind, Entry: row.Entry, Time: row.Time, Seat: row.Seat, Payload: body})
	}

	return kept, nil
}

func TestARewriteRunsItsFirstStepHoldingTheLogLock(t *testing.T) {
	dir := t.TempDir()
	log := openLog(t, dir)
	appendEvents(t, log, events(0, 2, 0)...)

	tail := filepath.Join(dir, memorylog.TailName)
	appended := make(chan error, 1)

	var tailWhileFirst []byte

	first := func() error {
		var err error

		tailWhileFirst, err = os.ReadFile(tail)
		if err != nil {
			return err
		}

		go func() {
			other, err := memorylog.Open(dir, dir)
			if err == nil {
				err = other.Append(context.Background(), event(2, 0))
			}

			appended <- err
		}()

		// An append not held off by the lock lands well inside this wait.
		time.Sleep(200 * time.Millisecond)

		during, err := os.ReadFile(tail)
		if err != nil {
			return err
		}

		if string(during) != string(tailWhileFirst) {
			return errors.New("an append landed while the first step ran")
		}

		return nil
	}

	err := log.RewriteAfter(context.Background(), first, keepAll)
	if err != nil {
		t.Fatalf("the first step should run with every other writer held off: %v", err)
	}

	if len(tailWhileFirst) == 0 {
		t.Error("the first step should see the log before the rewrite seals its tail")
	}

	err = <-appended
	if err != nil {
		t.Fatalf("the held-off append should land after the rewrite: %v", err)
	}

	if read := readEvents(t, log); len(read) != 3 {
		t.Errorf("every event should be kept, the held-off append's after the rewrite, got %d", len(read))
	}
}

func TestAFailingFirstStepLeavesTheLogAsItWas(t *testing.T) {
	dir := t.TempDir()
	log := openLog(t, dir)
	appendEvents(t, log, events(0, 2, 0)...)

	tail := filepath.Join(dir, memorylog.TailName)
	before := readFile(t, tail)
	refused := errors.New("no backup")

	err := log.RewriteAfter(context.Background(), func() error { return refused }, keepAll)
	if !errors.Is(err, refused) {
		t.Errorf("a failing first step should stop the rewrite with its error, got %v", err)
	}

	if string(readFile(t, tail)) != string(before) {
		t.Error("a failing first step should leave the log as it was")
	}
}
