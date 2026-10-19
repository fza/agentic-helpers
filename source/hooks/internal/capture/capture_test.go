package capture_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/capture"
	"github.com/fza/agentic-helpers/source/hooks/internal/graphconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/process"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const rejected = "  [high] participant-drift: unknown\npre-flight validation blocked: 1 high-severity finding(s)\npre-flight rejected entry\n"

type fixture struct {
	t      *testing.T
	root   string
	runner *test.Runner
	pauses *test.Pauses
	limit  string
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

	return &fixture{t: t, root: root, runner: &test.Runner{Capture: process.Result{Stdout: "captured\n"}}, pauses: &test.Pauses{}}
}

func (f *fixture) draft(fixture string) string {
	f.t.Helper()

	path := filepath.Join(f.root, ".tmp", "drafts", "entry.md")
	test.WriteFile(f.t, path, test.Fixture(f.t, fixture))

	return path
}

func (f *fixture) run(args ...string) result {
	f.t.Helper()

	return f.runIn(context.Background(), args...)
}

func (f *fixture) runIn(ctx context.Context, args ...string) result {
	f.t.Helper()

	captured := &test.Captured{}
	env := capture.Env{WorkingDir: f.root, Runner: f.runner, Limit: f.limit, Pause: f.pauses.Pause}
	code := capture.Main(ctx, args, env, captured.Streams(""))

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
		{name: "held, as sdd exits on a high finding", dryRun: process.Result{Code: 1, Stderr: rejected}, code: 2, says: "1 finding(s) of high severity"},
		{name: "held, forced", dryRun: process.Result{Code: 1, Stderr: rejected}, args: []string{"--force"}, captures: true, says: "capturing despite"},
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
	for _, args := range [][]string{{}, {"--force"}, {"a.md", "b.md"}, {"a.md", "--bogus"},
		{"a.md", "--preflight-limit"}, {"a.md", "--preflight-limit", "soon"}, {"a.md", "--preflight-limit", "0s"}} {
		f := newFixture(t, "")

		got := f.run(args...)
		if got.code != 1 || !strings.Contains(got.stderr, "usage:") {
			t.Errorf("%v should be refused with usage", args)
		}
	}

	outside := t.TempDir()
	test.WriteFile(t, filepath.Join(outside, "entry.md"), test.Fixture(t, "capture/full.md"))

	captured := &test.Captured{}
	code := capture.Main(context.Background(), []string{"entry.md"}, capture.Env{WorkingDir: outside, Runner: &test.Runner{}, Pause: (&test.Pauses{}).Pause}, captured.Streams(""))

	if code != 1 || !strings.Contains(captured.Err.String(), "no .sdd graph") {
		t.Error("a draft no graph governs should be refused")
	}

	if _, err := os.Stat(filepath.Join(outside, ".sdd")); err == nil {
		t.Error("nothing should be created")
	}
}

func fault(text string) process.Result {
	return process.Result{Code: 1, Stderr: "pre-flight error: running pre-flight validator: " + text + " (use --skip-preflight to bypass)\n"}
}

func TestAProviderFaultRetriesThePreflight(t *testing.T) {
	cases := []struct {
		name   string
		answer process.Result
		says   string
	}{
		{name: "an empty answer", answer: fault("gollm openai: no content or tool calls in response"), says: "provider answered nothing"},
		{name: "a rate limit", answer: fault("gollm openai: RateLimitError: API error: status code 429"), says: "provider rate-limited the call"},
		{name: "a server error", answer: fault("gollm openai: API error: status code 503"), says: "provider failed with status 503"},
		{name: "a server error, provider spelling", answer: fault("API error [provider openrouter status 502]"), says: "provider failed with status 502"},
		{name: "a request timeout", answer: fault("gollm openai: API error: status code 408"), says: "provider failed with status 408"},
		{name: "sdd's own time limit", answer: fault("gollm openai: timed out"), says: "provider timed out"},
		{name: "the claude provider's time limit", answer: fault("claude -p timed out (increase with --preflight-timeout)"), says: "provider timed out"},
	}

	for _, tc := range cases {
		t.Run(tc.name+", once", func(t *testing.T) {
			f := newFixture(t, "")
			f.runner.DryRuns = []process.Result{tc.answer, {}}

			got := f.run(f.draft("capture/full.md"))
			if got.code != 0 || len(f.runner.CallsOf("dry-run")) != 2 || len(f.runner.CallsOf("capture")) != 1 {
				t.Fatalf("one fault should cost one retry and still capture, got %d, stderr: %s", got.code, got.stderr)
			}

			if !slices.Equal(f.pauses.Waited, []time.Duration{10 * time.Second}) || !strings.Contains(got.stderr, tc.says+"; trying again in 10s") {
				t.Errorf("the retry should wait the first delay and say why, got %v, stderr: %s", f.pauses.Waited, got.stderr)
			}
		})
	}

	t.Run("every try", func(t *testing.T) {
		f := newFixture(t, "")
		f.runner.DryRun = fault("gollm openai: no content or tool calls in response")

		got := f.run(f.draft("capture/full.md"))
		if got.code != 1 || len(f.runner.CallsOf("dry-run")) != 3 || len(f.runner.CallsOf("capture")) != 0 {
			t.Fatalf("a fault on every try should give up without capturing, got %d, stderr: %s", got.code, got.stderr)
		}

		if !slices.Equal(f.pauses.Waited, []time.Duration{10 * time.Second, 30 * time.Second}) {
			t.Errorf("the retries should wait out each delay in turn, got: %v", f.pauses.Waited)
		}

		if !strings.Contains(got.stderr, "provider answered nothing on all 3 tries; the draft did not cause it") ||
			!strings.Contains(got.stderr, "no content or tool calls") {
			t.Errorf("giving up should name the fault and show sdd's output, got: %s", got.stderr)
		}
	})
}

