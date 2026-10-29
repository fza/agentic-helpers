package migration

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
)

const (
	writersName = "writers.json"
	formatName  = "format.json"
	// walkDepth bounds a walk of an old tree: a key is `local/<hash>` or a
	// repo_id of a few path segments.
	walkDepth = 8
	// metadataLimit caps a writers list or a format read.
	metadataLimit = 64 << 10
)

// oldKey is one old key and what earlier releases kept under it: its
// memory store, its tool library and the git common directories its writers
// list names, each a folder that exists or empty.
type oldKey struct {
	key     string
	memory  string
	tools   string
	writers []string
}

func (held oldKey) hasData() bool { return held.memory != "" || held.tools != "" }

// survey finds every old key: a memory store, a tool library or a writers
// list under it.
func (env Env) survey() []*oldKey {
	byKey := map[string]*oldKey{}

	at := func(key string) *oldKey {
		found, known := byKey[key]
		if !known {
			found = &oldKey{key: key}
			byKey[key] = found
		}

		return found
	}

	walkKeys(env.memoryRoot(), isMemoryStore, func(key string, dir string) { at(key).memory = dir })
	walkKeys(env.toolsRoot(), isToolsStore, func(key string, dir string) { at(key).tools = dir })
	walkKeys(env.writersRoot(), hasWriters, func(key string, _ string) { at(key) })

	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	projects := make([]*oldKey, 0, len(keys))

	for _, key := range keys {
		held := byKey[key]
		held.writers = readWriters(filepath.Join(env.writersRoot(), filepath.FromSlash(key), writersName))

		if held.writers == nil {
			held.writers = readWriters(filepath.Join(env.toolsRoot(), filepath.FromSlash(key), writersName))
		}

		projects = append(projects, held)
	}

	return projects
}

// walkKeys calls found for every folder below root that matches, by its key,
// never descending into one. A hidden folder is a tree a run set aside.
func walkKeys(root string, matches func(string) bool, found func(key string, dir string)) {
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.IsDir() {
			return nil
		}

		relative, relErr := filepath.Rel(root, path)
		if relErr != nil || relative == "." {
			return nil
		}

		depth := len(strings.Split(relative, string(filepath.Separator)))
		if strings.HasPrefix(entry.Name(), ".") || depth > walkDepth {
			return filepath.SkipDir
		}

		if matches(path) {
			found(filepath.ToSlash(relative), path)

			return filepath.SkipDir
		}

		return nil
	})
}

func isMemoryStore(dir string) bool {
	if isStub(dir) {
		return false
	}

	return isDir(filepath.Join(dir, "seats")) || isDir(filepath.Join(dir, "log")) ||
		exists(filepath.Join(dir, formatName)) || exists(filepath.Join(dir, "seats.json"))
}

// isToolsStore is a library as earlier releases told one: an index or a
// lock, or a tool folder with a current version.
func isToolsStore(dir string) bool {
	if exists(filepath.Join(dir, "index.json")) || exists(filepath.Join(dir, ".lock")) {
		return true
	}

	listed, err := os.ReadDir(dir)
	if err != nil {
		return false
	}

	return slices.ContainsFunc(listed, func(entry fs.DirEntry) bool {
		return entry.IsDir() && exists(filepath.Join(dir, entry.Name(), "current"))
	})
}

func hasWriters(dir string) bool { return exists(filepath.Join(dir, writersName)) }

// readWriters reads a writers list: a JSON array of clean absolute git common
// directories. A list that cannot be read names none.
func readWriters(path string) []string {
	data, err := readSmall(path)
	if err != nil {
		return nil
	}

	var listed []string

	err = json.Unmarshal(data, &listed)
	if err != nil {
		return nil
	}

	clean := listed[:0]

	for _, common := range listed {
		if filepath.IsAbs(common) && filepath.Clean(common) == common {
			clean = append(clean, common)
		}
	}

	return clean
}

// readSmall reads a regular file of at most metadataLimit bytes, never a
// link or a pipe.
func readSmall(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}

	if !info.Mode().IsRegular() {
		return nil, fs.ErrInvalid
	}

	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}

	defer func() { _ = file.Close() }()

	return io.ReadAll(io.LimitReader(file, metadataLimit))
}

// placement is where a key's data goes: the checkouts of the store adopting
// it, or none, leaving it unclaimed.
type placement struct {
	checkouts []home.Checkout
	lastKnown []string
}

// place decides each key's store. A git common directory listed under several
// keys belongs to one: the key its checkout has now, else the first listing
// it, so no two stores claim one checkout.
func (env Env) place(ctx context.Context, projects []*oldKey) map[string]placement {
	inspected := map[string]home.Checkout{}
	owner := map[string]string{}

	for _, held := range projects {
		for _, common := range held.writers {
			_, known := owner[common]
			if !known {
				owner[common] = held.key
			}

			if !isDir(common) {
				continue
			}

			_, done := inspected[common]
			if done {
				continue
			}

			checkout, err := home.Inspect(ctx, filepath.Dir(common), env.Lookup, env.Git)
			if err == nil {
				inspected[common] = checkout
			}
		}
	}

	for common, checkout := range inspected {
		if slices.ContainsFunc(projects, func(held *oldKey) bool {
			return held.key == checkout.LegacyKey && slices.Contains(held.writers, common)
		}) {
			owner[common] = checkout.LegacyKey
		}
	}

	placed := map[string]placement{}

	for _, held := range projects {
		var living, offline []home.Checkout

		for _, common := range held.writers {
			if owner[common] != held.key {
				continue
			}

			checkout, alive := inspected[common]

			switch {
			case alive:
				living = append(living, checkout)
			case env.offline(common):
				offline = append(offline, pathOnly(common, held.key))
			}
		}

		switch {
		case len(living) > 0:
			for _, common := range held.writers {
				_, alive := inspected[common]
				if owner[common] == held.key && !alive && !env.offline(common) {
					living = append(living, pathOnly(common, held.key))
				}
			}

			placed[held.key] = placement{checkouts: append(living, offline...)}
		case len(offline) > 0:
			placed[held.key] = placement{checkouts: offline}
		default:
			placed[held.key] = placement{lastKnown: held.writers}
		}
	}

	return placed
}

// pathOnly is a checkout known by its path alone, as one on a volume not
// mounted: its first visit learns the rest and writes its pointer.
func pathOnly(common string, key string) home.Checkout {
	checkout := home.Checkout{Root: filepath.Dir(common), CommonDir: common}
	if !strings.HasPrefix(key, "local/") {
		checkout.RepoID = key
	}

	return checkout
}

// offline says the git common directory lies on a volume that is not
// mounted: below a mount root, with the nearest folder still there on the
// mount root's own device, so the volume it was on is gone and not merely
// the checkout.
func (env Env) offline(common string) bool {
	if exists(common) {
		return false
	}

	for _, root := range env.MountRoots {
		relative, err := filepath.Rel(root, common)
		if err != nil || relative == "." || strings.HasPrefix(relative, "..") {
			continue
		}

		nearest := common
		for !exists(nearest) && nearest != root {
			nearest = filepath.Dir(nearest)
		}

		return sameDevice(nearest, root)
	}

	return false
}

func sameDevice(first string, second string) bool {
	firstInfo, err := os.Stat(first)
	if err != nil {
		return false
	}

	secondInfo, err := os.Stat(second)
	if err != nil {
		return false
	}

	firstStat, readable := firstInfo.Sys().(*syscall.Stat_t)
	secondStat, alsoReadable := secondInfo.Sys().(*syscall.Stat_t)

	return readable && alsoReadable && firstStat.Dev == secondStat.Dev
}
