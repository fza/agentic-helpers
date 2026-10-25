//go:build unix

package memorylog_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
)

func TestFaultInMappingIsAnError(t *testing.T) {
	dir := t.TempDir()
	log := openLog(t, dir)
	appendEvents(t, log, event(0, memorylog.SealBytes))

	snapshot, err := log.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("taking a snapshot: %v", err)
	}
	defer snapshot.Close()

	err = os.Truncate(filepath.Join(dir, readManifest(t, dir).Segments[0]), 0)
	if err != nil {
		t.Fatalf("truncating the mapped segment: %v", err)
	}

	_, err = snapshot.Payload(0)
	if !errors.Is(err, memorylog.ErrFault) {
		t.Errorf("a payload read from a segment truncated under its mapping should fail as a fault, got: %v", err)
	}

	if debug.SetPanicOnFault(false) {
		t.Error("reading a mapping should restore the goroutine's fault setting")
	}
}
