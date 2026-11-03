package memory_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func format1Store(t *testing.T, h *harness) {
	t.Helper()

	test.Format1Store(t, h.store())
}

func storeEvents(t *testing.T, h *harness) []memorylog.Row {
	t.Helper()

	log, err := memorylog.Open(h.store(), filepath.Join(h.store(), "log"))
	if err != nil {
		t.Fatalf("opening the log: %v", err)
	}

	snapshot, err := log.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}

	defer func() { _ = snapshot.Close() }()

	return snapshot.Rows()
}

func TestAClaimMintsASeatID(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	if format, _ := os.ReadFile(filepath.Join(h.store(), "format.json")); !strings.Contains(string(format), `"format":10`) {
		t.Errorf("the claim creating a store should write it at the current format, got %s", format)
	}

	h.file("Gate reads Bash only today", "finding")

	if test.Exists(t, filepath.Join(h.store(), "backup")) {
		t.Error("a store created at the current format should never migrate")
	}

	id := seatID(t, h.store(), "main")
	if !regexp.MustCompile(`^s-[0-9a-z]{6}$`).MatchString(id) || !test.Exists(t, filepath.Join(h.store(), "seats", id+".json")) {
		t.Errorf("a first claim should mint an s- id of 6 letters or digits and file the claim under it, got %s", id)
	}

	for _, row := range storeEvents(t, h) {
		if row.Seat != id {
			t.Errorf("every event should carry the seat's id, got %q", row.Seat)
		}
	}

	if format, _ := os.ReadFile(filepath.Join(h.store(), "format.json")); !strings.Contains(string(format), `"format":10`) {
		t.Errorf("a fresh store should be written at the current format, got %s", format)
	}
}

func TestRename(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	entry := h.file("Gate reads Bash only today", "finding")
	id := seatID(t, h.store(), "main")

	if out := h.must("", "rename", "haupt"); !strings.Contains(out, "main is now haupt") {
		t.Errorf("rename should say the seat's old and new name, got %q", out)
	}

	if seatID(t, h.store(), "haupt") != id || !test.Exists(t, filepath.Join(h.store(), "seats", id+".json")) {
		t.Error("a rename should keep the seat's id and its claim file")
	}

	if out := h.must("", "search", "--term", "Bash"); !strings.Contains(out, entry) || !strings.Contains(out, "[haupt, ") {
		t.Errorf("the seat's entries should stay its own under the new name, got:\n%s", out)
	}

	other := newHarnessSharing(h)
	other.must("", "claim", "drive")

	for _, tc := range []struct {
		name    string
		harness *harness
		args    []string
		says    string
	}{
		{name: "a name in use", harness: other, args: []string{"rename", "haupt"}, says: "haupt already names another seat"},
		{name: "an invalid name", harness: h, args: []string{"rename", "two words"}, says: "a seat name is a letter"},
		{name: "an id's shape", harness: h, args: []string{"rename", "s-abc123"}, says: "has the shape of a seat id"},
	} {
		got := tc.harness.run("", tc.args...)
		if got.code != 1 || !strings.Contains(got.err, tc.says) {
			t.Errorf("%s should be refused, got %d %q", tc.name, got.code, got.err)
		}
	}

	h.must("", "release")
	other.must("", "release")

	if got := other.run("", "rename", "x"); got.code != 1 || !strings.Contains(got.err, "holds no seat") {
		t.Errorf("a session holding no seat should be refused, got %d %q", got.code, got.err)
	}

	h.must("", "claim", "haupt")

	if seatID(t, h.store(), "haupt") != id {
		t.Error("claiming the new name should reach the same seat")
	}

	h.must("", "release")
	h.must("", "claim", "main")

	if seatID(t, h.store(), "main") == id {
		t.Error("the old name should be free, and claiming it mint a new seat")
	}
}

