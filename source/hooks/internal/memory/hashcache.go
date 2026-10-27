package memory

import (
	"os"
	"path/filepath"
)

// hashCache keeps each anchored file's content hash by its path, size and
// modification time, so a read hashes only a file that changed since a read
// last hashed it. Any sandboxed job can write the cache as it can the tree:
// a stamp that does not match costs a hash, never a wrong flag the tree
// itself would not give.
type hashCache struct {
	path    string
	stamps  map[string]fileStamp
	loaded  bool
	changed bool
}

type fileStamp struct {
	Size    int64  `json:"size"`
	ModTime int64  `json:"mtime"`
	Hash    string `json:"hash"`
}

func (mem *memory) hashes() *hashCache {
	if mem.hashCache == nil {
		mem.hashCache = &hashCache{path: filepath.Join(mem.cacheDir(), "anchor-hashes.json")}
	}

	return mem.hashCache
}

// hash is the content hash of the regular file at path, from the cache while
// its size and modification time are those it was hashed at.
func (cache *hashCache) hash(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}

	if !cache.loaded {
		cache.stamps, cache.loaded = map[string]fileStamp{}, true
		readJSON(cache.path, &cache.stamps)
	}

	stamp, known := cache.stamps[path]
	if known && stamp.Size == info.Size() && stamp.ModTime == info.ModTime().UnixNano() && stamp.Hash != "" {
		return stamp.Hash, true
	}

	hash, found := fileHash(path)
	if found {
		cache.stamps[path] = fileStamp{Size: info.Size(), ModTime: info.ModTime().UnixNano(), Hash: hash}
		cache.changed = true
	}

	return hash, found
}

// save keeps what this call hashed for the next one. The cache may be lost at
// any time, so a failure to write it costs hashing, never memory.
func (cache *hashCache) save() {
	if cache == nil || !cache.changed {
		return
	}

	_ = writeJSON(cache.path, cache.stamps)
	cache.changed = false
}

// fileHash is the content hash of the file at path, through the read's hash
// cache where the checkout carries one.
func (here tree) fileHash(path string) (string, bool) {
	if here.hashes == nil {
		return fileHash(path)
	}

	return here.hashes.hash(path)
}
