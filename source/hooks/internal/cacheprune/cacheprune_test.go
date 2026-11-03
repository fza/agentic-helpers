package cacheprune_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/cacheprune"
	"github.com/fza/agentic-helpers/source/hooks/internal/filelock"
	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/mcpserver"
	"github.com/fza/agentic-helpers/source/hooks/internal/vectorseg"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const retention = 90 * 24 * time.Hour

var now = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func newRoots(t *testing.T) home.Roots {
	t.Helper()

	root := t.TempDir()

	return home.Roots{Data: filepath.Join(root, "data"), Cache: filepath.Join(root, "cache")}
}

// placeStore writes a store with one row at dir, stamped by format at each
// time given, its files last written at written.
func placeStore(t *testing.T, roots home.Roots, dir string, written time.Time, stamps map[string]time.Time) *vectorseg.Store {
	t.Helper()

	store := vectorseg.Open(roots.Cache, dir)

	err := store.Write([]vectorseg.Row{{ID: "a", Hash: "h", Chunks: [][]float32{{1, 0, 0, 0}}}},
		func(string, string) bool { return true })
	if err != nil {
		t.Fatalf("writing a row: %v", err)
	}

	encoded := map[string]string{}
	for format, at := range stamps {
		encoded[format] = at.Format(time.RFC3339)
	}

	data, err := json.Marshal(encoded)
	if err != nil {
		t.Fatalf("encoding the stamps: %v", err)
	}

	stampPath := filepath.Join(dir, "used.json")
	if stamps == nil {
		err = os.Remove(stampPath)
	} else {
		err = os.WriteFile(stampPath, data, 0o600)
	}

	if err != nil {
		t.Fatalf("placing the stamps: %v", err)
	}

	listed, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("listing the store: %v", err)
	}

	for _, entry := range listed {
		err = os.Chtimes(filepath.Join(dir, entry.Name()), written, written)
		if err != nil {
			t.Fatalf("aging the store: %v", err)
		}
	}

	return store
}

func toolStore(roots home.Roots, fingerprint string) string {
	return filepath.Join(roots.SharedCache(home.Tools), "vectors", fingerprint)
}

func memoryStore(roots home.Roots, id string, fingerprint string) string {
	return filepath.Join(roots.StoreCache(id, home.Memory), fingerprint, "segments")
}

func exists(path string) bool {
	_, err := os.Stat(path)

	return err == nil
}

func TestARunDeletesTheStoresUnusedPastTheRetention(t *testing.T) {
	roots := newRoots(t)
	stale := now.Add(-retention - 24*time.Hour)
	fresh := now.Add(-24 * time.Hour)

	cases := []struct {
		name    string
		dir     string
		removed string
		written time.Time
		stamps  map[string]time.Time
		deleted bool
	}{
		{name: "a tool store stamped long ago goes", dir: toolStore(roots, "aaa"), written: stale,
			stamps: map[string]time.Time{vectorseg.Format: stale}, deleted: true},
		{name: "a tool store stamped yesterday stays", dir: toolStore(roots, "bbb"), written: stale,
			stamps: map[string]time.Time{vectorseg.Format: fresh}},
		{name: "a memory store stamped long ago goes with its folder", dir: memoryStore(roots, "s1", "ccc"),
			removed: filepath.Join(roots.StoreCache("s1", home.Memory), "ccc"), written: stale,
			stamps: map[string]time.Time{vectorseg.Format: stale}, deleted: true},
		{name: "a memory store another format used yesterday stays", dir: memoryStore(roots, "s2", "ddd"),
			written: stale, stamps: map[string]time.Time{vectorseg.Format: stale, "vectorseg/0": fresh}},
		{name: "a store without a stamp written long ago goes", dir: toolStore(roots, "eee"), written: stale, deleted: true},
		{name: "a store without a stamp written yesterday stays", dir: toolStore(roots, "fff"), written: fresh},
	}

	for _, tc := range cases {
		placeStore(t, roots, tc.dir, tc.written, tc.stamps)
	}

	result := cacheprune.Run(context.Background(), roots, retention, cacheprune.Releases{}, now)
	if result.Error != "" {
		t.Fatalf("the run should succeed: %s", result.Error)
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			removed := tc.removed
			if removed == "" {
				removed = tc.dir
			}

			if exists(removed) == tc.deleted {
				t.Errorf("a store should go exactly when no format used it within the retention")
			}

			if slices.Contains(result.Deleted, removed) != tc.deleted {
				t.Errorf("the run should record exactly the stores it deleted")
			}
		})
	}
}

