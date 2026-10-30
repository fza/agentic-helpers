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

	err := atomicfile.Write(dir, path, []byte("new"), 0o644)
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

	err := atomicfile.Write(dir, path, []byte("{}"), 0o600)
	if err == nil {
		t.Error("a file that cannot be put in place should be an error")
	}

	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Errorf("the temporary file should be removed, got %v %v", entries, err)
	}
}

func TestParallelWritersNeverPublishAMix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vectors.gob")

	payloads := make([][]byte, 8)
	for index := range payloads {
		payloads[index] = bytes.Repeat([]byte{byte('a' + index)}, (index+1)<<16)
	}

	err := atomicfile.Write(dir, path, payloads[0], 0o600)
	if err != nil {
		t.Fatalf("writing the first content: %v", err)
	}

	var writers sync.WaitGroup

	for _, payload := range payloads {
		writers.Go(func() {
			for range 50 {
				_ = atomicfile.Write(dir, path, payload, 0o600)
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

func TestAWriteNeverLandsAtAPlantedLink(t *testing.T) {
	for _, planting := range test.Plantings() {
		t.Run(planting.Name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "state", "ledger.json")
			outside := test.Outside(t)
			planting.Plant(t, path, outside)

			err := atomicfile.Write(root, path, []byte("written"), 0o600)

			if !test.Untouched(t, outside) {
				t.Error("a write should never land outside its root")
			}

			if err == nil && planting.Name != test.Plantings()[0].Name {
				t.Error("a write through a planted folder link or onto a hard link should be refused")
			}

			if err == nil && test.ReadFile(t, path) != "written" {
				t.Error("a write past a link at the file should replace the link")
			}
		})
	}
}

func TestARootThatIsALinkKeepsWorking(t *testing.T) {
	real := t.TempDir()
	root := filepath.Join(t.TempDir(), "share")

	err := os.Symlink(real, root)
	if err != nil {
		t.Fatalf("linking the root: %v", err)
	}

	err = atomicfile.Write(root, filepath.Join(root, "index.json"), []byte("{}"), 0o600)
	if err != nil || test.ReadFile(t, filepath.Join(real, "index.json")) != "{}" {
		t.Errorf("a root that is a link should be written through, got %v", err)
	}
}
