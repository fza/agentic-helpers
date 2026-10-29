package memory_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

// sealedStore files entries large enough that the log seals its tail into
// segments, then one more into the tail.
func sealedStore(t *testing.T) (*harness, []string) {
	t.Helper()

	h := newHarness(t)
	h.must("", "claim", "main")

	body := strings.Repeat("x", 30000)
	for index := range 3 {
		h.file(fmt.Sprintf("Large finding number %d here", index), body)
	}

	h.file("Small finding in the tail", "short")

	segments, _ := filepath.Glob(filepath.Join(h.store(), "log", "*.seg"))
	if len(segments) == 0 {
		t.Fatal("the store should hold sealed segments")
	}

	return h, segments
}

func liveListing(t *testing.T, h *harness) string {
	t.Helper()

	return h.must("", "search", "--status", "live", "--all-seats", "--limit", "50")
}

func TestASecondReadOfAnUnchangedStoreReadsNoSegment(t *testing.T) {
	h, segments := sealedStore(t)
	first := liveListing(t, h)

	for _, segment := range segments {
		err := os.Chmod(segment, 0)
		if err != nil {
			t.Fatal(err)
		}
	}

	t.Cleanup(func() {
		for _, segment := range segments {
			_ = os.Chmod(segment, 0o600)
		}
	})

	got := h.run("", "search", "--status", "live", "--all-seats", "--limit", "50", "--again")
	if got.code != 0 || got.out != first+"\n" && got.out != first {
		t.Errorf("a read of an unchanged store should come from the cached fold, reading no segment, got %d %q", got.code, got.err)
	}
}

func TestAnAppendIsFoldedOntoTheCache(t *testing.T) {
	h, _ := sealedStore(t)
	liveListing(t, h)

	id := h.file("Finding filed after the read", "appended")
	h.must("", "retire", id, "--because", "done")

	out := h.must("", "search", "--term", "finding", "--all-seats", "--limit", "50")
	if strings.Count(out, "Finding filed after the read") != 1 || !strings.Contains(out, "retired") ||
		strings.Count(out, "Small finding in the tail") != 1 || strings.Count(out, "Large finding number 0 here") != 1 {
		t.Errorf("rows appended since the cached fold should be folded on it once, the rest kept once, got:\n%s", out)
	}
}

func TestACacheThatDoesNotMatchIsRebuilt(t *testing.T) {
	cases := []struct {
		name   string
		change func(t *testing.T, h *harness)
		want   string
	}{
		{name: "a planted cache", want: "Small finding in the tail", change: func(t *testing.T, h *harness) {
			test.WriteFile(t, filepath.Join(h.cacheDir(), "fold.gob"), "planted")
		}},
		{name: "a rewritten tail", want: "Rewritten finding in the tail", change: func(t *testing.T, h *harness) {
			tail := filepath.Join(h.store(), "log", "tail.jsonl")
			raw, _ := os.ReadFile(tail)
			info, _ := os.Stat(tail)

			test.WriteFile(t, tail, strings.Replace(string(raw), "Small finding in the tail", "Rewritten finding in the tail", 1))
			_ = os.Chtimes(tail, info.ModTime(), info.ModTime())
		}},
		{name: "a renamed seat", want: "[chief, note", change: func(t *testing.T, h *harness) {
			h.must("", "rename", "chief")
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := sealedStore(t)
			liveListing(t, h)
			tc.change(t, h)

			out := h.must("", "search", "--term", "tail", "--all-seats", "--again")
			if !strings.Contains(out, tc.want) {
				t.Errorf("a fold whose key no longer matches the store should be folded again, got:\n%s", out)
			}
		})
	}
}

func TestASealSinceTheCachedFoldIsFoldedAgain(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	body := strings.Repeat("x", 30000)
	for index := range 3 {
		h.file(fmt.Sprintf("Large finding number %d here", index), body)
	}

	tail, _ := os.ReadFile(filepath.Join(h.store(), "log", "tail.jsonl"))
	if len(tail) != 0 {
		t.Fatal("the store should have sealed its whole tail, so the cached fold holds no tail row")
	}

	liveListing(t, h)

	// One append past the seal size seals at once, leaving the tail as empty
	// as the cached fold found it: only the segments tell the two apart.
	h.file("Later large finding sealed at once", strings.Repeat("y", 70000))

	if out := liveListing(t, h); strings.Count(out, "Later large finding") != 1 {
		t.Errorf("segments sealed since the cached fold should be folded again, got:\n%s", out)
	}
}

func TestAnchorHashesAreKeptBySizeAndModificationTime(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	id := h.file("Gate reads only Bash calls", "the gate reads internal/gate/gate.go only")

	gate := filepath.Join(h.repo, "internal", "gate", "gate.go")
	if out := h.must("", "show", id, "--no-heat"); strings.Contains(out, "drifted") {
		t.Fatalf("an untouched anchor should not read as drifted:\n%s", out)
	}

	info, _ := os.Stat(gate)
	test.WriteFile(t, gate, "package gatf\n")

	err := os.Chtimes(gate, info.ModTime(), info.ModTime())
	if err != nil {
		t.Fatal(err)
	}

	if out := h.must("", "show", id, "--no-heat"); strings.Contains(out, "drifted") {
		t.Errorf("a file of the size and time it was hashed at should be answered from the cache, got:\n%s", out)
	}

	later := info.ModTime().Add(time.Minute)

	err = os.Chtimes(gate, later, later)
	if err != nil {
		t.Fatal(err)
	}

	if out := h.must("", "show", id, "--no-heat", "--again"); !strings.Contains(out, "drifted: internal/gate/gate.go") {
		t.Errorf("a file whose modification time moved should be hashed again, got:\n%s", out)
	}
}

func TestLinkStatesAreWorkedOutOnlyForWhatAReadShows(t *testing.T) {
	h := newHarness(t)
	withGraph(t, h)
	h.must("", "claim", "main")
	h.file("Finding citing a closed decision", "see the decision", "--link", closedDecision)
	h.file("Finding about something else", "unrelated")

	cache := filepath.Join(h.cacheDir(), "graph-status.json")
	_ = os.Remove(cache)
	before := h.graph.Calls()

	h.must("", "search", "--term", "something else", "--no-heat")
	h.must("", "view", "--layout", "kind(rule):as-list")

	if h.graph.Calls() != before {
		t.Error("a read showing no entry with a graph link should ask sdd export nothing")
	}

	h.must("", "search", "--term", "closed decision", "--no-heat")

	if h.graph.Calls() != before+1 {
		t.Error("a read showing an entry with a graph link should ask sdd export once")
	}
}
