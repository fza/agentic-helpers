package capture_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/process"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const highFinding = "  [high] participant-drift: unknown\npre-flight validation blocked: 1 high-severity finding(s)\npre-flight rejected entry\n"

// stockFixture is a project captured into through stock sdd, setting no
// listing prefix, so a draft of any topics clears the header.
func stockFixture(t *testing.T) *fixture {
	t.Helper()

	f := newFixture(t, "")
	f.runner.Stock = true

	return f
}

func TestNoSddRefusesTheCapture(t *testing.T) {
	f := newFixture(t, "grounding/area-prefix.yaml")
	f.runner.Missing = true

	got := f.run(f.draft("capture/full.md"))
	if got.code != 1 || !strings.Contains(got.stderr, "agentic-capture needs sdd on the PATH") ||
		!strings.Contains(got.stderr, "brew install networkteam/tap/sdd") {
		t.Errorf("a missing sdd should be named, with its install, got: %+v", got)
	}

	if len(f.runner.CallsOf("help")) != len(f.runner.Calls) {
		t.Errorf("nothing should run past the probe, got: %v", f.runner.Calls)
	}
}

func TestStockSddChecksAndWritesInOneCall(t *testing.T) {
	f := stockFixture(t)
	f.runner.Capture = process.Result{Stdout: "20261026-090000-d-tac-new.md\n", Stderr: "  [medium] form: a note\n"}

	got := f.run(f.draft("capture/full.md"))
	if got.code != 0 || !strings.Contains(got.stdout, "20261026-090000-d-tac-new.md") {
		t.Fatalf("a clean draft should be written by one call, got: %+v", got)
	}

	captures := f.runner.CallsOf("capture")
	if len(captures) != 1 || len(f.runner.CallsOf("dry-run")) != 0 {
		t.Fatalf("one sdd new and no dry run should run, got: %v", f.runner.Calls)
	}

	call := captures[0]
	if slices.Contains(call, "--preflight-verified") || slices.Contains(call, "--skip-preflight") || call[len(call)-1] == "" {
		t.Errorf("the call should leave the pre-flight to sdd, got: %v", call)
	}

	if strings.Count(got.stderr, "[medium] form: a note") != 1 || strings.Contains(got.stderr, "pre-flight notes, not blocking") {
		t.Errorf("sdd's own findings should pass through once, got: %s", got.stderr)
	}

	if !test.Exists(t, filepath.Join(f.root, ".sdd", "tmp", "capture.lock")) {
		t.Error("the one call should run under the capture lock")
	}
}

func TestStockSddHoldsADraftOnAHighFinding(t *testing.T) {
	f := stockFixture(t)
	f.runner.Capture = process.Result{Code: 1, Stderr: highFinding}

	got := f.run(f.draft("capture/full.md"))
	if got.code != 2 || !strings.Contains(got.stderr, "participant-drift") {
		t.Errorf("a high finding should hold the draft back, got: %+v", got)
	}

	if len(f.runner.CallsOf("capture")) != 1 {
		t.Errorf("nothing should run after the held call, got: %v", f.runner.Calls)
	}
}

func TestStockSddForcedPastAHighFindingRecordsNoCheck(t *testing.T) {
	f := stockFixture(t)
	f.runner.Capture = process.Result{Code: 1, Stderr: highFinding}

	got := f.run(f.draft("capture/full.md"), "--force")

	captures := f.runner.CallsOf("capture")
	if len(captures) != 2 || captures[1][len(captures[1])-1] != "--skip-preflight" {
		t.Fatalf("a forced capture should write once more with --skip-preflight, got: %v", f.runner.Calls)
	}

	if !strings.Contains(got.stderr, "records that nothing checked it") {
		t.Errorf("the caller should hear the entry is recorded unchecked, got: %s", got.stderr)
	}
}

func TestStockSddRetriesAProviderFault(t *testing.T) {
	f := stockFixture(t)
	f.runner.Capture = fault("Error: no content or tool calls in response")

	got := f.run(f.draft("capture/full.md"))
	if got.code != 1 || len(f.runner.CallsOf("capture")) != 3 {
		t.Errorf("a provider fault should be tried three times, got %d: %v", got.code, f.runner.Calls)
	}
}