func TestAFormat1StoreMigrates(t *testing.T) {
	h := newHarness(t)
	format1Store(t, h)
	original := test.Tree(t, h.store())

	h.must("", "topics")

	if format, _ := os.ReadFile(filepath.Join(h.store(), "format.json")); !strings.Contains(string(format), `"format":10`) {
		t.Fatalf("the store should be at the current format after one call, got %s", format)
	}

	main, drive := seatID(t, h.store(), "main"), seatID(t, h.store(), "drive")

	for _, row := range storeEvents(t, h) {
		if row.Seat != main && row.Seat != drive {
			t.Errorf("every event, sealed or not, should carry a seat id, got %q", row.Seat)
		}
	}

	for _, file := range []string{main + ".json", main + ".last.json", main + ".slice", drive + ".last.json"} {
		if !test.Exists(t, filepath.Join(h.store(), "seats", file)) {
			t.Errorf("the seat file %s should exist under the seat's id", file)
		}
	}

	if test.Exists(t, filepath.Join(h.store(), "seats", "main.json")) {
		t.Error("no seat file should remain under a seat's name")
	}

	manifest, _ := os.ReadFile(filepath.Join(h.store(), "log", "manifest.json"))
	if strings.Contains(string(manifest), `"format"`) {
		t.Errorf("the manifest should carry no format any more, got %s", manifest)
	}

	backups, _ := filepath.Glob(filepath.Join(h.store(), "backup", "format-1-*"))
	if len(backups) != 1 {
		t.Fatalf("one backup should hold the store as it was, got %v", backups)
	}

	if backed := test.Tree(t, backups[0]); backed != original {
		t.Errorf("the backup should hold the original log and seat files byte for byte, got:\n%s\nfrom:\n%s", backed, original)
	}

	h.must("", "claim", "main")
	h.file("Filed after the migration", "a new finding")

	if out := h.must("", "search", "--term", "Gate reads|after the migration"); !strings.Contains(out, "m-aaaaaa") ||
		!strings.Contains(out, "[main, ") {
		t.Errorf("the migrated seat should keep its entries under its name, got:\n%s", out)
	}

	tail := []byte(test.Dumped(t, filepath.Join(h.store(), "log", "tail.jnl")))
	if !regexp.MustCompile(`"time":"\d{4}-\d{2}-\d{2}T[^"]+Z"`).Match(tail) || strings.Contains(string(tail), `"seat":"main"`) {
		t.Errorf("rows after the migration should carry RFC 3339 times and seat ids, got %s", tail)
	}
}

// A failure in a step's first operation, its backup, leaves the store as it
// was.
func TestAFailedMigrationLeavesTheStoreAsItWas(t *testing.T) {
	h := newHarness(t)
	format1Store(t, h)

	before := map[string]string{}
	for _, part := range []string{"log", "seats"} {
		before[part] = test.Tree(t, filepath.Join(h.store(), part))
	}

	blocker := filepath.Join(h.store(), "backup")
	test.WriteFile(t, blocker, "keeps the backup from being written")

	first := h.run("", "topics")
	second := h.run("", "topics")

	if !strings.Contains(first.err, "migrating the store failed: moving this store from format 1 to 2: backing up the store: ") ||
		!strings.Contains(first.err, "; the next call carries it on") {
		t.Errorf("a failed migration should say so, got %q", first.err)
	}

	if strings.Contains(second.err, "migrating the store failed") {
		t.Errorf("the failure should be said once per session, got it again: %q", second.err)
	}

	for part, tree := range before {
		if after := test.Tree(t, filepath.Join(h.store(), part)); after != tree {
			t.Errorf("a failed migration should leave the store's %s byte-identical, got:\n%s\nbefore:\n%s", part, after, tree)
		}
	}

	if test.Exists(t, filepath.Join(h.store(), "seats.json")) || test.Exists(t, filepath.Join(h.store(), "format.json")) ||
		test.Exists(t, filepath.Join(h.store(), "log.migrating")) {
		t.Error("a failed migration should leave no registry, format or half-built log behind")
	}

	if out := h.must("", "search", "--all-seats", "--term", "finding"); !strings.Contains(out, "m-aaaaaa") {
		t.Errorf("the store should keep working at its earlier format, got:\n%s", out)
	}

	err := os.RemoveAll(blocker)
	if err != nil {
		t.Fatal(err)
	}

	h.must("", "topics")

	if !test.Exists(t, filepath.Join(h.store(), "format.json")) {
		t.Error("the next call after the fault clears should migrate")
	}
}

// A run killed between setting the old log aside and writing format.json
// leaves `log.format-<n>` behind; the next run carries the step on.
func TestASetAsideLogNeverBlocksTheNextRun(t *testing.T) {
	for _, tc := range []struct {
		name     string
		setAside string
		store    func(t *testing.T, h *harness)
	}{
		{name: "format 1", setAside: "log.format-1", store: format1Store},
		{name: "format 3", setAside: "log.format-3", store: func(t *testing.T, h *harness) { format3Store(t, h) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			tc.store(t, h)

			err := os.CopyFS(filepath.Join(h.store(), tc.setAside), os.DirFS(filepath.Join(h.store(), "log")))
			if err != nil {
				t.Fatal(err)
			}

			got := h.run("", "topics")
			if got.code != 0 || strings.Contains(got.err, "migrating the store failed") {
				t.Errorf("a set-aside log left by a killed run should not block the next one, got %d %q", got.code, got.err)
			}

			if format, _ := os.ReadFile(filepath.Join(h.store(), "format.json")); !strings.Contains(string(format), `"format":10`) {
				t.Errorf("the next run should bring the store to the current format, got %s", format)
			}

			if test.Exists(t, filepath.Join(h.store(), tc.setAside)) {
				t.Error("the set-aside log should be gone once the step is done")
			}

			if out := h.must("", "search", "--all-seats", "--term", "finding"); !strings.Contains(out, "m-") {
				t.Errorf("the store should keep its entries, got:\n%s", out)
			}
		})
	}
}

