package memory_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/space"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

// format5Store turns the harness's store into one an earlier release left:
// format 5, its log's tail as JSON lines, a JSON file per session and purpose
// in the cache, a JSON file per session in the tool library and the prompt
// vectors, and a context reading of a session that crashed.
func format5Store(t *testing.T, h *harness) (string, string) {
	t.Helper()

	h.must("", "claim", "main")
	h.file("Gate reads Bash only today", "finding")

	seatKey := seatID(t, h.store(), "main")
	tail := filepath.Join(h.store(), "log", "tail.jnl")

	var legacy strings.Builder

	for _, line := range strings.Split(strings.TrimSpace(test.Dumped(t, tail)), "\n") {
		var event map[string]any

		err := json.Unmarshal([]byte(line), &event)
		if err != nil {
			t.Fatalf("reading the tail: %v", err)
		}

		row, _ := json.Marshal(map[string]any{
			"kind": event["event"], "entry": event["entry"], "time": event["time"], "seat": event["seat"],
			"target": event["target"], "payload": event["payload"],
		})
		legacy.WriteString(string(row) + "\n")
	}

	test.WriteFile(t, filepath.Join(h.store(), "log", "tail.jsonl"), legacy.String())
	test.WriteFile(t, filepath.Join(h.store(), "format.json"), `{"format":5}`+"\n")

	for _, gone := range []string{tail, filepath.Join(h.cacheDir(), "sessions.slots"), filepath.Join(h.store(), space.LedgerName)} {
		err := os.Remove(gone)
		if err != nil {
			t.Fatalf("removing %s: %v", filepath.Base(gone), err)
		}
	}

	test.FixtureTreeReplacing(t, "memory/format-5-cache", h.cacheDir(), strings.NewReplacer("SESSION", h.session, "SEAT", seatKey))

	library := h.roots().Store(filepath.Base(filepath.Dir(h.store())), home.Tools)
	test.FixtureTree(t, "tools/fdbox-library", library)
	test.WriteFile(t, filepath.Join(library, "sessions", h.session+".json"), test.Fixture(t, "tools/legacy-sessions/session.json"))

	test.WriteFile(t, filepath.Join(h.roots().Cache, "prompt-vectors", h.session+".json"),
		test.Fixture(t, "promptvector/legacy-slot.json"))

	h.sensor(40)

	crashed := h.sensorPath(test.OtherSession)
	test.WriteFile(t, crashed, `{"used_percentage":12,"size":200000,"at":1790000000}`)

	past := h.clock.Now().Add(-30 * 24 * time.Hour)

	err := os.Chtimes(crashed, past, past)
	if err != nil {
		t.Fatalf("dating the crashed session's reading: %v", err)
	}

	return seatKey, library
}

func TestAnEarlierStoreMovesToTheBinaryFormats(t *testing.T) {
	h := newHarness(t)
	seatKey, library := format5Store(t, h)

	h.must(hookPayload("startup", "", ""), "session-start")
	h.must(hookPayload("", "", "what does the gate read"), "prompt")

	format, _ := os.ReadFile(filepath.Join(h.store(), "format.json"))
	if !strings.Contains(string(format), `"format":7`) {
		t.Errorf("the store should move to the current format, got %s", format)
	}

	if out := h.must("", "search", "--term", "Gate reads"); !strings.Contains(out, "Gate reads Bash only today") {
		t.Errorf("the events of the legacy tail should still read, got:\n%s", out)
	}

	for key, want := range map[string]string{
		"ladder/" + h.session: `"nudged_at":"2026-10-20T09:00:00Z"`,
		"do-now/" + seatKey:   `"stops":{"claim":1}`,
		"skipped/" + seatKey:  `"m-cccccc"`,
		"traps/" + seatKey:    `"traps"`,
	} {
		if !strings.Contains(h.slot(key), want) {
			t.Errorf("the slot %s should hold what its file held, got %q", key, h.slot(key))
		}
	}

	legacyDirs := []string{"shown", "ladder", "graph-seen", "forks", "waiters", "migration-failed", "traps", "briefs", "seen"}

	var left []string

	for _, root := range []string{h.roots().Data, h.roots().Cache} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			rel, _ := filepath.Rel(root, path)

			switch {
			case entry.IsDir() && slices.Contains(legacyDirs, entry.Name()):
				left = append(left, rel)
			case entry.IsDir():
			case strings.HasSuffix(entry.Name(), ".jsonl"):
				left = append(left, rel)
			case path == h.sensorPath(h.session):
			case strings.Contains(rel, test.Session) || strings.Contains(rel, test.OtherSession):
				left = append(left, rel)
			case filepath.Base(filepath.Dir(path)) == "sessions" && strings.HasSuffix(path, ".json"):
				left = append(left, rel)
			}

			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}

	if len(left) > 0 {
		t.Errorf("no per-session file but the running session's context reading should be left, got %v", left)
	}

	if !exists(h.sensorPath(h.session)) {
		t.Error("the running session's context reading should stay")
	}

	for _, dir := range []string{h.store(), library} {
		mismatches := test.LedgerMismatches(t, dir)
		if len(mismatches) > 0 {
			t.Errorf("the moved store's ledger should match its files: %s", strings.Join(mismatches, "; "))
		}
	}
}

func TestTheLedgersMatchTheFilesAfterEveryWriteAndPurge(t *testing.T) {
	h, other := rowShelf(t)
	attachments := h.roots().Shared("attachments")

	steps := []struct {
		name string
		run  func()
	}{
		{name: "a write with an attachment", run: func() {
			h.file("Row finding with a log", "the row's own", "--attach", h.sizedFile("row.log", 'r', 4<<10))
		}},
		{name: "a retire", run: func() { h.must("", "retire", h.file("Short-lived row finding", "brief"), "--because", "done") }},
		{name: "a purge", run: func() {
			h.must("", "lifetime", "30d")
			h.must("", "release")
			other.must("", "release")
			h.must("", "drop", "row-7ah")
		}},
		{name: "the SessionStart pass", run: func() { h.must(hookPayload("startup", "", ""), "session-start") }},
	}

	for _, step := range steps {
		step.run()

		for _, dir := range []string{h.store(), attachments} {
			mismatches := test.LedgerMismatches(t, dir)
			if len(mismatches) > 0 {
				t.Errorf("the ledger of %s should match its files after %s: %s", filepath.Base(dir), step.name,
					strings.Join(mismatches, "; "))
			}
		}
	}
}

func TestDumpOfTheTailPrintsTheJournal(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("Gate reads Bash only today", "finding")

	if out := h.must("", "dump", "--tail"); !strings.Contains(out, "Gate reads Bash only today") {
		t.Errorf("the tail's dump should print the events of the log's journal, got:\n%s", out)
	}
}
