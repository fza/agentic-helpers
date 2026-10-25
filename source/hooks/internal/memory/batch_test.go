package memory_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// logBytes is every file of the store's log, by name, so a refused batch can
// be shown to have written nothing.
func (h *harness) logBytes() map[string]string {
	h.t.Helper()

	held := map[string]string{}

	_ = filepath.WalkDir(filepath.Join(h.store(), "log"), func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}

		data, err := os.ReadFile(path)
		if err == nil {
			held[path] = string(data)
		}

		return nil
	})

	return held
}

func (h *harness) logUnchanged(before map[string]string) bool {
	after := h.logBytes()
	if len(after) != len(before) {
		return false
	}

	for path, data := range before {
		if after[path] != data {
			return false
		}
	}

	return true
}

func (h *harness) tailLines() int {
	data, _ := os.ReadFile(filepath.Join(h.store(), "log", "tail.jsonl"))

	return strings.Count(string(data), "\n")
}

func TestABatchLandsEveryIDInOneAppend(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	first := h.file("First gate finding to fold", "first")
	second := h.file("Second gate finding to fold", "second")
	summary := h.file("Summary of both gate findings", "both")

	before := h.tailLines()

	got := h.must("", "supersede", first, second, "--by", summary)
	if !strings.Contains(got, "superseded "+first+", "+second+" by "+summary) {
		t.Errorf("the reply should name every superseded id, got %q", got)
	}

	if h.tailLines() != before+2 {
		t.Error("a batch should append one event per id")
	}

	for _, id := range []string{first, second} {
		if !strings.Contains(h.must("", "show", id), "superseded") {
			t.Errorf("show should reflect the supersede of %s", id)
		}
	}

	if got := h.must("", "confirm", summary, first); strings.Count(got, "confirmed ") != 2 {
		t.Errorf("confirm should name every confirmed id, got %q", got)
	}

	third := h.file("Third gate finding kept longer", "third")

	if got := h.must("", "retain", summary, third, "long"); !strings.Contains(got, summary+", "+third+" retained long") {
		t.Errorf("retain should name every id, got %q", got)
	}

	for _, id := range []string{summary, third} {
		if !strings.Contains(h.must("", "show", id), "long") {
			t.Errorf("show should reflect the retention of %s", id)
		}
	}

	if got := h.must("", "retire", summary, third); !strings.Contains(got, "retired "+summary+", "+third) {
		t.Errorf("a retire should name every id, got %q", got)
	}
}

func TestABatchWithAFailingIDLandsNothing(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	live := h.file("Live gate finding stays live", "live")
	superseded := h.file("Gate finding already superseded", "gone")
	retired := h.file("Gate finding already retired", "done")
	h.must("", "supersede", superseded, "--because", "wrong")
	h.must("", "retire", retired)

	other := newHarnessSharing(h)
	other.must("", "claim", "drive")
	foreign := other.file("Gate finding of another seat", "theirs")

	cases := []struct {
		name  string
		args  []string
		verb  string
		names []string
	}{
		{"supersede over a superseded id", []string{"supersede", live, superseded, "--because", "x"}, "superseded",
			[]string{superseded + " is already superseded"}},
		{"retire over a retired id", []string{"retire", live, retired}, "retired", []string{retired + " is already retired"}},
		{"retain over another seat's id", []string{"retain", live, foreign, "long"}, "retained",
			[]string{foreign + " belongs to drive"}},
		{"every failing id", []string{"retire", live, retired, foreign, "m-zzzz99"}, "retired",
			[]string{retired + " is already retired", foreign + " belongs to drive", "m-zzzz99: no such entry"}},
		{"confirm over a missing id", []string{"confirm", live, "m-zzzz99"}, "confirmed", []string{"m-zzzz99"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := h.logBytes()

			got := h.run("", tc.args...)
			if got.code != 1 || !strings.Contains(got.err, "agentic-memory: nothing "+tc.verb+":\n") {
				t.Fatalf("a batch with a failing id should be refused whole, got %d %q", got.code, got.err)
			}

			for _, line := range tc.names {
				if !strings.Contains(got.err, "\n  "+line) {
					t.Errorf("the refusal should name each failing id on its own line, got %q", got.err)
				}
			}

			if strings.Contains(got.err, live) {
				t.Errorf("the refusal should name only the failing ids, got %q", got.err)
			}

			if !h.logUnchanged(before) {
				t.Error("a refused batch should leave the log as it was")
			}
		})
	}

	if strings.Contains(h.must("", "show", live), "retired") {
		t.Error("the good id of a refused batch should stay live")
	}
}

