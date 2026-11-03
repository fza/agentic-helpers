package memory_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const scratchSession = "0b1c2d3e-4f50-4617-8899-aabbccddeeff"

type scratchEntries struct {
	live, retired, superseded, todo, ending, trap string
}

// unrewrittenStore files entries, a todo and a state quoting old scratch
// paths, with the file one anchors moved as the scratch move moves it, and
// takes the marker away: the store as an earlier release left it. The todo
// ending on that file and the trap are appended as rows, since `new` stores
// a declared `.tmp/` path without its leading dot.
func unrewrittenStore(t *testing.T, h *harness) scratchEntries {
	t.Helper()

	h.must("", "claim", "main")

	notes := filepath.Join(h.repo, ".tmp", "claude", "main", "notes.md")
	test.WriteFile(t, notes, "where the nudges stand\n")

	var held scratchEntries

	held.live = h.file("Nudges sit in .tmp/claude/main/notes.md",
		"probe in .tmp/claude/"+scratchSession+"/probe.txt and the list in .tmp/claude/todos/list.md")
	held.retired = h.file("Old probe output in .tmp/claude/probe/out.txt", "copied from .tmp/claude/main/a.txt")
	h.must("", "retire", held.retired, "--because", "done")
	held.superseded = h.file("Plan kept in .tmp/claude/main/plan.md", "the plan")
	h.must("", "supersede", held.superseded, "--because", "replaced")
	h.must("", "link", held.live, held.retired)
	held.todo, _ = h.todo("Port the waiter", "run .tmp/claude/main/check.sh")
	h.addLine("next: read .tmp/claude/main/plan.md")

	held.ending, held.trap = "m-ending01", "m-trap0001"
	hash := sha256.Sum256([]byte("where the nudges stand\n"))
	notesHash := hex.EncodeToString(hash[:])[:16]
	at := h.clock.Now()
	main := seatID(t, h.store(), "main")

	log, err := memorylog.Open(h.store(), filepath.Join(h.store(), "log"))
	if err != nil {
		t.Fatal(err)
	}

	err = log.Append(context.Background(),
		memorylog.Event{Kind: memorylog.KindNew, Entry: held.ending, Time: at, Seat: main, Payload: []byte(
			`{"title":"Nudge todo ending with its notes","body":"b","topics":["gate"],"kind":"todo","from":"agent",` +
				`"anchors":[{"path":".tmp/claude/main/notes.md","hash":"` + notesHash + `"}],` +
				`"about":[".tmp/claude/main/notes.md"],` +
				`"until":["gone:.tmp/claude/main/notes.md","changed:.tmp/claude/main/notes.md"],` +
				`"until_hashes":{".tmp/claude/main/notes.md":"` + notesHash + `"}}`)},
		memorylog.Event{Kind: memorylog.KindNew, Entry: held.trap, Time: at, Seat: main, Payload: []byte(
			`{"title":"Nudge trap on scratch writes","body":"b","topics":["gate"],"kind":"trap","from":"agent",` +
				`"when":["path:.tmp/claude/main/**","path:.tmp/claude/*","path:.tmp/claude/ma*"]}`)},
	)
	if err != nil {
		t.Fatalf("appending the rows naming paths: %v", err)
	}

	moved := filepath.Join(h.repo, ".tmp", "seats", "main", "notes.md")
	test.WriteFile(t, moved, "where the nudges stand\n")

	err = os.Remove(notes)
	if err == nil {
		err = os.Remove(filepath.Join(h.store(), "scratch-paths.json"))
	}

	if err != nil {
		t.Fatalf("laying out the earlier store: %v", err)
	}

	return held
}

// storePayloads is every event's payload of the store's log, in order.
func storePayloads(t *testing.T, h *harness) []string {
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

	payloads := make([]string, 0, len(snapshot.Rows()))

	for index := range snapshot.Rows() {
		payload, err := snapshot.Payload(index)
		if err != nil {
			t.Fatalf("reading a payload: %v", err)
		}

		payloads = append(payloads, string(payload))
	}

	return payloads
}

func scratchBackups(t *testing.T, h *harness) []string {
	t.Helper()

	backups, err := filepath.Glob(filepath.Join(h.store(), "backup", "scratch-paths-*"))
	if err != nil {
		t.Fatal(err)
	}

	return backups
}

