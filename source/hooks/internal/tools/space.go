package tools

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/projectkey"
	"github.com/fza/agentic-helpers/source/hooks/internal/space"
)

// ledgerName is the machine's ledger of every store's recorded size, beside
// the stores: what the agentic-memory brief reads without opening an index.
const ledgerName = ".space.json"

// pruneLockWait bounds how long the SessionStart pass waits for a store's
// lock: a store being written is passed over until the next session.
const pruneLockWait = 200 * time.Millisecond

// Root is the folder every project's tool library lies below.
func Root(home string) string { return filepath.Join(home, ".claude", self) }

func ledgerPath(root string) string { return filepath.Join(root, ledgerName) }

// Space is the tool library's space as the machine's ledger records it.
func Space(home string, limitMB int) space.Figure {
	return figureOf(space.Read(ledgerPath(Root(home))), limitMB)
}

func figureOf(records map[string]space.Record, limitMB int) space.Figure {
	return space.Figure{Kind: space.Tools, Used: space.Total(records).Held, Limit: int64(limitMB) << 20}
}

// storedVersion is one version a store keeps on disk.
type storedVersion struct {
	store    *store
	tool     string
	v        int
	at       string
	bytes    int64
	prunable bool
}

func (version storedVersion) String() string {
	return fmt.Sprintf("%s v%d of %s", version.tool, version.v, version.store.key)
}

// hashedBytes is a version's size as its index recorded it, never more than
// a tool may hold: an index anything sandboxed can write must not inflate the
// total into pruning valid versions elsewhere.
func hashedBytes(hashes []fileHash) int64 {
	var total int64
	for _, hash := range hashes {
		total = space.Add(total, min(max(hash.Size, 0), toolLimit))
	}

	return min(total, toolLimit)
}

// validSizes says whether every size an index row records could be a real
// version's: none negative, none past what a tool may hold.
func validSizes(row *indexed) bool {
	for _, hashes := range row.Hashes {
		var total int64

		for _, hash := range hashes {
			if hash.Size < 0 || hash.Size > toolLimit {
				return false
			}

			total += hash.Size
		}

		if total > toolLimit {
			return false
		}
	}

	return true
}

// versionStored says whether a version's folder is there: a version an index
// names without one neither counts nor is pruned.
func versionStored(at string, tool string, folder string) bool {
	info, err := os.Lstat(filepath.Join(at, tool, folder))

	return err == nil && info.IsDir()
}

func walkedBytes(files ...[]walked) int64 {
	var total int64

	for _, set := range files {
		for _, file := range set {
			total += int64(len(file.data))
		}
	}

	return total
}

// origins is every version some fork in the stores names as its origin.
func origins(stores []*store) map[forkOrigin]bool {
	named := map[forkOrigin]bool{}

	for _, held := range stores {
		for _, tool := range held.index.Tools {
			if tool.ForkedFrom != nil {
				named[*tool.ForkedFrom] = true
			}
		}
	}

	return named
}

func versionNumber(folder string) (int, bool) {
	v, err := strconv.Atoi(strings.TrimPrefix(folder, "v"))

	return v, err == nil && folder == versionDir(v)
}

// versionsOf is every version a store keeps, with its recorded size, each
// marked prunable where it is neither its tool's current version nor a fork's
// origin.
func versionsOf(held *store, named map[forkOrigin]bool) []storedVersion {
	var every []storedVersion

	for _, name := range held.names() {
		tool := held.index.Tools[name]

		for folder, hashes := range tool.Hashes {
			v, valid := versionNumber(folder)
			if !valid || !versionStored(held.path, name, folder) {
				continue
			}

			every = append(every, storedVersion{store: held, tool: name, v: v, at: tool.Written[folder],
				bytes:    hashedBytes(hashes),
				prunable: v != tool.Version && !named[forkOrigin{Project: held.key, Tool: name, V: v}]})
		}
	}

	return every
}

func usageOf(held *store, named map[forkOrigin]bool) space.Record {
	var record space.Record

	for _, version := range versionsOf(held, named) {
		record.Stored = space.Add(record.Stored, version.bytes)

		if !version.prunable {
			record.Held = space.Add(record.Held, version.bytes)
		}
	}

	return record
}

// recordedBytes is what the stores' indexes record they hold: it only decides
// whether the stores are measured.
func recordedBytes(stores []*store, named map[forkOrigin]bool) int64 {
	var total int64

	for _, held := range stores {
		total = space.Add(total, usageOf(held, named).Stored)
	}

	return total
}

