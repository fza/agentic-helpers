package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/formatsteps"
	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
	"github.com/fza/agentic-helpers/source/hooks/internal/space"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const killStore = "s1"

// killStart is a store the cut test migrates, laid into a fresh root.
type killStart struct {
	name string
	lay  func(t *testing.T, mem *memory)
}

var killStarts = []killStart{{
	name: "a format-1 store with format-5 session files",
	lay: func(t *testing.T, mem *memory) {
		t.Helper()

		test.Format1Store(t, mem.store())
		test.FixtureTreeReplacing(t, "memory/format-5-cache", mem.cacheDir(),
			strings.NewReplacer("SESSION", test.Session, "SEAT", "main"))

		// A session file goes into its slot at the time it was written.
		err := filepath.WalkDir(mem.cacheDir(), func(path string, _ fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			return os.Chtimes(path, cutTime, cutTime)
		})
		if err != nil {
			t.Fatal(err)
		}
	},
}}

var cutTime = time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)

// releaseStarts is every committed release's memory store and its cache.
func releaseStarts(t *testing.T) []killStart {
	t.Helper()

	var starts []killStart

	for _, version := range test.ReleaseVersions(t) {
		starts = append(starts, killStart{name: version, lay: func(t *testing.T, mem *memory) {
			t.Helper()

			laid := t.TempDir()
			test.ReleaseTree(t, version, laid)

			roots := home.Locate(filepath.Join(laid, "home"), func(string) string { return "" })

			stores, err := filepath.Glob(filepath.Join(roots.Stores(), "*"))
			if err != nil || len(stores) != 1 {
				t.Fatalf("the fixture should hold one store, got %v %v", stores, err)
			}

			id := filepath.Base(stores[0])

			for from, to := range map[string]string{roots.Store(id, home.Memory): mem.store(), roots.StoreCache(id, home.Memory): mem.cacheDir()} {
				err = os.CopyFS(to, os.DirFS(from))
				if err != nil {
					t.Fatal(err)
				}
			}
		}})
	}

	return starts
}

// cutMemory is a process's memory of the store in root: each run its own,
// as each process draws its ids afresh.
func cutMemory(root string) *memory {
	return &memory{
		env: Env{
			Roots:  home.Roots{Data: filepath.Join(root, "data"), Cache: filepath.Join(root, "cache")},
			Now:    func() time.Time { return cutTime },
			Random: test.Random(),
		},
		key: killStore,
	}
}

// TestEveryStepCarriesOnFromAnyOperation cuts every step after each of its
// operations, as a kill would, and runs the store's steps again in a new
// process: the store must end as an uninterrupted run leaves it.
func TestEveryStepCarriesOnFromAnyOperation(t *testing.T) {
	ctx := context.Background()

	for _, start := range append(killStarts, releaseStarts(t)...) {
		reference := t.TempDir()
		start.lay(t, cutMemory(reference))

		first, err := formatsteps.Read(cutMemory(reference).store())
		if err != nil {
			t.Fatal(err)
		}

		err = cutMemory(reference).migrate(ctx)
		if err != nil {
			t.Fatalf("%s: the uninterrupted run should succeed: %v", start.name, err)
		}

		for _, name := range legacyCacheDirs {
			if test.Exists(t, filepath.Join(cutMemory(reference).cacheDir(), name)) {
				t.Errorf("%s: the run should remove the session files it took in, %s/ stands", start.name, name)
			}
		}

		want := cutDump(t, reference)
		steps := cutMemory(reference).formatSteps()

		for index, step := range steps {
			if step.From < first {
				continue
			}

			for cut := 0; ; cut++ {
				root := t.TempDir()
				start.lay(t, cutMemory(root))

				listed := cutAt(t, root, steps[:index], index)
				if cut > len(listed) {
					break
				}

				name := fmt.Sprintf("%s, step %d to %d cut after %d of %d operations", start.name, step.From, step.To, cut, len(listed))

				t.Run(name, func(t *testing.T) {
					for _, operation := range listed[:cut] {
						err := operation.Apply(ctx)
						if err != nil {
							t.Fatalf("%s: %v", operation.Name, err)
						}
					}

					err := cutMemory(root).migrate(ctx)
					if err != nil {
						t.Fatalf("the run after the cut should carry the step on: %v", err)
					}

					if got := cutDump(t, root); got != want {
						t.Errorf("a run cut and carried on should end as an uninterrupted run does, got:\n%s\nwant:\n%s", got, want)
					}

					if mismatches := test.LedgerMismatches(t, cutMemory(root).store()); len(mismatches) > 0 {
						t.Errorf("the size ledger should match the files: %s", strings.Join(mismatches, "; "))
					}
				})
			}
		}
	}
}

// cutAt brings the store in root to the format the step at index starts
// from, through the steps before it, and lists that step's operations as a
// new process would.
func cutAt(t *testing.T, root string, before []formatsteps.Step, index int) []formatsteps.Operation {
	t.Helper()

	ctx := context.Background()

	if len(before) > 0 {
		mem := cutMemory(root)
		component := mem.storeComponent()
		component.Steps = mem.formatSteps()[:index]

		err := formatsteps.Run(ctx, component)
		if err != nil {
			t.Fatalf("bringing the store to the step: %v", err)
		}
	}

	listed, err := cutMemory(root).formatSteps()[index].Ops(ctx)
	if err != nil {
		t.Fatalf("listing the step's operations: %v", err)
	}

	return listed
}

// cutDump is what the store and its cache hold, every seat and shelf id
// named by what it stands for and the log as its events.
func cutDump(t *testing.T, root string) string {
	t.Helper()

	mem := cutMemory(root)

	registry, _, err := seat.LoadRegistry(mem.store())
	if err != nil {
		t.Fatalf("loading the registry: %v", err)
	}

	shelves, _, err := seat.LoadShelves(mem.store())
	if err != nil {
		t.Fatalf("loading the shelves: %v", err)
	}

	var pairs []string
	for id, held := range registry.Seats {
		pairs = append(pairs, id, "seat:"+held.Name)
	}

	for id, held := range shelves.Shelves {
		pairs = append(pairs, id, "shelf:"+held.Name)
	}

	names := strings.NewReplacer(pairs...)

	events, err := mem.allEvents(context.Background())
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}

	var lines []string

	for _, event := range events {
		line, _ := json.Marshal(map[string]any{
			"kind": event.Kind, "entry": event.Entry, "time": event.Time, "seat": event.Seat, "target": event.Target,
			"payload": string(event.Payload),
		})
		lines = append(lines, names.Replace(string(line)))
	}

	logDir := filepath.ToSlash(filepath.Join("data", "stores", killStore, home.Memory, "log")) + "/"

	// The size ledger is checked against the files instead, and the clock
	// mark is the latest time the last process read, which a process
	// carrying a step on reads at another point.
	return strings.Join(lines, "\n") + "\n" + test.CanonicalTree(t, root, names, func(relative string) bool {
		return strings.HasPrefix(relative, logDir) || filepath.Base(relative) == space.LedgerName ||
			filepath.Base(relative) == clockMarkName
	})
}

func TestTheStepTableEndsAtTheStoreFormat(t *testing.T) {
	if newest := formatsteps.Newest(cutMemory(t.TempDir()).formatSteps()); newest != storeFormat {
		t.Errorf("the store's step table should end at the format this release writes, ends at %d", newest)
	}
}
