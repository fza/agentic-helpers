package capture_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/capture"
	"github.com/fza/agentic-helpers/source/hooks/internal/graphconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/process"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

type fixture struct {
	t      *testing.T
	root   string
	runner *test.Runner
}

type result struct {
	code   int
	stdout string
	stderr string
}

func newFixture(t *testing.T, config string) *fixture {
	t.Helper()

	root := t.TempDir()
	test.WriteFile(t, filepath.Join(root, ".sdd", "config.yaml"), "graph_dir: .sdd/graph\n")

	if config != "" {
		test.WriteFile(t, filepath.Join(root, ".sdd", "grounding.yaml"), test.Fixture(t, config))
	}

	return &fixture{t: t, root: root, runner: &test.Runner{Capture: process.Result{Stdout: "captured\n"}}}
}

func (f *fixture) draft(fixture string) string {
	f.t.Helper()

	path := filepath.Join(f.root, ".tmp", "drafts", "entry.md")
	test.WriteFile(f.t, path, test.Fixture(f.t, fixture))

	return path
}

func (f *fixture) run(args ...string) result {
	f.t.Helper()

	captured := &test.Captured{}
	env := capture.Env{WorkingDir: f.root, Runner: f.runner}
	code := capture.Main(context.Background(), args, env, captured.Streams(""))

	return result{code: code, stdout: captured.Out.String(), stderr: captured.Err.String()}
}

func flagValue(call []string, flag string) string {
	index := slices.Index(call, flag)
	if index < 0 || index+1 >= len(call) {
		return ""
	}

	return call[index+1]
}

func TestTheDraftBecomesOneSddNewCall(t *testing.T) {
	f := newFixture(t, "grounding/area-prefix.yaml")

	got := f.run(f.draft("capture/full.md"))
	if got.code != 0 || !strings.Contains(got.stdout, "captured") {
		t.Fatalf("a clean draft should capture, got: %+v", got)
	}

	captures := f.runner.CallsOf("capture")
	if len(captures) != 1 || len(f.runner.CallsOf("dry-run")) != 1 {
		t.Fatalf("one pre-flight and one capture should run, got: %v", f.runner.Calls)
	}

	call := captures[0]
	if strings.Join(call[:4], " ") != "sdd new d tac" {
		t.Errorf("the type and layer should open the call, got: %v", call[:4])
	}

	if call[4] != "The binary is `agentic-capture`, and `$(rm -rf /)` stays text." {
		t.Errorf("the body should pass as one argument, backticks intact, got: %q", call[4])
	}

	checks := map[string]string{
		"--kind":         "directive",
		"--intent":       "pending",
		"--confidence":   "high",
		"--participants": "Felix",
		"--topics":       "area-hooks,cli",
	}
	for flag, want := range checks {
		if flagValue(call, flag) != want {
			t.Errorf("%s should carry the header's value, got: %q", flag, flagValue(call, flag))
		}
	}

	refs := flagValue(call, "--refs")
	if !strings.Contains(refs, `"id":"20260925-144721-d-tac-a9x"`) || !strings.Contains(refs, `"kind":"refines"`) {
		t.Errorf("a ref should pass as one JSON object, got: %s", refs)
	}

	if call[len(call)-1] != "--preflight-verified" {
		t.Error("the capture should record the settled pre-flight")
	}

	for _, dir := range f.runner.Dirs {
		if dir != graphconfig.RealPath(f.root) {
			t.Error("sdd should run from the repository root")
		}
	}

	if !test.Exists(t, filepath.Join(f.root, ".sdd", "tmp", "capture.lock")) {
		t.Error("the capture should run under the lock")
	}
}

func TestADraftTheCallCannotTakeIsRefused(t *testing.T) {
	cases := []struct {
		name    string
		config  string
		fixture string
		says    string
	}{
		{name: "no leading block", fixture: "capture/no-block.md", says: "opens with no --- line"},
		{name: "no body", fixture: "capture/no-body.md", says: "carries no body"},
		{name: "no type", fixture: "capture/no-type.md", says: "needs a type"},
		{name: "no topic carrying the prefix", config: "grounding/area-prefix.yaml", fixture: "capture/no-area.md", says: "opening with area-"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, tc.config)

			got := f.run(f.draft(tc.fixture))
			if got.code != 1 || !strings.Contains(got.stderr, tc.says) {
				t.Errorf("the draft should be refused with a reason, got: %+v", got)
			}

			if len(f.runner.Calls) != 0 {
				t.Error("a refused draft should reach no command")
			}
		})
	}

	t.Run("no prefix, no topic rule", func(t *testing.T) {
		f := newFixture(t, "")

		if f.run(f.draft("capture/no-area.md")).code != 0 || f.run(f.draft("capture/no-topics.md")).code != 0 {
			t.Error("a project naming no prefix should take any topic, or none")
		}
	})
}