func TestADraftRefusalNeverRetries(t *testing.T) {
	cases := []struct {
		name   string
		answer process.Result
		code   int
	}{
		{name: "a status no retry changes", answer: fault("gollm openai: API error: status code 401"), code: 1},
		{name: "a high finding naming a fault", answer: process.Result{Code: 1, Stderr: "  [high] scope: the body says the provider timed out\n" + rejected}, code: 2},
		{name: "a shape error naming a fault", answer: process.Result{Code: 1, Stderr: "error: summary says timed out twice\nvalidation failed: 1 issue(s)\n"}, code: 1},
		{name: "an unknown failure", answer: process.Result{Code: 1, Stderr: "unknown flag --terms"}, code: 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "")
			f.runner.DryRun = tc.answer

			got := f.run(f.draft("capture/full.md"))
			if got.code != tc.code || len(f.runner.CallsOf("dry-run")) != 1 || len(f.pauses.Waited) != 0 {
				t.Errorf("the pre-flight should run once and end the capture, got %d after %d tries, stderr: %s",
					got.code, len(f.runner.CallsOf("dry-run")), got.stderr)
			}
		})
	}
}

func TestTheLimitCutsOffATry(t *testing.T) {
	t.Run("once", func(t *testing.T) {
		f := newFixture(t, "")
		f.runner.Hangs = 1

		got := f.run(f.draft("capture/full.md"), "--preflight-limit", "20ms")
		if got.code != 0 || len(f.runner.CallsOf("dry-run")) != 2 || len(f.runner.CallsOf("capture")) != 1 {
			t.Fatalf("a try cut off should retry and still capture, got %d, stderr: %s", got.code, got.stderr)
		}

		if !strings.Contains(got.stderr, "the pre-flight gave no answer within 20ms; trying again in 10s") {
			t.Errorf("the retry should name the limit, got: %s", got.stderr)
		}
	})

	t.Run("every try", func(t *testing.T) {
		f := newFixture(t, "")
		f.runner.Hangs = 9

		got := f.run(f.draft("capture/full.md"), "--preflight-limit", "10ms")
		if got.code != 1 || len(f.runner.CallsOf("dry-run")) != 3 || !strings.Contains(got.stderr, "within 10ms on all 3 tries") {
			t.Errorf("a try cut off every time should give up, got %d, stderr: %s", got.code, got.stderr)
		}

		if !strings.HasSuffix(got.stderr, "capture it again later\n") {
			t.Errorf("a try that printed nothing should leave nothing to show, got: %q", got.stderr)
		}
	})

	t.Run("an interrupt ends the run", func(t *testing.T) {
		f := newFixture(t, "")
		f.runner.Hangs = 9

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		got := f.runIn(ctx, f.draft("capture/full.md"))
		if got.code != 1 || len(f.runner.CallsOf("dry-run")) != 1 || len(f.pauses.Waited) != 0 || !strings.Contains(got.stderr, "interrupted") {
			t.Errorf("an interrupted try should end the run, not retry, got %d, stderr: %s", got.code, got.stderr)
		}
	})
}

func TestWhereTheLimitComesFrom(t *testing.T) {
	cases := []struct {
		name     string
		variable string
		args     []string
		limit    time.Duration
	}{
		{name: "default", limit: 5 * time.Minute},
		{name: "the variable", variable: "7m", limit: 7 * time.Minute},
		{name: "the flag over the variable", variable: "7m", args: []string{"--preflight-limit", "2m"}, limit: 2 * time.Minute},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "")
			f.limit = tc.variable

			f.run(append([]string{f.draft("capture/full.md")}, tc.args...)...)

			if len(f.runner.Limits) != 1 || f.runner.Limits[0] > tc.limit || f.runner.Limits[0] < tc.limit-time.Second {
				t.Errorf("the pre-flight should run under the limit that applies, got: %v", f.runner.Limits)
			}
		})
	}

	t.Run("a variable that is no duration", func(t *testing.T) {
		f := newFixture(t, "")
		f.limit = "soon"

		got := f.run(f.draft("capture/full.md"))
		if got.code != 1 || !strings.Contains(got.stderr, capture.LimitVariable) || len(f.runner.Calls) != 0 {
			t.Errorf("a bad variable should be refused by name before anything runs, got %d, stderr: %s", got.code, got.stderr)
		}
	})
}

func TestTheCaptureRunsOnce(t *testing.T) {
	f := newFixture(t, "")
	f.runner.Capture = process.Result{Code: 1, Stdout: "20261019-120000-d-tac-abc.md\n", Stderr: "gollm openai: timed out\n"}

	got := f.run(f.draft("capture/full.md"))
	if got.code != 1 || len(f.runner.CallsOf("capture")) != 1 || len(f.pauses.Waited) != 0 {
		t.Errorf("a failed capture should never run again, since the entry may have landed, got %d after %d captures",
			got.code, len(f.runner.CallsOf("capture")))
	}
}