func TestANewEntryAbsorbsTheEntriesItSummarises(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	first := h.file("First gate note to absorb", "first")
	second := h.file("Second gate note to absorb", "second")

	before := h.tailLines()
	out := h.must("summary", "new", "--title", "Summary of the gate notes", "--topic", "gate", "--absorbs", first, "--absorbs", second)
	summary := idPattern.FindString(out)

	if !strings.Contains(out, "superseded "+first+", "+second+" by "+summary) {
		t.Errorf("the filing should say what it superseded, got %q", out)
	}

	if h.tailLines() != before+3 {
		t.Error("the new entry and its supersedes should land in one append")
	}

	for _, id := range []string{first, second} {
		if !strings.Contains(h.must("", "show", id), "superseded by "+summary) {
			t.Errorf("%s should be superseded by the new entry", id)
		}
	}

	t.Run("over an id already superseded", func(t *testing.T) {
		third := h.file("Third gate note to absorb", "third")
		logged := h.logBytes()

		got := h.run("again", "new", "--title", "Second summary of gate notes", "--topic", "gate",
			"--absorbs", third, "--absorbs", first)
		if got.code != 1 || !strings.Contains(got.err, "agentic-memory: nothing filed:\n  "+first+" is already superseded") {
			t.Fatalf("a filing over a failing absorb should be refused, got %d %q", got.code, got.err)
		}

		if !h.logUnchanged(logged) {
			t.Error("a refused filing should leave the log as it was")
		}
	})
}

func TestTheBatchToolsTakeIDs(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	first := h.file("First gate note over MCP", "first")
	second := h.file("Second gate note over MCP", "second")

	for _, tool := range []string{"supersede", "confirm", "retain"} {
		answer := h.mcp(`{"name":"` + tool + `","arguments":{"id":"` + first + `"}}`)
		if !strings.Contains(answer, tool+` takes no key \"id\"`) {
			t.Errorf("%s should refuse the single id key, got %s", tool, answer)
		}
	}

	answer := h.mcp(`{"name":"retain","arguments":{"ids":["` + first + `","` + second + `"],"level":"long"}}`)
	if !strings.Contains(answer, first+", "+second+" retained long") {
		t.Errorf("retain should take ids, got %s", answer)
	}

	answer = h.mcp(`{"name":"new","arguments":{"title":"Summary of the MCP notes","topic":["gate"],"body":"both",` +
		`"absorbs":["` + first + `","` + second + `"]}}`)
	if !strings.Contains(answer, "superseded "+first+", "+second+" by ") {
		t.Errorf("new should take absorbs, got %s", answer)
	}
}

func TestAnIDNamedTwiceChangesOnce(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	id := h.file("Gate finding named twice", "twice")

	before := h.tailLines()

	if got := h.must("", "retire", id, id); strings.Count(got, id) != 1 {
		t.Errorf("the reply should name a repeated id once, got %q", got)
	}

	if h.tailLines() != before+1 {
		t.Error("a repeated id should land one event")
	}
}

func TestTheSimilarHintSkipsWhatTheFilingAbsorbed(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	absorbed := h.file("Gate seal tail manifest rename", "the gate seals the tail and renames the manifest")
	kept := h.file("Gate seal tail manifest rewrite", "the gate seals the tail and renames the manifest again")

	out := h.must("the gate seals the tail and renames the manifest", "new", "--title", "Gate seal tail manifest summary",
		"--topic", "gate", "--absorbs", absorbed)

	if strings.Contains(out, "similar: "+absorbed) {
		t.Errorf("the hint should never offer an entry the filing absorbed:\n%s", out)
	}

	if !strings.Contains(out, "similar: "+kept) {
		t.Errorf("the hint should still offer a close entry the filing kept:\n%s", out)
	}
}