// measured is what every version in the stores holds on disk, and the
// prunable ones with their sizes on disk, oldest first. An index and the
// ledger are writable by anything sandboxed, so a prune is decided on these
// sizes, never on recorded ones.
func measured(stores []*store, named map[forkOrigin]bool) (int64, []storedVersion) {
	var (
		stored   int64
		prunable []storedVersion
	)

	for _, held := range stores {
		for _, version := range versionsOf(held, named) {
			version.bytes = versionOnDisk(held.path, version.tool, versionDir(version.v))
			stored = space.Add(stored, version.bytes)

			if version.prunable {
				prunable = append(prunable, version)
			}
		}
	}

	slices.SortFunc(prunable, func(a, b storedVersion) int {
		return cmp.Or(strings.Compare(a.at, b.at), strings.Compare(a.store.key, b.store.key),
			strings.Compare(a.tool, b.tool), cmp.Compare(a.v, b.v))
	})

	return stored, prunable
}

func versionOnDisk(at string, tool string, folder string) int64 {
	root, err := openDir(at)
	if err != nil {
		return 0
	}

	defer root.close()

	toolDir, err := root.sub(tool)
	if err != nil {
		return 0
	}

	defer toolDir.close()

	version, err := toolDir.sub(folder)
	if err != nil {
		return 0
	}

	defer version.close()

	return version.size(versionLimit)
}

// storeOnDisk is what a store's version folders hold on disk, counted to at
// most limit, read without its index.
func storeOnDisk(at string, limit int64) int64 {
	root, err := openDir(at)
	if err != nil {
		return 0
	}

	defer root.close()

	tools, err := root.entries()
	if err != nil {
		return 0
	}

	var total int64

	for _, entry := range tools {
		if !entry.IsDir() || total >= limit {
			continue
		}

		toolDir, err := root.sub(entry.Name())
		if err != nil {
			continue
		}

		versions, err := toolDir.entries()
		if err == nil {
			for _, folder := range versions {
				_, valid := versionNumber(folder.Name())
				if !valid || !folder.IsDir() {
					continue
				}

				version, err := toolDir.sub(folder.Name())
				if err != nil {
					continue
				}

				total = space.Add(total, version.size(versionLimit))
				version.close()
			}
		}

		toolDir.close()
	}

	return min(total, limit)
}

// size is what the regular files below held take, by their lstat, counted to
// at most limit and walkDepth folders deep.
func (held *dir) size(limit int64) int64 {
	var total int64

	var descend func(current *dir, depth int)

	descend = func(current *dir, depth int) {
		listed, err := current.entries()
		if err != nil {
			return
		}

		for _, entry := range listed {
			if total >= limit {
				return
			}

			info, err := entry.Info()
			if err != nil {
				continue
			}

			switch {
			case info.IsDir() && depth < walkDepth:
				child, err := current.sub(entry.Name())
				if err != nil {
					continue
				}

				descend(child, depth+1)
				child.close()
			case info.Mode().IsRegular():
				total = space.Add(total, info.Size())
			}
		}
	}

	descend(held, 0)

	return min(total, limit)
}

// pruneVersion removes one version from its store: its files' hashes go into
// the tool's `pruned.jsonl` first, so a crash between the two leaves the
// duplicate check whole, then the folder goes and the store's index is read
// again from disk. The caller holds the store's lock.
func pruneVersion(version storedVersion) error {
	root, err := openDir(version.store.path)
	if err != nil {
		return err
	}

	defer root.close()

	folder, err := root.sub(version.tool)
	if err != nil {
		return err
	}

	defer folder.close()

	tool := version.store.index.Tools[version.tool]

	err = appendLine(folder, "pruned.jsonl", prunedLine{V: version.v, At: version.at,
		Hashes: tool.Hashes[versionDir(version.v)]})
	if err != nil {
		return err
	}

	err = folder.root.RemoveAll(versionDir(version.v))
	if err != nil {
		return fmt.Errorf("removing %s/%s: %w", folder.path, versionDir(version.v), err)
	}

	row, err := readTool(folder, version.tool)
	if err != nil {
		return err
	}

	current, found := loadIndex(version.store.path)
	if !found {
		current, err = buildIndex(version.store.path)
		if err != nil {
			return err
		}
	}

	current.Tools[version.tool] = row
	version.store.index.Tools[version.tool] = row

	return writeIndexInto(root, current)
}

