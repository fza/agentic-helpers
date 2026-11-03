package home

import (
	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
)

// ExcludeCache keeps the cache root out of the machine's backups where it
// exists without the mark, as SessionStart does: a root already marked is
// left alone, so a SessionStart writes nothing, and a missing one refuses the
// mark. Everything below the root rebuilds itself, while a backup copies a
// changed slot file whole. A failure to set the mark is ignored: the backup
// only costs more.
func (roots Roots) ExcludeCache() {
	if excludedFromBackups(roots.Cache) {
		return
	}

	_ = excludeFromBackups(roots.Cache)
}

// ExcludeCacheOnCreate has the writer that makes the cache root mark it.
func (roots Roots) ExcludeCacheOnCreate() {
	safefs.OnCreate(roots.Cache, func(root string) { _ = excludeFromBackups(root) })
}
