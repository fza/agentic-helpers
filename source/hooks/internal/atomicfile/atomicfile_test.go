package atomicfile_test

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/atomicfile"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestWriteReplacesTheFileWhole(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.json")
	test.WriteFile(t, path, "old")

	err := atomicfile.Write(path, []byte("new"), 0o644)
	if err != nil {
		t.Fatalf("writing: %v", err)
	}

	written, err := os.ReadFile(path)
	if err != nil || string(written) != "new" {
		t.Errorf("the file should hold the new content, got %q %v", written, err)
	}

	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Errorf("the file should carry the asked permissions, got %v %v", info.Mode(), err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Errorf("no temporary file should stay behind, got %v %v", entries, err)
	}
}

func TestAFailedWriteLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "heat.json")
	test.WriteFile(t, filepath.Join(path, "occupied"), "a directory stands where the file goes")

	err := atomicfile.Write(path, []byte("{}"), 0o600)
	if err == nil {
		t.Error("a file that cannot be put in place should be an error")
	}

	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Errorf("the temporary file should be removed, got %v %v", entries, err)
	}
}

func TestParallelWritersNeverPublishAMix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vectors.gob")

	payloads := make([][]byte, 8)
	for index := range payloads {
		payloads[index] = bytes.Repeat([]byte{byte('a' + index)}, (index+1)<<16)
	}

	err := atomicfile.Write(path, payloads[0], 0o600)
	if err != nil {
		t.Fatalf("writing the first content: %v", err)
	}

	var writers sync.WaitGroup

	for _, payload := range payloads {
		writers.Go(func() {
			for range 50 {
				_ = atomicfile.Write(path, payload, 0o600)
			}
		})
	}

	done := make(chan struct{})

	var reader sync.WaitGroup

	torn := 0

	reader.Go(func() {
		for {
			select {
			case <-done:
				return
			default:
			}

			read, err := os.ReadFile(path)
			if err != nil || !slices.ContainsFunc(payloads, func(payload []byte) bool { return bytes.Equal(read, payload) }) {
				torn++
			}
		}
	})

	writers.Wait()
	close(done)
	reader.Wait()

	if torn > 0 {
		t.Errorf("a reader should only ever see one writer's whole content, saw %d torn reads", torn)
	}

	final, err := os.ReadFile(path)
	if err != nil || !slices.ContainsFunc(payloads, func(payload []byte) bool { return bytes.Equal(final, payload) }) {
		t.Error("the last rename should leave one writer's whole content")
	}
}