func TestTheScratchPassRewritesEveryPathTheStoreQuotes(t *testing.T) {
	h := newHarness(t)
	entries := unrewrittenStore(t, h)

	rowsBefore := storeEvents(t, h)
	heatBefore := h.heat()
	logBefore := test.Tree(t, filepath.Join(h.store(), "log"))

	h.must("", "topics")

	joined := strings.Join(storePayloads(t, h), "\n")
	for _, want := range []string{
		".tmp/seats/main/notes.md", ".tmp/seats/" + scratchSession + "/probe.txt", ".tmp/keep/todos/list.md",
		".tmp/keep/probe/out.txt", ".tmp/seats/main/a.txt", ".tmp/seats/main/plan.md", ".tmp/seats/main/check.sh",
		`"about":[".tmp/seats/main/notes.md"]`, `"gone:.tmp/seats/main/notes.md"`, `"changed:.tmp/seats/main/notes.md"`,
		`"until_hashes":{".tmp/seats/main/notes.md":`, `"path:.tmp/seats/main/**"`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("every title, body, todo item and state should carry the mapped path, missing %q", want)
		}
	}

	if strings.Count(joined, ".tmp/claude/") != 2 || !strings.Contains(joined, `"path:.tmp/claude/*"`) ||
		!strings.Contains(joined, `"path:.tmp/claude/ma*"`) {
		t.Errorf("no old scratch path should stay in the log but a glob before or within the name:\n%s", joined)
	}

	rowsAfter := storeEvents(t, h)
	if !slices.Equal(rowsBefore, rowsAfter) {
		t.Errorf("ids, links, seats, times and kinds should stay, got:\n%v\nfrom:\n%v", rowsAfter, rowsBefore)
	}

	if h.heat() != heatBefore {
		t.Error("the heat should stay as it was")
	}

	if mismatches := test.LedgerMismatches(t, h.store()); len(mismatches) > 0 {
		t.Errorf("the size ledger should match the store's files after the pass: %v", mismatches)
	}

	backups := scratchBackups(t, h)
	if len(backups) != 1 {
		t.Fatalf("one backup should hold the log as it was, got %v", backups)
	}

	if backed := test.Tree(t, filepath.Join(backups[0], "log")); backed != logBefore {
		t.Errorf("the backup should hold the log byte for byte as it was, got:\n%s\nfrom:\n%s", backed, logBefore)
	}

	if format, _ := os.ReadFile(filepath.Join(h.store(), "format.json")); strings.TrimSpace(string(format)) != `{"format":9}` {
		t.Errorf("the store should stay at its format, got %s", format)
	}

	if !test.Exists(t, filepath.Join(h.store(), "scratch-paths.json")) {
		t.Error("the pass should leave its marker")
	}

	for _, id := range []string{entries.live, entries.retired, entries.superseded, entries.todo} {
		if out := h.must("", "show", id); strings.Contains(out, ".tmp/claude/") {
			t.Errorf("a live, retired or superseded entry and a todo should read with the mapped path:\n%s", out)
		}
	}

	h.must("", "claim", "main")

	ending := h.must("", "search", "--term", "Nudge todo ending", "--status", "retired", "--status", "live")
	if !strings.Contains(ending, entries.ending) || strings.Contains(ending, "end reached") {
		t.Errorf("an entry ending on a moved file should stay live, its ends following the move:\n%s", ending)
	}

	if strings.Contains(ending, "gone:") || strings.Contains(ending, "drifted:") {
		t.Errorf("the entry should read anchored to the moved file:\n%s", ending)
	}
}

func TestAnEntryThePassChangesIsEmbeddedAgain(t *testing.T) {
	h := newHarness(t)
	unrewrittenStore(t, h)

	h.must("", "topics")
	h.must("", "search", "--query", "nudges notes")

	embedded := false
	for _, text := range h.embedder.Texts() {
		embedded = embedded || strings.Contains(text, ".tmp/seats/main/notes.md")
	}

	if !embedded {
		t.Errorf("a changed entry should be embedded from its rewritten text, embedded:\n%v", h.embedder.Texts())
	}
}

func TestTheMarkerStopsASecondPass(t *testing.T) {
	h := newHarness(t)
	unrewrittenStore(t, h)

	h.must("", "topics")

	later := h.file("Filed later naming .tmp/claude/main/late.md", "an older release wrote this")
	h.must("", "topics")

	if out := h.must("", "show", later); !strings.Contains(out, ".tmp/claude/main/late.md") {
		t.Errorf("a store carrying the marker should not run the pass again:\n%s", out)
	}

	if backups := scratchBackups(t, h); len(backups) != 1 {
		t.Errorf("the pass should back the log up once, got %v", backups)
	}
}

