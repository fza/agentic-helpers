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

// format1Store lays a store of the earlier format into the harness: a segment
// and a tail naming seats, the tail times in Unix nanoseconds, a manifest at
// format 1, and claim, last-claim and slice files named for their seats.
func format1Store(t *testing.T, h *harness) {
	t.Helper()

	sealed := []memorylog.Event{{
		Kind: memorylog.KindNew, Entry: "m-cccccc", Time: time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC), Seat: "drive",
		Payload: []byte(`{"title":"An older drive finding sealed","body":"sealed in a segment","topics":["release"],"retention":"normal","from":"agent"}`),
	}}

	err := memorylog.Build(context.Background(), filepath.Join(h.store(), "log"), sealed)
	if err != nil {
		t.Fatalf("building the segment: %v", err)
	}

	manifestPath := filepath.Join(h.store(), "log", "manifest.json")

	var held map[string]any

	raw, err := os.ReadFile(manifestPath)
	if err == nil {
		err = json.Unmarshal(raw, &held)
	}

	if err != nil {
		t.Fatalf("reading the built manifest: %v", err)
	}

	held["format"] = 1
	raw, _ = json.Marshal(held)
	test.WriteFile(t, manifestPath, string(raw))

	test.FixtureTree(t, "memory/format-1", h.store())
}

func storeEvents(t *testing.T, h *harness) []memorylog.Row {
	t.Helper()

	log, err := memorylog.Open(filepath.Join(h.store(), "log"))
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

	if format, _ := os.ReadFile(filepath.Join(h.store(), "format.json")); !strings.Contains(string(format), `"format":2`) {
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

	if format, _ := os.ReadFile(filepath.Join(h.store(), "format.json")); !strings.Contains(string(format), `"format":2`) {
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

	if format, _ := os.ReadFile(filepath.Join(h.store(), "format.json")); !strings.Contains(string(format), `"format":2`) {
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

	tail, _ := os.ReadFile(filepath.Join(h.store(), "log", "tail.jsonl"))
	if !regexp.MustCompile(`"time":"\d{4}-\d{2}-\d{2}T[^"]+Z"`).Match(tail) || strings.Contains(string(tail), `"seat":"main"`) {
		t.Errorf("rows after the migration should carry RFC 3339 times and seat ids, got %s", tail)
	}
}

func TestAFailedMigrationLeavesTheStoreAsItWas(t *testing.T) {
	h := newHarness(t)
	format1Store(t, h)

	before := map[string]string{}
	for _, part := range []string{"log", "seats"} {
		before[part] = test.Tree(t, filepath.Join(h.store(), part))
	}

	blocker := filepath.Join(h.store(), "format.json.tmp")
	test.WriteFile(t, filepath.Join(blocker, "keeps the format from being written"), "")

	first := h.run("", "topics")
	second := h.run("", "topics")

	if !strings.Contains(first.err, "migrating the store failed:") || !strings.Contains(first.err, "the store is unchanged and the next call tries again") {
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

func TestANewerStoreIsRefused(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("Gate reads Bash only today", "finding")
	test.WriteFile(t, filepath.Join(h.store(), "format.json"), `{"format": 3}`)

	before := test.Tree(t, h.store())

	got := h.run("", "search", "--term", "Gate")
	if got.code != 1 || !strings.Contains(got.err, "agentic-memory: this store has format 3; this release reads up to 2") {
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