func TestTwoCallsMigrateOnce(t *testing.T) {
	h := newHarness(t)
	format1Store(t, h)

	var group sync.WaitGroup

	for range 2 {
		group.Go(func() { h.run("", "topics") })
	}

	group.Wait()

	backups, _ := filepath.Glob(filepath.Join(h.store(), "backup", "format-1-*"))
	if len(backups) != 1 {
		t.Errorf("two calls on one unmigrated store should migrate it once, got %d backups", len(backups))
	}

	registry, _ := os.ReadFile(filepath.Join(h.store(), "seats.json"))
	if strings.Count(string(registry), `"name"`) != 2 {
		t.Errorf("each seat should be minted once, got %s", registry)
	}
}

func TestADevelopmentBuildFromAnotherProjectsPathIsRefusedAnOlderStore(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	test.WriteFile(t, filepath.Join(h.store(), "format.json"), `{"format": 7}`)

	before := test.Tree(t, h.store())
	h.version = "dev"

	got := h.run("", "search", "--term", "Gate")
	if got.code != 1 || !strings.Contains(got.err, "agentic-memory: this build writes store format 10, which a release may not read") {
		t.Errorf("the build should be refused saying what it would write, got %d %q", got.code, got.err)
	}

	if hook := h.run(hookPayload("startup", "", ""), "session-start"); hook.code != 0 || hook.out != "" {
		t.Errorf("a hook of a held build should stay silent, got %+v", hook)
	}

	if after := test.Tree(t, h.store()); after != before {
		t.Error("the store should stay as it is")
	}
}

func TestANewerStoreIsRefused(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("Gate reads Bash only today", "finding")
	test.WriteFile(t, filepath.Join(h.store(), "format.json"), `{"format": 11}`)

	before := test.Tree(t, h.store())

	got := h.run("", "search", "--term", "Gate")
	if got.code != 1 || !strings.Contains(got.err, "agentic-memory: this store has format 11; this release reads up to 10") {
		t.Errorf("a store of a newer format should be refused naming both, got %d %q", got.code, got.err)
	}

	if hook := h.run(hookPayload("startup", "", ""), "session-start"); hook.code != 0 || hook.out != "" {
		t.Errorf("a hook on a newer store should stay silent, got %+v", hook)
	}

	if after := test.Tree(t, h.store()); after != before {
		t.Error("nothing should be written to a store of a newer format")
	}
}

func TestHolderKeepsItsShapeAcrossTheMigration(t *testing.T) {
	h := newHarness(t)
	format1Store(t, h)

	var view map[string]any

	err := json.Unmarshal([]byte(h.must("", "holder", "main", "--json")), &view)
	if err != nil {
		t.Fatalf("holder --json should answer JSON: %v", err)
	}

	keys := make([]string, 0, len(view))
	for key := range view {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	if !slices.Equal(keys, []string{"alive", "claimed", "pid", "seat", "session_id", "started"}) || view["seat"] != "main" ||
		view["alive"] != true {
		t.Errorf("holder --json should keep its field names and name the seat, got %v", view)
	}
}

func TestAMigrationKeepsSeatsTheRegistryAlreadyHolds(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("Gate reads Bash only today", "finding")
	id := seatID(t, h.store(), "main")

	err := os.Remove(filepath.Join(h.store(), "format.json"))
	if err != nil {
		t.Fatal(err)
	}

	h.must("", "topics")

	registry, _ := os.ReadFile(filepath.Join(h.store(), "seats.json"))
	if strings.Count(string(registry), `"name"`) != 1 || seatID(t, h.store(), "main") != id {
		t.Errorf("a migration should mint nothing for a seat the registry holds or an id an event carries, got %s", registry)
	}
}

// format3Store is a store of the format before entry kinds: the seat main
// claimed, and entries filed with a retention level and no kind.
func format3Store(t *testing.T, h *harness) (string, string, string) {
	t.Helper()

	h.must("", "claim", "main")
	main := seatID(t, h.store(), "main")
	at := h.clock.Now()

	log, err := memorylog.Open(h.store(), filepath.Join(h.store(), "log"))
	if err != nil {
		t.Fatal(err)
	}

	err = log.Append(context.Background(),
		memorylog.Event{Kind: memorylog.KindNew, Entry: "m-rule01", Time: at, Seat: main,
			Payload: []byte(`{"title":"Owner allows commits on main","body":"b","topics":["grants"],"retention":"standing","from":"user"}`)},
		memorylog.Event{Kind: memorylog.KindNew, Entry: "m-long01", Time: at.Add(time.Second), Seat: main,
			Payload: []byte(`{"title":"Gate finding kept long","body":"b","topics":["gate"],"retention":"long","from":"agent"}`)},
		memorylog.Event{Kind: memorylog.KindNew, Entry: "m-norm01", Time: at.Add(2 * time.Second), Seat: main,
			Payload: []byte(`{"title":"Gate finding kept normal","body":"b","topics":["gate"],"retention":"normal","from":"agent"}`)},
	)
	if err != nil {
		t.Fatal(err)
	}

	test.WriteFile(t, filepath.Join(h.store(), "format.json"), `{"format":3}`)

	return "m-rule01", "m-long01", "m-norm01"
}

func TestAFormat3StoreMigratesToKinds(t *testing.T) {
	h := newHarness(t)
	rule, long, normal := format3Store(t, h)
	original := test.Tree(t, filepath.Join(h.store(), "log"))

	out := test.Unscored(h.must("", "search", "--term", "Owner|Gate finding"))

	if format, _ := os.ReadFile(filepath.Join(h.store(), "format.json")); !strings.Contains(string(format), `"format":10`) {
		t.Fatalf("the store should be at the current format after one call, got %s", format)
	}

	for _, want := range []string{
		h.label(rule) + " Owner allows commits on main  [main, rule, from user, ",
		h.label(long) + " Gate finding kept long  [main, note, retain long, from agent, ",
		h.label(normal) + " Gate finding kept normal  [main, note, from agent, ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("a standing entry should read as a rule and every other as a note keeping its retention, missing %q in:\n%s",
				want, out)
		}
	}

	backups, _ := filepath.Glob(filepath.Join(h.store(), "backup", "format-3-*"))
	if len(backups) != 1 {
		t.Fatalf("one backup should hold the log as it was, got %v", backups)
	}

	if backed := test.Tree(t, filepath.Join(backups[0], "log")); backed != original {
		t.Errorf("the backup should hold the original log byte for byte, got:\n%s\nfrom:\n%s", backed, original)
	}

	if brief := h.must("", "brief"); strings.Index(brief, rule) > strings.Index(brief, "notes:") {
		t.Errorf("the migrated rule should stand in the rules section:\n%s", brief)
	}
}

