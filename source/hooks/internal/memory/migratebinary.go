package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/formatsteps"
	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
)

// legacyCacheDirs are the folders of the cache in which earlier releases kept
// a JSON file per session and purpose, or per seat.
var legacyCacheDirs = []string{"shown", "ladder", "graph-seen", "forks", "waiters", "migration-failed", "traps", "briefs"}

// slotSteps move a format-5 store to format 6: the log's `tail.jsonl` is
// sealed into a segment through the log's own crash-safe steps, every
// per-session file of the cache goes into the slot file, and only then do the
// per-session files go. A run carrying the step on finds a sealed tail and
// takes in the files still there, each under its own key; the size ledger is
// rebuilt once the run ends.
func (mem *memory) slotSteps(context.Context) ([]formatsteps.Operation, error) {
	return []formatsteps.Operation{
		{Name: "sealing the store's tail", Apply: func(ctx context.Context) error {
			_, err := os.Stat(filepath.Join(mem.logDir(), "tail.jsonl"))
			if err != nil {
				return nil
			}

			log, err := mem.openLog()
			if err != nil {
				return err
			}

			return log.Repair(ctx)
		}},
		{Name: "taking in the per-session files", Apply: func(ctx context.Context) error {
			return mem.slotFile().Import(ctx, mem.legacySessionFiles())
		}},
		{Name: "removing the per-session files", Apply: func(context.Context) error {
			mem.removeLegacyCache()

			return nil
		}},
	}, nil
}

// legacySessionFiles is every per-session or per-seat file of the cache an
// earlier release kept, with the slot key its state goes under. A waiter, a
// fork marker and a failed-migration marker belong to a process long gone, and
// are only removed.
func (mem *memory) legacySessionFiles() []slots.Legacy {
	var legacy []slots.Legacy

	add := func(pattern string, key func(base string) (string, bool)) {
		paths, _ := filepath.Glob(filepath.Join(mem.cacheDir(), pattern))

		for _, path := range paths {
			named, keep := key(filepath.Base(path))
			if keep {
				legacy = append(legacy, slots.Legacy{Path: path, Key: named})
			}
		}
	}

	trimmed := func(prefix string, suffix string) func(string) (string, bool) {
		return func(base string) (string, bool) {
			name, cut := strings.CutSuffix(base, suffix)

			return prefix + name, cut && name != "" && !strings.Contains(name, ".")
		}
	}

	add("shown/*.json", trimmed("shown/", ".json"))
	add("traps/*.json", trimmed("traps/", ".json"))
	add("ladder/*.do-now.json", trimmed("do-now/", ".do-now.json"))
	add("ladder/*.json", trimmed("ladder/", ".json"))
	add("briefs/*.skipped.json", trimmed("skipped/", ".skipped.json"))
	add("briefs/*.seen.json", trimmed("seen/", ".seen.json"))
	add("briefs/*.json", trimmed("brief/", ".json"))

	return legacy
}

// removeLegacyCache removes the folders of per-session files earlier releases
// kept: after the migration took them in, and on every SessionStart pass, for
// a run stopped between format.json and their removal.
func (mem *memory) removeLegacyCache() {
	for _, name := range legacyCacheDirs {
		_ = os.RemoveAll(filepath.Join(mem.cacheDir(), name))
	}
}