func TestTheProjectsLint(t *testing.T) {
	t.Run("no key, no lint", func(t *testing.T) {
		f := newFixture(t, "grounding/area-prefix.yaml")
		f.run(f.draft("capture/full.md"))

		if len(f.runner.CallsOf("lint")) != 0 {
			t.Error("a project naming no lint should run none")
		}
	})

	t.Run("runs the named command on the body beside the draft", func(t *testing.T) {
		f := newFixture(t, "grounding/full.yaml")
		path := f.draft("capture/full.md")

		if f.run(path).code != 0 {
			t.Fatal("a clean lint should let the capture through")
		}

		lints := f.runner.CallsOf("lint")
		if len(lints) != 1 {
			t.Fatalf("the lint should run once, got: %v", f.runner.Calls)
		}

		body := filepath.Join(filepath.Dir(path), ".entry.md.body.md")
		if !strings.HasPrefix(lints[0][2], "vale --output=line") || lints[0][len(lints[0])-1] != body {
			t.Errorf("the named command should get the body file beside the draft, got: %v", lints[0])
		}

		if test.Exists(t, body) {
			t.Error("the body file should be removed")
		}
	})

	t.Run("findings block, on the draft's lines", func(t *testing.T) {
		f := newFixture(t, "grounding/full.yaml")
		path := f.draft("capture/two-lines.md")
		body := filepath.Join(filepath.Dir(path), ".entry.md.body.md")
		f.runner.Lint = process.Result{Code: 1, Stdout: body + ":2:1:House.Passive:passive voice\n"}

		got := f.run(path)
		if got.code != 1 || !strings.Contains(got.stderr, path+":8:1:House.Passive") {
			t.Errorf("the finding should block and name the draft's line, got: %+v", got)
		}

		if len(f.runner.CallsOf("dry-run")) != 0 || len(f.runner.CallsOf("capture")) != 0 {
			t.Error("a lint finding should stop the run before the pre-flight")
		}
	})

	t.Run("a silent failure blocks", func(t *testing.T) {
		f := newFixture(t, "grounding/full.yaml")
		f.runner.Lint = process.Result{Code: 2, Stderr: "config not found"}

		got := f.run(f.draft("capture/full.md"))
		if got.code != 1 || !strings.Contains(got.stderr, "went unread") || len(f.runner.CallsOf("capture")) != 0 {
			t.Errorf("a lint that never read the body should block, got: %+v", got)
		}
	})
}

func TestThePreflight(t *testing.T) {
	cases := []struct {
		name     string
		dryRun   process.Result
		args     []string
		code     int
		captures bool
		says     string
		hides    string
	}{
		{name: "clean", code: 0, captures: true},
		{name: "medium notes", dryRun: process.Result{Stdout: "  [medium] ref-kind: prefer refines\n    wrapped detail\n"}, captures: true, says: "not blocking: [medium] ref-kind: prefer refines wrapped detail"},
		{name: "low stays quiet", dryRun: process.Result{Stdout: "  [low] directive-shape: fine\n"}, captures: true, hides: "directive-shape"},
		{name: "high blocks", dryRun: process.Result{Stdout: "  [high] participant-drift: unknown\n"}, code: 2, says: "participant-drift"},
		{name: "high forced", dryRun: process.Result{Stdout: "  [high] participant-drift: unknown\n"}, args: []string{"--force"}, captures: true, says: "capturing despite"},
		{name: "refused outright", dryRun: process.Result{Code: 1, Stderr: "unknown flag --terms"}, code: 1, says: "pre-flight refused the draft"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "")
			f.runner.DryRun = tc.dryRun

			got := f.run(append([]string{f.draft("capture/full.md")}, tc.args...)...)
			if got.code != tc.code {
				t.Errorf("the exit should say how the run ended, got %d, stderr: %s", got.code, got.stderr)
			}

			if (len(f.runner.CallsOf("capture")) == 1) != tc.captures {
				t.Error("the capture should run only where the pre-flight clears or is forced")
			}

			if tc.says != "" && !strings.Contains(got.stderr, tc.says) {
				t.Errorf("the finding should reach the caller, got: %s", got.stderr)
			}

			if tc.hides != "" && strings.Contains(got.stderr, tc.hides) {
				t.Error("a low finding should never reach the caller")
			}
		})
	}

	t.Run("skip-preflight captures unchecked", func(t *testing.T) {
		f := newFixture(t, "")

		got := f.run(f.draft("capture/full.md"), "--skip-preflight")
		captures := f.runner.CallsOf("capture")

		if got.code != 0 || len(f.runner.CallsOf("dry-run")) != 0 || len(captures) != 1 || captures[0][len(captures[0])-1] != "--skip-preflight" {
			t.Errorf("the capture should run once, marked unchecked, got: %v", f.runner.Calls)
		}
	})
}

func TestUsageAndPlace(t *testing.T) {
	for _, args := range [][]string{{}, {"--force"}, {"a.md", "b.md"}, {"a.md", "--bogus"}} {
		f := newFixture(t, "")

		got := f.run(args...)
		if got.code != 1 || !strings.Contains(got.stderr, "usage:") {
			t.Errorf("%v should be refused with usage", args)
		}
	}

	outside := t.TempDir()
	test.WriteFile(t, filepath.Join(outside, "entry.md"), test.Fixture(t, "capture/full.md"))

	captured := &test.Captured{}
	code := capture.Main(context.Background(), []string{"entry.md"}, capture.Env{WorkingDir: outside, Runner: &test.Runner{}}, captured.Streams(""))

	if code != 1 || !strings.Contains(captured.Err.String(), "no .sdd graph") {
		t.Error("a draft no graph governs should be refused")
	}

	if _, err := os.Stat(filepath.Join(outside, ".sdd")); err == nil {
		t.Error("nothing should be created")
	}
}