func TestARunSkipsAStoreAnotherProcessHolds(t *testing.T) {
	roots := newRoots(t)
	stale := now.Add(-retention - 24*time.Hour)
	store := placeStore(t, roots, toolStore(roots, "aaa"), stale, map[string]time.Time{vectorseg.Format: stale})

	release, err := store.TryHold()
	if err != nil {
		t.Fatalf("holding the store: %v", err)
	}

	defer release()

	result := cacheprune.Run(context.Background(), roots, retention, cacheprune.Releases{}, now)
	if result.Error != "" || len(result.Deleted) > 0 || !exists(store.Dir()) {
		t.Error("a run should pass over a store being written, quietly")
	}
}

func TestARunExitsWhileAnotherRuns(t *testing.T) {
	roots := newRoots(t)
	stale := now.Add(-retention - 24*time.Hour)
	store := placeStore(t, roots, toolStore(roots, "aaa"), stale, map[string]time.Time{vectorseg.Format: stale})

	err := os.MkdirAll(roots.Data, 0o700)
	if err != nil {
		t.Fatalf("creating the data root: %v", err)
	}

	lock, err := os.OpenFile(filepath.Join(roots.Data, "cache-prune.run.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("opening the run lock: %v", err)
	}

	defer func() { _ = lock.Close() }()

	taken, err := filelock.TryExclusive(lock)
	if err != nil || !taken {
		t.Fatalf("taking the run lock: %v", err)
	}

	result := cacheprune.Run(context.Background(), roots, retention, cacheprune.Releases{}, now)
	if result.Error != "" || len(result.Deleted) > 0 || !exists(store.Dir()) {
		t.Error("a run should exit at once while another holds the run lock")
	}
}

func TestARunRecordsItsOutcome(t *testing.T) {
	roots := newRoots(t)
	stale := now.Add(-retention - 24*time.Hour)
	placeStore(t, roots, toolStore(roots, "aaa"), stale, map[string]time.Time{vectorseg.Format: stale})

	cacheprune.Run(context.Background(), roots, retention, cacheprune.Releases{}, now)

	data, err := os.ReadFile(filepath.Join(roots.Data, cacheprune.StatusName))
	if err != nil {
		t.Fatalf("reading the status: %v", err)
	}

	var status cacheprune.Status

	err = json.Unmarshal(data, &status)
	if err != nil {
		t.Fatalf("decoding the status: %v", err)
	}

	if status.Finished == "" || len(status.Deleted) != 1 || status.Error != "" {
		t.Error("the status should name when the run finished and what it deleted")
	}
}

func TestTheDaysClaimStartsOneRun(t *testing.T) {
	roots := newRoots(t)
	starts := &test.Starts{}
	executable := filepath.Join(t.TempDir(), "bin", "agentic-memory")

	for _, at := range []time.Time{now, now.Add(time.Hour), now.Add(23 * time.Hour), now.Add(25 * time.Hour)} {
		err := cacheprune.Start(context.Background(), roots, starts, executable, at)
		if err != nil {
			t.Fatalf("starting the run: %v", err)
		}
	}

	started := starts.Started()
	if len(started) != 2 {
		t.Fatalf("one run should start per day")
	}

	want := []string{filepath.Join(filepath.Dir(executable), "agentic-helpers"), "prune"}
	if !slices.Equal(started[0], want) {
		t.Error("the run should be agentic-helpers prune beside the calling binary")
	}
}

func TestIdleRunsItsBaseThenClaimsTheRun(t *testing.T) {
	roots := newRoots(t)
	starts := &test.Starts{}
	ran := false

	base := mcpserver.Idle{After: time.Minute, Run: func(context.Context) { ran = true }}

	idle := cacheprune.Idle(base, roots, starts,
		filepath.Join(t.TempDir(), "agentic-tools"), func() time.Time { return now })
	idle.Run(context.Background())

	if !ran || len(starts.Started()) != 1 {
		t.Error("an idle server should run its own idle work and start the day's run")
	}
}

func TestAFailedRunIsWarnedOnce(t *testing.T) {
	roots := newRoots(t)
	stale := now.Add(-retention - 24*time.Hour)
	placeStore(t, roots, toolStore(roots, "aaa"), stale, map[string]time.Time{vectorseg.Format: stale})

	// A link in the store's path is refused by every safe open: an error that
	// is no denial.
	parent := filepath.Dir(toolStore(roots, "aaa"))

	err := os.Rename(parent, parent+"-real")
	if err != nil {
		t.Fatalf("moving the parent: %v", err)
	}

	err = os.Symlink(parent+"-real", parent)
	if err != nil {
		t.Fatalf("linking the parent: %v", err)
	}

	result := cacheprune.Run(context.Background(), roots, retention, cacheprune.Releases{}, now)
	if result.Error == "" {
		t.Fatal("a store that cannot be removed should fail the run")
	}

	first := cacheprune.Warning(roots)
	if !strings.Contains(first, cacheprune.StatusName) {
		t.Error("the first warning should name the status file")
	}

	if cacheprune.Warning(roots) != "" {
		t.Error("a failure should be warned once")
	}

	cacheprune.Run(context.Background(), roots, retention, cacheprune.Releases{}, now.Add(48*time.Hour))

	if cacheprune.Warning(roots) == "" {
		t.Error("a new failure should be warned again")
	}
}

func TestASucceedingRunIsNeverWarned(t *testing.T) {
	roots := newRoots(t)

	cacheprune.Run(context.Background(), roots, retention, cacheprune.Releases{}, now)

	if cacheprune.Warning(roots) != "" {
		t.Error("a run without an error should warn nothing")
	}
}

// The foreground cost a SessionStart and an idle server pay on a day the run
// already started: the claim and the warning check.
func BenchmarkTheForegroundCheck(b *testing.B) {
	root := b.TempDir()
	roots := home.Roots{Data: filepath.Join(root, "data"), Cache: filepath.Join(root, "cache")}
	starts := &test.Starts{}
	executable := filepath.Join(root, "agentic-memory")

	_ = cacheprune.Start(context.Background(), roots, starts, executable, now)

	for b.Loop() {
		_ = cacheprune.Start(context.Background(), roots, starts, executable, now.Add(time.Hour))
		_ = cacheprune.Warning(roots)
	}
}

// A foreground read of a live store, alone and while a run deletes stale
// stores beside it: the run holds nothing the read takes.
func BenchmarkASnapshotWhileARunDeletes(b *testing.B) {
	for _, pruning := range []bool{false, true} {
		name := "alone"
		if pruning {
			name = "while a run deletes"
		}

		b.Run(name, func(b *testing.B) {
			root := b.TempDir()
			roots := home.Roots{Data: filepath.Join(root, "data"), Cache: filepath.Join(root, "cache")}
			live := vectorseg.Open(roots.Cache, toolStore(roots, "live"))

			err := live.Write([]vectorseg.Row{{ID: "a", Hash: "h", Chunks: [][]float32{{1, 0, 0, 0}}}},
				func(string, string) bool { return true })
			if err != nil {
				b.Fatalf("writing the live store: %v", err)
			}

			done := make(chan struct{})
			stopped := make(chan struct{})

			go func() {
				defer close(stopped)

				for round := 0; pruning; round++ {
					select {
					case <-done:
						return
					default:
					}

					for index := range 20 {
						dir := toolStore(roots, fmt.Sprintf("stale-%d-%d", round, index))
						stale := vectorseg.Open(roots.Cache, dir)
						_ = stale.Write([]vectorseg.Row{{ID: "a", Hash: "h", Chunks: [][]float32{{1, 0, 0, 0}}}},
							func(string, string) bool { return true })
						_ = os.Remove(filepath.Join(dir, "used.json"))
						old := now.Add(-2 * retention)

						listed, _ := os.ReadDir(dir)
						for _, entry := range listed {
							_ = os.Chtimes(filepath.Join(dir, entry.Name()), old, old)
						}
					}

					cacheprune.Run(context.Background(), roots, retention, cacheprune.Releases{}, now.Add(time.Duration(round)*25*time.Hour))
				}
			}()

			for b.Loop() {
				snapshot, err := live.Snapshot()
				if err != nil {
					b.Fatalf("reading the live store: %v", err)
				}

				_ = snapshot.Close()
			}

			close(done)
			<-stopped
		})
	}
}

func TestTheChecksOnAQuietDayTakeNoLock(t *testing.T) {
	roots := newRoots(t)
	starts := &test.Starts{}
	executable := filepath.Join(t.TempDir(), "agentic-memory")

	if cacheprune.Warning(roots) != "" || exists(roots.Data) {
		t.Error("a warning check with no run recorded should create nothing")
	}

	err := cacheprune.Start(context.Background(), roots, starts, executable, now)
	if err != nil {
		t.Fatalf("starting the run: %v", err)
	}

	lock := filepath.Join(roots.Data, "cache-prune.lock")

	err = os.Remove(lock)
	if err != nil {
		t.Fatalf("removing the status lock: %v", err)
	}

	err = cacheprune.Start(context.Background(), roots, starts, executable, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("checking the run: %v", err)
	}

	_ = cacheprune.Warning(roots)

	if exists(lock) {
		t.Error("the claim and the warning check should only read the status on a day the run started already")
	}
}

func TestARunDeniedTheCacheIsSkippedAndRetried(t *testing.T) {
	roots := newRoots(t)
	stale := now.Add(-retention - 24*time.Hour)
	store := placeStore(t, roots, toolStore(roots, "aaa"), stale, map[string]time.Time{vectorseg.Format: stale})

	claimed, err := cacheprune.Claim(roots, now)
	if err != nil || !claimed {
		t.Fatalf("claiming the run: %v", err)
	}

	// A sandbox denies the cache: no store below it can be moved or removed.
	parent := filepath.Dir(store.Dir())

	err = os.Chmod(parent, 0o500)
	if err != nil {
		t.Fatalf("denying the stores: %v", err)
	}

	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	result := cacheprune.Run(context.Background(), roots, retention, cacheprune.Releases{}, now)
	if result.Error != "" || result.Denied != 1 || !exists(store.Dir()) {
		t.Fatal("a store the run may not touch should be skipped, not failed")
	}

	if cacheprune.Warning(roots) != "" {
		t.Error("a denied store should warn nothing")
	}

	soon, err := cacheprune.Claim(roots, now.Add(30*time.Minute))
	if err != nil || soon {
		t.Error("a denied run should not be claimed again within the hour")
	}

	later, err := cacheprune.Claim(roots, now.Add(2*time.Hour))
	if err != nil || !later {
		t.Error("a denied run should leave the day's run to a later session")
	}
}

func TestARunRemovesAStoreMovedAsideAndOneACrashLeft(t *testing.T) {
	roots := newRoots(t)
	stale := now.Add(-retention - 24*time.Hour)
	placeStore(t, roots, toolStore(roots, "aaa"), stale, map[string]time.Time{vectorseg.Format: stale})

	left := filepath.Join(filepath.Dir(toolStore(roots, "aaa")), ".pruned-bbb")
	test.WriteFile(t, filepath.Join(left, "seg.seg"), "x")

	result := cacheprune.Run(context.Background(), roots, retention, cacheprune.Releases{}, now)
	if result.Error != "" {
		t.Fatalf("the run should succeed: %s", result.Error)
	}

	listed, err := os.ReadDir(filepath.Dir(toolStore(roots, "aaa")))
	if err != nil {
		t.Fatalf("listing the stores: %v", err)
	}

	if len(listed) != 0 || slices.Contains(result.Deleted, left) {
		t.Error("a run should leave no store it moved aside, remove one a crash left, and never count it as a store")
	}
}
