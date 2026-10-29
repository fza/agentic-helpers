package migration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// StubFormat is the memory store format a stub declares. Every release
// before the home reads a store up to its own format and refuses a newer one
// with its own line, `agentic-memory: this store has format <n>; this release
// reads up to <m>`, so a stub keeps a session of such a release from writing
// a store the home no longer reads. No memory format comes near it.
const StubFormat = 999

// FencePrefix opens the file standing where the old tool library and the
// writers lists were. A release before the home finds a file where it expects
// a folder: it reads no library and admits no checkout to any write.
const FencePrefix = "agentic-helpers moved this data to "

func fenceText(data string) []byte {
	return []byte(FencePrefix + data + "; a session started before the update has none until it restarts\n")
}

func stubText() []byte {
	data, _ := json.Marshal(map[string]int{"format": StubFormat})

	return append(data, '\n')
}

// isStub says the folder holds nothing but a stub's `format.json`.
func isStub(dir string) bool {
	listed, err := os.ReadDir(dir)
	if err != nil || len(listed) != 1 || listed[0].Name() != formatName {
		return false
	}

	data, err := readSmall(filepath.Join(dir, formatName))
	if err != nil {
		return false
	}

	var held struct {
		Format int `json:"format"`
	}

	return json.Unmarshal(data, &held) == nil && held.Format == StubFormat
}

// placeStub leaves a stub where a memory store moved out.
func (log *journal) placeStub(dir string) error {
	err := log.record(step{Kind: stepStub, Path: dir})
	if err != nil {
		return err
	}

	err = os.Mkdir(dir, 0o700)
	if err == nil {
		err = os.WriteFile(filepath.Join(dir, formatName), stubText(), 0o600)
	}

	if err != nil {
		return fmt.Errorf("placing the stub at %s: %w", dir, err)
	}

	return nil
}

// placeFence replaces the tree at root by a file naming the home, the tree
// set aside until the run completes.
func (log *journal) placeFence(root string, aside string, data string) error {
	switch {
	case isFence(root):
		return nil
	case exists(root) && !isDir(root):
		return fmt.Errorf("fencing %s: it is no folder", root)
	case isDir(root):
		err := log.record(step{Kind: stepRename, From: root, To: aside})
		if err != nil {
			return err
		}

		err = os.Rename(root, aside)
		if err != nil {
			return fmt.Errorf("setting %s aside: %w", root, err)
		}

		err = log.record(step{Kind: stepDiscard, Path: aside})
		if err != nil {
			return err
		}
	}

	err := log.record(step{Kind: stepFence, Path: root})
	if err != nil {
		return err
	}

	err = os.WriteFile(root, fenceText(data), 0o600)
	if err != nil {
		return fmt.Errorf("fencing %s: %w", root, err)
	}

	return nil
}

// isFence says the file at path is a fence this package placed.
func isFence(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}

	data, err := readSmall(path)

	return err == nil && bytes.HasPrefix(data, []byte(FencePrefix)) && strings.Count(string(data), "\n") == 1
}