func TestTwoCallsMigrateAFormat3StoreOnce(t *testing.T) {
	h := newHarness(t)
	format3Store(t, h)

	var group sync.WaitGroup

	for range 2 {
		group.Go(func() { h.run("", "topics") })
	}

	group.Wait()

	backups, _ := filepath.Glob(filepath.Join(h.store(), "backup", "format-3-*"))
	if len(backups) != 1 {
		t.Errorf("two calls on one unmigrated store should migrate it once, under the store lock, got %d backups", len(backups))
	}

	if out := h.must("", "search", "--term", "Gate finding"); strings.Count(out, "[main, note, ") != 2 {
		t.Errorf("every entry should be migrated once:\n%s", out)
	}
}

// A failure after a step changed the store leaves it at the step's format,
// working, and the next call carries the step on from its first operation.
func TestAFailedKindMigrationIsCarriedOnByTheNextCall(t *testing.T) {
	h := newHarness(t)
	format3Store(t, h)
	before := test.Tree(t, filepath.Join(h.store(), "log"))

	locked := filepath.Join(h.store(), "log.migrating", "locked")
	test.WriteFile(t, filepath.Join(locked, "keeps the half-built log from being cleared"), "")

	err := os.Chmod(locked, 0o500)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	got := h.run("", "topics")
	if !strings.Contains(got.err, "migrating the store failed: moving this store from format 3 to 4: writing the migrated log: ") {
		t.Errorf("a failed migration should name the step and the operation, got %q", got.err)
	}

	if after := test.Tree(t, filepath.Join(h.store(), "log")); after != before {
		t.Errorf("a failed migration should leave the live log byte-identical, got:\n%s\nbefore:\n%s", after, before)
	}

	if format, _ := os.ReadFile(filepath.Join(h.store(), "format.json")); !strings.Contains(string(format), `"format":3`) {
		t.Errorf("a failed migration should leave the store at its format, got %s", format)
	}

	err = os.Chmod(locked, 0o700)
	if err != nil {
		t.Fatal(err)
	}

	if out := h.must("", "search", "--term", "Gate finding"); strings.Count(out, "[main, note, ") != 2 {
		t.Errorf("the next call should carry the step on and give every entry its kind:\n%s", out)
	}

	if backups, _ := filepath.Glob(filepath.Join(h.store(), "backup", "format-3-*")); len(backups) != 1 {
		t.Errorf("the step carried on should keep the backup the failed run made and make no second one, got %v", backups)
	}

	if test.Exists(t, filepath.Join(h.store(), "log.migrating")) || test.Exists(t, filepath.Join(h.store(), "log.format-3")) {
		t.Error("the finished step should leave no half-built or set-aside log")
	}
}
