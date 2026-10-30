package tools

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
	"github.com/fza/agentic-helpers/source/hooks/internal/space"
)

// pruneLockWait bounds how long the SessionStart pass waits for a store's
// lock: a store being written is passed over until the next session.
const pruneLockWait = 200 * time.Millisecond

// Space is the tool library's space as every store's size ledger records it:
// the bytes of the versions nothing may prune.
func Space(roots home.Roots, limitMB int) space.Figure {
	return figureOf(ledgers(roots), limitMB)
}

// ledgers is what every store's size ledger records, by store id.
func ledgers(roots home.Roots) map[string]space.Record {
	records := map[string]space.Record{}

	for _, key := range storeIDs(roots) {
		records[key] = space.LedgerTotal(space.ReadLedger(roots.Store(key, home.Tools)))
	}

	return records
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

func walkedBytes(files ...[]safefs.Walked) int64 {
	var total int64

	for _, set := range files {
		for _, file := range set {
			total += int64(len(file.Data))
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
	root, err := safefs.OpenDir(at)
	if err != nil {
		return 0
	}

	defer root.Close()

	toolDir, err := root.Sub(tool)
	if err != nil {
		return 0
	}

	defer toolDir.Close()

	version, err := toolDir.Sub(folder)
	if err != nil {
		return 0
	}

	defer version.Close()

	return size(version, versionLimit)
}

// storeOnDisk is what a store's version folders hold on disk, counted to at
// most limit, read without its index.
func storeOnDisk(at string, limit int64) int64 {
	root, err := safefs.OpenDir(at)
	if err != nil {
		return 0
	}

	defer root.Close()

	tools, err := root.Entries()
	if err != nil {
		return 0
	}

	var total int64

	for _, entry := range tools {
		if !entry.IsDir() || total >= limit {
			continue
		}

		toolDir, err := root.Sub(entry.Name())
		if err != nil {
			continue
		}

		versions, err := toolDir.Entries()
		if err == nil {
			for _, folder := range versions {
				_, valid := versionNumber(folder.Name())
				if !valid || !folder.IsDir() {
					continue
				}

				version, err := toolDir.Sub(folder.Name())
				if err != nil {
					continue
				}

				total = space.Add(total, size(version, versionLimit))
				version.Close()
			}
		}

		toolDir.Close()
	}

	return min(total, limit)
}

// size is what the regular files below held take, by their lstat, counted to
// at most limit and safefs.WalkDepth folders deep.
func size(held *safefs.Dir, limit int64) int64 {
	var total int64

	var descend func(current *safefs.Dir, depth int)

	descend = func(current *safefs.Dir, depth int) {
		listed, err := current.Entries()
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
			case info.IsDir() && depth < safefs.WalkDepth:
				child, err := current.Sub(entry.Name())
				if err != nil {
					continue
				}

				descend(child, depth+1)
				child.Close()
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
	root, err := safefs.OpenDir(version.store.path)
	if err != nil {
		return err
	}

	defer root.Close()

	folder, err := root.Sub(version.tool)
	if err != nil {
		return err
	}

	defer folder.Close()

	tool := version.store.index.Tools[version.tool]

	err = appendJournal(folder, prunedJournal, prunedLine{V: version.v, At: version.at,
		Hashes: tool.Hashes[versionDir(version.v)]})
	if err != nil {
		return err
	}

	err = folder.RemoveAll(versionDir(version.v))
	if err != nil {
		return fmt.Errorf("removing %s/%s: %w", folder.Path(), versionDir(version.v), err)
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
	scoped := lib.scopeProject()

	recorded := recordedBytes(stores, named)

	if scoped {
		for key, record := range ledgers(lib.env.Roots) {
			if key != lib.key {
				recorded = space.Add(recorded, record.Stored)
			}
		}
	}

	if space.Add(recorded, incoming) <= limit {
		return nil
	}

	stored, prunable := measured(stores, named)

	if scoped {
		for _, at := range lib.unreached() {
			stored = space.Add(stored, storeOnDisk(at, limit))
		}
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
		err := lib.prune(ctx, version, named)
		if err != nil {
			return fmt.Errorf("pruning %s to make room: %w", version, err)
		}
	}

	return nil
}

// unreached is the folder of every store on the machine but the own one, each
// once: what a project-scoped caller measures on disk instead of trusting the
// ledger. A store is a folder storeIDs lists, each once, never a key the
// ledger names, so no folder elsewhere on the machine counts. The own store is
// told apart by the folder's identity, not its id.
func (lib *library) unreached() []string {
	own, ownErr := os.Lstat(lib.ownDir())

	var folders []string

	for _, key := range storeIDs(lib.env.Roots) {
		at := lib.env.Roots.Store(key, home.Tools)

		info, err := os.Lstat(at)
		if err != nil || (ownErr == nil && os.SameFile(own, info)) {
			continue
		}

		folders = append(folders, at)
	}

	return folders
}

func (lib *library) prune(ctx context.Context, version storedVersion, named map[forkOrigin]bool) error {
	if version.store.own {
		return pruneVersion(version)
	}

	return pruneLocked(ctx, version, named, cmp.Or(lib.env.LockWait, 2*time.Second))
}

// recordSpace is the library's space as every store's ledger records it, the
// own store's refreshed by the write before it.
func (lib *library) recordSpace(ctx context.Context) space.Figure {
	return Space(lib.env.Roots, lib.options.ToolsTotalMB)
}

// Prune is the SessionStart pass over every store on the machine: it prunes
// every prunable version replaced longer than lifetime before now, then, while
// the stores hold more than limitMB, their oldest prunable versions, passing
// over a store another process is writing, then brings every store still of
// an earlier format to this one and gives it its size ledger.
func Prune(ctx context.Context, roots home.Roots, limitMB int, lifetime time.Duration, now time.Time) {
	ids := storeIDs(roots)
	if len(ids) == 0 {
		return
	}

	var stores []*store

	for _, key := range ids {
		held := &store{key: key, root: roots.Data, path: roots.Store(key, home.Tools)}

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

	for _, version := range aged(stores, named, now.Add(-lifetime)) {
		_ = pruneLocked(ctx, version, named, pruneLockWait)
	}

	limit := int64(limitMB) << 20

	if recordedBytes(stores, named) > limit {
		stored, prunable := measured(stores, named)
		over := stored - limit

		for _, version := range prunable {
			if over <= 0 {
				break
			}

			if pruneLocked(ctx, version, named, pruneLockWait) == nil {
				over -= version.bytes
			}
		}
	}

	// A pruned store's ledger was refreshed with its prune; the pass writes
	// one only where a store keeps none yet, so a ledger stays what its
	// writers recorded.
	for _, held := range stores {
		if ledgerMissing(held.path) {
			ledgerLocked(ctx, roots, held, named, roots.StoreCache(held.key, home.Tools))
		}
	}
}

// aged is every prunable version its tool replaced before cutoff.
func aged(stores []*store, named map[forkOrigin]bool, cutoff time.Time) []storedVersion {
	var due []storedVersion

	for _, held := range stores {
		for _, version := range versionsOf(held, named) {
			replaced, known := replacedAt(held.index.Tools[version.tool], version.v)
			if version.prunable && known && replaced.Before(cutoff) {
				due = append(due, version)
			}
		}
	}

	return due
}

// replacedAt is when version v stopped being its tool's current one: the
// recorded write of the next version still stored. Counted from v's own write, a
// version current for a year would go the moment an update replaced it,
// taking the way back with it.
func replacedAt(tool *indexed, v int) (time.Time, bool) {
	var later []int

	for folder := range tool.Written {
		next, valid := versionNumber(folder)
		if valid && next > v {
			later = append(later, next)
		}
	}

	slices.Sort(later)

	for _, next := range later {
		written, err := time.Parse(time.RFC3339, tool.Written[versionDir(next)])
		if err == nil {
			return written, true
		}
	}

	return time.Time{}, false
}

// ledgerLocked refreshes a store's size ledger holding its lock, passing over
// a store another process is writing.
func ledgerLocked(ctx context.Context, roots home.Roots, held *store, named map[forkOrigin]bool, cache string) {
	root, err := safefs.OpenDir(held.path)
	if err != nil {
		return
	}

	defer root.Close()

	unlock, err := lockStore(ctx, root, pruneLockWait)
	if err != nil {
		return
	}

	defer unlock()

	err = prepareStore(ctx, root, roots, held.path, cache)
	if err == nil {
		refreshLedger(ctx, held, named)
	}
}

// keepIndex writes an index the pass rebuilt back into its store, so the next
// pass reads the sizes it recorded instead of reading every file again.
func keepIndex(ctx context.Context, at string, built *index) {
	root, err := safefs.OpenDir(at)
	if err != nil {
		return
	}

	defer root.Close()

	unlock, err := lockStore(ctx, root, pruneLockWait)
	if err != nil {
		return
	}

	defer unlock()

	_ = writeIndexInto(root, built)
}

func pruneLocked(ctx context.Context, version storedVersion, named map[forkOrigin]bool, wait time.Duration) error {
	root, err := safefs.OpenDir(version.store.path)
	if err != nil {
		return err
	}

	defer root.Close()

	unlock, err := lockStore(ctx, root, wait)
	if err != nil {
		return err
	}

	defer unlock()

	err = pruneVersion(version)
	if err != nil {
		return err
	}

	refreshLedger(ctx, version.store, named)

	return nil
}