// makeRoom prunes the oldest prunable versions in reach until a write of
// incoming bytes fits under tools_total_mb, and refuses the write where
// pruning every one of them would not. The caller holds the own store's lock.
func (lib *library) makeRoom(ctx context.Context, incoming int64) error {
	stores := lib.stores(ctx)
	named := origins(stores)
	limit := int64(lib.options.ToolsTotalMB) << 20
	others := lib.unreached()

	recorded := recordedBytes(stores, named)
	for _, record := range others {
		recorded = space.Add(recorded, record.Stored)
	}

	if space.Add(recorded, incoming) <= limit {
		return nil
	}

	stored, prunable := measured(stores, named)
	for key := range others {
		stored = space.Add(stored, storeOnDisk(projectkey.Dir(lib.root(), key), limit))
	}

	over := space.Add(stored, incoming) - limit
	if over <= 0 {
		return nil
	}

	var (
		picked []storedVersion
		freed  int64
	)

	for _, version := range prunable {
		if freed >= over {
			break
		}

		picked = append(picked, version)
		freed = space.Add(freed, version.bytes)
	}

	if freed < over {
		return fmt.Errorf("the tool library would hold %s on this machine, past the %d MB limit (tools_total_mb), "+
			"and no version is left to prune: merge tools, or raise tools_total_mb", space.Size(space.Add(stored, incoming)),
			lib.options.ToolsTotalMB)
	}

	for _, version := range picked {
		err := lib.prune(ctx, version)
		if err != nil {
			return fmt.Errorf("pruning %s to make room: %w", version, err)
		}
	}

	return nil
}

// unreached is the ledger's record of every store a project-scoped caller
// cannot reach. The ledger is writable by anything sandboxed, so a key counts
// only where it names a folder below the root in its one clean form: never a
// folder elsewhere on the machine, never the own store a second time.
func (lib *library) unreached() map[string]space.Record {
	if !lib.scopeProject() {
		return nil
	}

	records := map[string]space.Record{}

	for key, record := range space.Read(ledgerPath(lib.root())) {
		local := filepath.FromSlash(key)
		if key != lib.key && filepath.IsLocal(local) && filepath.Clean(local) == local {
			records[key] = record
		}
	}

	return records
}

func (lib *library) prune(ctx context.Context, version storedVersion) error {
	if version.store.own {
		return pruneVersion(version)
	}

	return pruneLocked(ctx, version, cmp.Or(lib.env.LockWait, 2*time.Second))
}

// recordSpace writes the figures of every store in reach into the machine's
// ledger and answers the library's space. Reaching every store, it replaces
// the whole ledger, a store gone included.
func (lib *library) recordSpace(ctx context.Context) space.Figure {
	lib.forget()

	stores := lib.stores(ctx)
	named := origins(stores)

	records := map[string]space.Record{}
	for _, held := range stores {
		records[held.key] = usageOf(held, named)
	}

	scoped := lib.scopeProject()

	_ = space.Update(ledgerPath(lib.root()), func(ledger map[string]space.Record) {
		if !scoped {
			clear(ledger)
		}

		maps.Copy(ledger, records)
		records = maps.Clone(ledger)
	})

	return figureOf(records, lib.options.ToolsTotalMB)
}

// Prune is the SessionStart pass over every store on the machine: while the
// stores hold more than limitMB, it prunes their oldest prunable versions,
// passing over a store another process is writing, then records every
// store's figures in the ledger afresh.
func Prune(ctx context.Context, home string, limitMB int) {
	root := Root(home)
	if !exists(root) {
		return
	}

	var stores []*store

	for _, key := range storeKeys(root) {
		held := &store{key: key, path: projectkey.Dir(root, key)}

		loaded, found := loadIndex(held.path)
		if !found {
			built, err := buildIndex(held.path)
			if err != nil {
				continue
			}

			loaded = built
			keepIndex(ctx, held.path, built)
		}

		held.index = loaded
		stores = append(stores, held)
	}

	named := origins(stores)
	limit := int64(limitMB) << 20

	if recordedBytes(stores, named) > limit {
		stored, prunable := measured(stores, named)
		over := stored - limit

		for _, version := range prunable {
			if over <= 0 {
				break
			}

			if pruneLocked(ctx, version, pruneLockWait) == nil {
				over -= version.bytes
			}
		}
	}

	_ = space.Update(ledgerPath(root), func(ledger map[string]space.Record) {
		clear(ledger)

		for _, held := range stores {
			ledger[held.key] = usageOf(held, named)
		}
	})
}

// keepIndex writes an index the pass rebuilt back into its store, so the next
// pass reads the sizes it recorded instead of reading every file again.
func keepIndex(ctx context.Context, at string, built *index) {
	root, err := openDir(at)
	if err != nil {
		return
	}

	defer root.close()

	unlock, err := lockStore(ctx, root, pruneLockWait)
	if err != nil {
		return
	}

	defer unlock()

	_ = writeIndexInto(root, built)
}

func pruneLocked(ctx context.Context, version storedVersion, wait time.Duration) error {
	root, err := openDir(version.store.path)
	if err != nil {
		return err
	}

	defer root.close()

	unlock, err := lockStore(ctx, root, wait)
	if err != nil {
		return err
	}

	defer unlock()

	return pruneVersion(version)
}