func TestStockSddRefusesWhatOnlyTheForkWrites(t *testing.T) {
	cases := []struct {
		name  string
		draft string
		args  []string
		meta  string
		says  string
	}{
		{name: "a title", draft: "capture/titled.md", says: "carries `title`"},
		{name: "an actor kind", draft: "capture/actor.md", says: "carries `actor_kind`"},
		{name: "a fallback route", draft: "capture/full.md", args: []string{"--preflight-route", "glm-5.3=layer.yaml"}, says: "--preflight-route"},
		{name: "a caller's pre-flight", draft: "capture/full.md", args: []string{"--preflight-done", strings.Repeat("a", 64)}, says: "--preflight-done"},
		{name: "a graph on schema 2", draft: "capture/full.md", meta: `{"graph_schema_version": 2}`, says: "the graph is on schema 2"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := stockFixture(t)
			if tc.meta != "" {
				test.WriteFile(t, filepath.Join(f.root, ".sdd", "meta.json"), tc.meta)
			}

			got := f.run(append([]string{f.draft(tc.draft)}, tc.args...)...)
			if got.code != 1 || !strings.Contains(got.stderr, tc.says) || !strings.Contains(got.stderr, "brew install fza/tap/sdd") {
				t.Errorf("the capture should be refused, naming the fork, got: %+v", got)
			}

			if len(f.runner.CallsOf("help")) != len(f.runner.Calls) {
				t.Errorf("no sdd new should run, got: %v", f.runner.Calls)
			}
		})
	}
}

func TestTheForkWritesASchemaTwoGraph(t *testing.T) {
	f := newFixture(t, "")
	test.WriteFile(t, filepath.Join(f.root, ".sdd", "meta.json"), `{"graph_schema_version": 2}`)

	got := f.run(f.draft("capture/titled.md"))
	if got.code != 0 || len(f.runner.CallsOf("dry-run")) != 1 || len(f.runner.CallsOf("capture")) != 1 {
		t.Errorf("the fork should take a schema-2 graph and a title, dry run first, got %+v: %v", got, f.runner.Calls)
	}
}

func TestAStockCallCutOffIsNeverRetried(t *testing.T) {
	f := stockFixture(t)
	f.runner.HangCapture = true
	f.limit = "50ms"

	got := f.run(f.draft("capture/full.md"))
	if got.code != 1 || !strings.Contains(got.stderr, "check `git log` before capturing again") {
		t.Errorf("a call cut off by the limit may have written, so it should say where to look, got: %+v", got)
	}

	if len(f.runner.CallsOf("capture")) != 1 || len(f.pauses.Waited) != 0 {
		t.Errorf("a call cut off by the limit should never run again, got: %v", f.runner.Calls)
	}
}

func TestAStockCallFailingWithoutAFindingSaysWhereToLook(t *testing.T) {
	f := stockFixture(t)
	f.runner.Capture = process.Result{Code: 1, Stderr: "error: committing the entry: index.lock exists\n"}

	got := f.run(f.draft("capture/full.md"))
	if got.code != 1 || !strings.Contains(got.stderr, "sdd new failed") || !strings.Contains(got.stderr, "index.lock") ||
		strings.Contains(got.stderr, "pre-flight refused the draft") {
		t.Errorf("a failure past the pre-flight should not blame the draft, got: %+v", got)
	}
}

func TestAStockSchemaRefusalNamesNoMigrate(t *testing.T) {
	f := stockFixture(t)
	f.runner.Capture = process.Result{Code: 1, Stderr: "sdd: refusing to write — graph incompatible with this binary\n  graph schema version 3 does not match binary schema version 1\n"}

	got := f.run(f.draft("capture/full.md"))
	if got.code != 1 || !strings.Contains(got.stderr, "sdd refuses to write this graph; the draft did not cause this") ||
		strings.Contains(got.stderr, "sdd migrate` and capture again") {
		t.Errorf("stock sdd has no migrate, so the refusal should not send the caller there, got: %+v", got)
	}
}
