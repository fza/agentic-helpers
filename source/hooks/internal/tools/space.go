package tools

import (
	"cmp"
	"context"
	"fmt"
	"maps"
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
	store *store
	tool  string
	v     int
	at    string
	bytes int64
}

func (version storedVersion) String() string {
	return fmt.Sprintf("%s v%d of %s", version.tool, version.v, version.store.key)
}

func hashedBytes(hashes []fileHash) int64 {
	var total int64
	for _, hash := range hashes {
		total += hash.Size
	}

	return total
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

// versionsOf is every version a store keeps, and those of them it may prune:
// neither its tool's current version nor a fork's origin.
func versionsOf(held *store, named map[forkOrigin]bool) ([]storedVersion, []storedVersion) {
	var every, prunable []storedVersion

	for _, name := range held.names() {
		tool := held.index.Tools[name]

		for folder, hashes := range tool.Hashes {
			v, err := strconv.Atoi(strings.TrimPrefix(folder, "v"))
			if err != nil {
				continue
			}

			version := storedVersion{store: held, tool: name, v: v, at: tool.Written[folder], bytes: hashedBytes(hashes)}
			every = append(every, version)

			if v != tool.Version && !named[forkOrigin{Project: held.key, Tool: name, V: v}] {
				prunable = append(prunable, version)
			}
		}
	}

	return every, prunable
}

func usageOf(held *store, named map[forkOrigin]bool) space.Record {
	every, prunable := versionsOf(held, named)

	var record space.Record

	for _, version := range every {
		record.Stored += version.bytes
	}

	record.Held = record.Stored

	for _, version := range prunable {
		record.Held -= version.bytes
	}

	return record
}

// prunableVersions is every version the stores may prune, oldest first.
func prunableVersions(stores []*store, named map[forkOrigin]bool) []storedVersion {
	var all []storedVersion

	for _, held := range stores {
		_, prunable := versionsOf(held, named)
		all = append(all, prunable...)
	}

	slices.SortFunc(all, func(a, b storedVersion) int {
		return cmp.Or(strings.Compare(a.at, b.at), strings.Compare(a.store.key, b.store.key),
			strings.Compare(a.tool, b.tool), cmp.Compare(a.v, b.v))
	})

	return all
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

	var stored int64

	for _, held := range stores {
		stored += usageOf(held, named).Stored
	}

	if lib.scopeProject() {
		for key, record := range space.Read(ledgerPath(lib.root())) {
			if key != lib.key {
				stored += record.Stored
			}
		}
	}

	over := stored + incoming - int64(lib.options.ToolsTotalMB)<<20
	if over <= 0 {
		return nil
	}

	var (
		picked []storedVersion
		freed  int64
	)

	for _, version := range prunableVersions(stores, named) {
		if freed >= over {
			break
		}

		picked = append(picked, version)
		freed += version.bytes
	}

	if freed < over {
		return fmt.Errorf("the tool library would hold %s on this machine, past the %d MB limit (tools_total_mb), "+
			"and no version is left to prune: merge tools, or raise tools_total_mb", space.Size(stored+incoming),
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

	var stored int64

	for _, held := range stores {
		stored += usageOf(held, named).Stored
	}

	over := stored - int64(limitMB)<<20

	for _, version := range prunableVersions(stores, named) {
		if over <= 0 {
			break
		}

		if pruneLocked(ctx, version, pruneLockWait) == nil {
			over -= version.bytes
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