func TestAFreshStoreNeedsNoPass(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	if !test.Exists(t, filepath.Join(h.store(), "scratch-paths.json")) {
		t.Error("a store created by this release should carry the marker from the start")
	}

	h.file("Names .tmp/claude/main/x.md", "written by an older release")
	h.must("", "topics")

	if backups := scratchBackups(t, h); len(backups) != 0 {
		t.Errorf("a fresh store should never run the pass, got %v", backups)
	}
}

func TestAStoreNamingNoOldPathTakesOnlyTheMarker(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.file("Gate reads Bash only today", "see internal/gate/gate.go")

	err := os.Remove(filepath.Join(h.store(), "scratch-paths.json"))
	if err != nil {
		t.Fatal(err)
	}

	before := test.Tree(t, filepath.Join(h.store(), "log"))

	h.must("", "topics")

	if after := test.Tree(t, filepath.Join(h.store(), "log")); after != before {
		t.Error("a log the pass does not change should stay byte for byte")
	}

	if backups := scratchBackups(t, h); len(backups) != 0 {
		t.Errorf("a log the pass does not change needs no backup, got %v", backups)
	}

	if !test.Exists(t, filepath.Join(h.store(), "scratch-paths.json")) {
		t.Error("the pass should mark the store done")
	}
}

func TestAnInterruptedPassLeavesTheStoreReadableAndRunsAgain(t *testing.T) {
	for _, tc := range []struct {
		name string
		// blocker is where a folder or file stands in the pass's way.
		blocker string
		folder  bool
	}{
		{name: "before the backup", blocker: "backup"},
		{name: "before the marker", blocker: "scratch-paths.json", folder: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			entries := unrewrittenStore(t, h)

			blocker := filepath.Join(h.store(), tc.blocker)
			if tc.folder {
				test.WriteFile(t, filepath.Join(blocker, "in the way"), "")
			} else {
				test.WriteFile(t, blocker, "")
			}

			got := h.run("", "show", entries.live)
			if !strings.Contains(got.err, "rewriting the scratch paths the store's entries name failed:") {
				t.Errorf("a pass stopped half way should say so, got %q", got.err)
			}

			if !strings.Contains(got.out, entries.live) {
				t.Errorf("the store should stay readable after a stopped pass:\n%s", got.out)
			}

			err := os.RemoveAll(blocker)
			if err != nil {
				t.Fatal(err)
			}

			h.must("", "topics")

			if out := h.must("", "show", entries.live); strings.Contains(out, ".tmp/claude/") {
				t.Errorf("the next call should run the pass again and finish it:\n%s", out)
			}

			if !test.Exists(t, filepath.Join(h.store(), "scratch-paths.json")) {
				t.Error("the finished pass should leave its marker")
			}
		})
	}
}

func TestAnEntryWhoseStoredPathsLostTheirDotFollowsTheMove(t *testing.T) {
	h := newHarness(t)
	unrewrittenStore(t, h)

	hash := sha256.Sum256([]byte("where the nudges stand\n"))
	notesHash := hex.EncodeToString(hash[:])[:16]

	log, err := memorylog.Open(h.store(), filepath.Join(h.store(), "log"))
	if err == nil {
		err = log.Append(context.Background(), memorylog.Event{
			Kind: memorylog.KindNew, Entry: "m-dotless01", Time: h.clock.Now(), Seat: seatID(t, h.store(), "main"),
			Payload: []byte(`{"title":"Dotless todo ending with its notes","body":"b","topics":["gate"],"kind":"todo",` +
				`"from":"agent","about":["tmp/claude/main/notes.md"],` +
				`"until":["gone:tmp/claude/main/notes.md","changed:tmp/claude/main/notes.md"],` +
				`"until_hashes":{"tmp/claude/main/notes.md":"` + notesHash + `"}}`),
		})
	}

	if err != nil {
		t.Fatalf("appending the row an earlier filing stored: %v", err)
	}

	h.must("", "claim", "main")

	joined := strings.Join(storePayloads(t, h), "\n")
	for _, want := range []string{
		`"about":[".tmp/seats/main/notes.md"]`, `"gone:.tmp/seats/main/notes.md","changed:.tmp/seats/main/notes.md"`,
		`"until_hashes":{".tmp/seats/main/notes.md":`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("a stored path that lost its dot should map to the dotted new path, missing %s", want)
		}
	}

	out := h.must("", "search", "--term", "Dotless todo ending", "--status", "retired", "--status", "live")
	if !strings.Contains(out, "m-dotless01") || strings.Contains(out, "end reached") {
		t.Errorf("an entry ending when its moved file is gone should stay live while the file exists:\n%s", out)
	}
}
