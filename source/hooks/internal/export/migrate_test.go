package export_test

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/export"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestExportReadsAnOlderStoreThroughAMigratedCopy(t *testing.T) {
	m := newMachine(t)
	store := m.older()
	before, cacheBefore := test.TreeState(t, m.roots().Data), test.TreeState(t, m.roots().Cache)
	target := filepath.Join(t.TempDir(), "machine.tar.gz")

	code, out, errOut := m.run(target)
	if code != 0 || !strings.Contains(out, "memory read from a copy migrated from format 1") {
		t.Fatalf("an older store should export through a migrated copy, saying so: %d %s%s", code, out, errOut)
	}

	if !maps.Equal(before, test.TreeState(t, m.roots().Data)) {
		t.Errorf("the older store itself should stay byte for byte as it was")
	}

	_, err := os.Lstat(filepath.Join(m.roots().Cache, "export-tmp"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the migrated copy should be removed once read: %v", err)
	}

	if !maps.Equal(cacheBefore, test.TreeState(t, m.roots().Cache)) {
		t.Errorf("migrating the copy should leave the store's own cache as it was")
	}

	manifest, err := export.Verify(target)
	if err != nil || len(manifest.Stores) != 1 {
		t.Fatalf("the archive should read back whole: %+v %v", manifest, err)
	}

	held := manifest.Stores[0]
	if held.MigratedFrom["memory"] != 1 || held.MigratedFrom["tools"] != 1 || held.Formats["memory"] != 11 ||
		held.Formats["tools"] != 3 {
		t.Errorf("the manifest should name the format each component migrated from and the one exported: %+v", held)
	}

	members := test.ArchiveMembers(t, target)
	prefix := "stores/" + filepath.Base(store) + "/"
	entry := byID(t, lines(t, member(t, members, prefix+"entries.jsonl")), "m-cccccc")

	if entry["title"] != "An older drive finding sealed" || !strings.HasPrefix(entry["seat"].(string), "s-") {
		t.Errorf("the older entry should travel as the migrated copy reads it, its seat by id: %v", entry)
	}

	if len(lines(t, member(t, members, prefix+"tools/log-scan/history.jsonl"))) != 1 {
		t.Errorf("the older library's history should travel")
	}
}

func TestExportLeavesTheCacheTreeAsItFoundIt(t *testing.T) {
	for _, tc := range []struct {
		name string
		// emptyRoot keeps an empty cache root in place of none.
		emptyRoot bool
	}{
		{"no cache root, nor the folder above it", false},
		{"an empty cache root", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine(t)
			m.older()

			parent := filepath.Dir(m.roots().Cache)

			err := os.RemoveAll(parent)
			if err != nil {
				t.Fatalf("removing the cache root and its parent: %v", err)
			}

			if tc.emptyRoot {
				err = os.MkdirAll(m.roots().Cache, 0o700)
				if err != nil {
					t.Fatalf("making an empty cache root: %v", err)
				}
			}

			code, out, errOut := m.run(filepath.Join(t.TempDir(), "machine.tar.gz"))
			if code != 0 || !strings.Contains(out, "migrated from format 1") {
				t.Fatalf("an older store should export through a migrated copy: %d %s%s", code, out, errOut)
			}

			listed, err := os.ReadDir(m.roots().Cache)

			switch {
			case tc.emptyRoot && (err != nil || len(listed) != 0):
				t.Errorf("an empty cache root should stay, and stay empty: %v %v", listed, err)
			case !tc.emptyRoot:
				_, err = os.Lstat(parent)
				if !errors.Is(err, os.ErrNotExist) {
					t.Errorf("an export on a home with no cache root should leave no cache root, nor any folder above it: %v", err)
				}
			}
		})
	}
}

func TestExportRemovesTheCopyWhenItCannotMigrateIt(t *testing.T) {
	m := newMachine(t)
	store := m.older()
	outside := test.Outside(t)

	err := os.Symlink(outside, filepath.Join(store, "memory", "seats", "planted"))
	if err != nil {
		t.Fatalf("planting the link: %v", err)
	}

	before := test.TreeState(t, m.roots().Data)
	dir := t.TempDir()

	code, out, errOut := m.run(filepath.Join(dir, "machine.tar.gz"))
	if code != 1 || !strings.Contains(errOut, "copying") {
		t.Fatalf("a store that cannot be copied should refuse the export: %d %s%s", code, out, errOut)
	}

	_, err = os.Lstat(filepath.Join(m.roots().Cache, "export-tmp"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the partial copy should be removed on failure: %v", err)
	}

	listed, _ := os.ReadDir(dir)
	if len(listed) != 0 || !maps.Equal(before, test.TreeState(t, m.roots().Data)) || !test.Untouched(t, outside) {
		t.Errorf("a refused export should write nothing and change no store")
	}
}

func TestExportReplacesACopyAKilledRunLeft(t *testing.T) {
	m := newMachine(t)
	store := m.older()
	id := filepath.Base(store)
	test.WriteFile(t, filepath.Join(m.roots().Cache, "export-tmp", id, "data", "stores", id, "memory", "seats", "main.json"),
		"left by a killed export")

	code, out, errOut := m.run(filepath.Join(t.TempDir(), "machine.tar.gz"))
	if code != 0 {
		t.Fatalf("a copy a killed run left should give way to a fresh one: %s%s", out, errOut)
	}

	_, err := os.Lstat(filepath.Join(m.roots().Cache, "export-tmp"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("no copy should stay once the export is written: %v", err)
	}
}
