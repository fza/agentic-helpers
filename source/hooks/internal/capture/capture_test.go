package capture_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

func TestEveryHeaderKeyReachesItsFlag(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
		flag    string
		want    func(string) bool
	}{
		{name: "an actor's kind", fixture: "capture/actor.md", flag: "--actor-kind",
			want: func(value string) bool { return value == "human" }},
		{name: "an entry's title", fixture: "capture/titled.md", flag: "--title",
			want: func(value string) bool { return value == "Plugin ships from plugin/" }},
		{name: "a fact's index", fixture: "capture/fact.md", flag: "--index",
			want: func(value string) bool {
				return strings.Contains(value, `"title":"The hooks ship as a plugin"`) &&
					strings.Contains(value, `"topic":"hooks/toolchain"`)
			}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "")

			got := f.run(f.draft(tc.fixture))
			if got.code != 0 {
				t.Fatalf("the draft should capture, got: %+v", got)
			}

			for _, call := range append(f.runner.CallsOf("dry-run"), f.runner.CallsOf("capture")...) {
				if !tc.want(flagValue(call, tc.flag)) {
					t.Errorf("%s should carry the header's value to the pre-flight and the capture, got: %q",
						tc.flag, flagValue(call, tc.flag))
				}
			}
		})
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
		{name: "a key reaching no flag", fixture: "capture/unknown-key.md", says: "carries actorkind, reviewer, which reaches no `sdd new` flag; it takes type, layer, attach, kind"},
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
		{"a.md", "--preflight-limit"}, {"a.md", "--preflight-limit", "soon"}, {"a.md", "--preflight-limit", "0s"},
		{"a.md", "--preflight-route"}, {"a.md", "--preflight-route", "glm"}, {"a.md", "--preflight-route", "=a.yaml"},
		{"a.md", "--preflight-route", "glm="}, {"a.md", "--preflight-done"}, {"a.md", "--preflight-done", "abc"},
		{"a.md", "--preflight-done", strings.Repeat("g", 64)}, {"a.md", "--preflight-done", strings.Repeat("a", 64), "--skip-preflight"}} {
		f := newFixture(t, "")

		got := f.run(args...)
		if got.code != 1 || !strings.Contains(got.stderr, "usage:") || !strings.Contains(got.stderr, "--preflight-route") ||
			!strings.Contains(got.stderr, "--preflight-done") {
			t.Errorf("%v should be refused with usage naming every flag", args)
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

const emptyAnswer = "gollm openai: ResponseError (failed to parse response): no content or tool calls in response"

// layer writes a route's configuration layer under mode and returns its path.
func (f *fixture) layer(name string, mode os.FileMode) string {
	f.t.Helper()

	path := filepath.Join(f.root, ".tmp", "layers", name)
	test.WriteFile(f.t, path, test.Fixture(f.t, "capture/layer.yaml"))

	err := os.Chmod(path, mode)
	if err != nil {
		f.t.Fatalf("setting the mode of %s: %v", path, err)
	}

	return path
}

func (f *fixture) routed(routes ...string) result {
	f.t.Helper()

	args := []string{f.draft("capture/full.md")}
	for _, each := range routes {
		args = append(args, "--preflight-route", each)
	}

	return f.run(args...)
}

func faults(count int, then ...process.Result) []process.Result {
	answers := make([]process.Result, count)
	for index := range answers {
		answers[index] = fault(emptyAnswer)
	}

	return append(answers, then...)
}

func TestAFallbackRouteDecidesThePreflight(t *testing.T) {
	cases := []struct {
		name    string
		answers []process.Result
		tries   int
		route   string
		layer   string
	}{
		{name: "the primary answering", answers: []process.Result{{}}, tries: 1, route: "primary"},
		{name: "the first route after the primary answered nothing", answers: faults(3, process.Result{}), tries: 4, route: "glm-5.3", layer: "glm.yaml"},
		{name: "the second route after the first answered nothing", answers: faults(4, process.Result{}), tries: 5, route: "kimi-k3", layer: "kimi.yaml"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "")
			f.runner.DryRuns = tc.answers

			got := f.routed("glm-5.3="+f.layer("glm.yaml", 0o600), "kimi-k3="+f.layer("kimi.yaml", 0o600))
			if got.code != 0 || len(f.runner.CallsOf("capture")) != 1 {
				t.Fatalf("the route answering should let the capture through, got %d, stderr: %s", got.code, got.stderr)
			}

			if got.stdout != "AGENTIC_CAPTURE_ROUTE "+tc.route+"\ncaptured\n" {
				t.Errorf("one line should name the route that answered, ahead of the capture's output, got: %q", got.stdout)
			}

			dryRuns := f.runner.CallsOf("dry-run")
			if len(dryRuns) != tc.tries || !slices.Equal(f.pauses.Waited, retryWaits(tc.tries)) {
				t.Fatalf("the primary should keep its retries and each route get one try with no wait, got %d tries, waits %v",
					len(dryRuns), f.pauses.Waited)
			}

			last := dryRuns[len(dryRuns)-1]
			if tc.layer == "" {
				if slices.Contains(last, "--model") || slices.Contains(last, "--local-config") {
					t.Error("the primary should run on the caller's own configuration")
				}
			} else if flagValue(last, "--model") != tc.route || filepath.Base(flagValue(last, "--local-config")) != tc.layer {
				t.Errorf("the route should hand sdd its model and its layer, got: %v", last)
			}

			capturing := f.runner.CallsOf("capture")[0]
			if slices.Contains(capturing, "--model") || slices.Contains(capturing, "--local-config") {
				t.Error("the capture should stay on the caller's own configuration")
			}
		})
	}

	t.Run("a relative layer names a file in the working directory", func(t *testing.T) {
		f := newFixture(t, "")
		f.runner.DryRuns = faults(3, process.Result{})
		f.layer("glm.yaml", 0o600)

		got := f.routed("glm-5.3=.tmp/layers/glm.yaml")
		dryRuns := f.runner.CallsOf("dry-run")

		if got.code != 0 || flagValue(dryRuns[3], "--local-config") != filepath.Join(f.root, ".tmp", "layers", "glm.yaml") {
			t.Errorf("the layer should reach sdd as an absolute path, got: %v", dryRuns[3])
		}
	})
}

func retryWaits(tries int) []time.Duration {
	if tries == 1 {
		return nil
	}

	return []time.Duration{10 * time.Second, 30 * time.Second}
}

func TestARefusalNeverReachesARoute(t *testing.T) {
	cases := []struct {
		name    string
		answers []process.Result
		tries   int
		code    int
	}{
		{name: "a high finding on the primary", answers: []process.Result{{Code: 1, Stderr: rejected}}, tries: 1, code: 2},
		{name: "a status no retry changes on the primary", answers: []process.Result{fault("gollm openai: API error: status code 401")}, tries: 1, code: 1},
		{name: "a high finding on a route", answers: faults(3, process.Result{Code: 1, Stderr: rejected}), tries: 4, code: 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "")
			f.runner.DryRuns = tc.answers

			got := f.routed("glm-5.3="+f.layer("glm.yaml", 0o600), "kimi-k3="+f.layer("kimi.yaml", 0o600))
			if got.code != tc.code || len(f.runner.CallsOf("dry-run")) != tc.tries || len(f.runner.CallsOf("capture")) != 0 {
				t.Errorf("the route that answered should decide, with no route after it tried, got %d after %d tries, stderr: %s",
					got.code, len(f.runner.CallsOf("dry-run")), got.stderr)
			}
		})
	}
}

func TestAWrittenEntryIsNeverRunTwice(t *testing.T) {
	f := newFixture(t, "")
	f.runner.DryRuns = faults(3, process.Result{})
	f.runner.Capture = process.Result{Code: 1, Stdout: "20261020-120000-d-tac-abc.md\n", Stderr: "gollm openai: timed out\n"}

	got := f.routed("glm-5.3=" + f.layer("glm.yaml", 0o600))
	if got.code != 1 || len(f.runner.CallsOf("capture")) != 1 || len(f.runner.CallsOf("dry-run")) != 4 {
		t.Errorf("a failed capture should never run again, on any route, got %d after %d captures",
			got.code, len(f.runner.CallsOf("capture")))
	}
}

func TestEveryRouteFailing(t *testing.T) {
	t.Run("answering nothing", func(t *testing.T) {
		f := newFixture(t, "")
		f.runner.DryRun = fault(emptyAnswer)

		got := f.routed("glm-5.3="+f.layer("glm.yaml", 0o600), "kimi-k3="+f.layer("kimi.yaml", 0o600))
		if got.code != 1 || len(f.runner.CallsOf("dry-run")) != 5 || len(f.runner.CallsOf("capture")) != 0 {
			t.Fatalf("a fault on every route should give up without capturing, got %d, stderr: %s", got.code, got.stderr)
		}

		for _, name := range []string{"primary", "glm-5.3", "kimi-k3"} {
			if !strings.Contains(got.stderr, "\nroute "+name+": the pre-flight's provider answered nothing: pre-flight error:") {
				t.Errorf("each route's last error should stand on its own line, got: %s", got.stderr)
			}
		}

		if !strings.HasSuffix(got.stderr, "failed on every route; the draft did not cause it, so capture it again later\n") {
			t.Errorf("the failure should close the output, got: %s", got.stderr)
		}

		if got.stdout != "" {
			t.Errorf("no route answered, so none should be named, got: %q", got.stdout)
		}
	})

	t.Run("cut off", func(t *testing.T) {
		f := newFixture(t, "")
		f.runner.Hangs = 9

		got := f.run(f.draft("capture/full.md"), "--preflight-limit", "10ms", "--preflight-route", "glm-5.3="+f.layer("glm.yaml", 0o600))
		if got.code != 1 || len(f.runner.CallsOf("dry-run")) != 4 || !strings.Contains(got.stderr, "\nroute glm-5.3: the pre-flight gave no answer within 10ms\n") {
			t.Errorf("a route cut off should be passed over by its limit, got %d, stderr: %s", got.code, got.stderr)
		}
	})
}

func TestAnUnusableLayerIsSkipped(t *testing.T) {
	f := newFixture(t, "")
	f.runner.DryRuns = faults(3, process.Result{})

	missing := filepath.Join(f.root, ".tmp", "layers", "missing.yaml")
	shared := f.layer("shared.yaml", 0o640)
	open := f.layer("open.yaml", 0o604)

	got := f.routed("glm-5.3="+missing, "kimi-k3="+shared, "qwen-4="+open, "deepseek-v4="+f.layer("own.yaml", 0o600))
	if got.code != 0 || got.stdout != "AGENTIC_CAPTURE_ROUTE deepseek-v4\ncaptured\n" || len(f.runner.CallsOf("dry-run")) != 4 {
		t.Fatalf("the first usable route should answer, every unusable one skipped untried, got %d, %q, stderr: %s",
			got.code, got.stdout, got.stderr)
	}

	for _, line := range []string{
		"route glm-5.3 skipped: " + missing + " is missing\n",
		"route kimi-k3 skipped: " + shared + " is readable by group or others\n",
		"route qwen-4 skipped: " + open + " is readable by group or others\n",
	} {
		if strings.Count(got.stderr, line) != 1 {
			t.Errorf("each unusable layer should be named once with why, got: %s", got.stderr)
		}
	}

	if strings.Contains(got.stdout+got.stderr, "sk-fixture-never-printed") {
		t.Error("a layer's contents should never be printed")
	}
}

// A file another user owns cannot be made without root, so the system's own
// account file stands in for one.
func TestALayerNotTheCallersOwnOrUnreadableIsSkipped(t *testing.T) {
	foreign := "/etc/passwd"

	_, err := os.Stat(foreign)
	if err != nil || os.Getuid() == 0 {
		t.Skip("needs a file another user owns, and a caller other than root")
	}

	f := newFixture(t, "")
	f.runner.DryRuns = faults(3, process.Result{})
	unreadable := f.layer("unreadable.yaml", 0o200)

	got := f.routed("glm-5.3="+foreign, "kimi-k3="+unreadable, "qwen-4="+f.layer("own.yaml", 0o600))
	if got.code != 0 || got.stdout != "AGENTIC_CAPTURE_ROUTE qwen-4\ncaptured\n" || len(f.runner.CallsOf("dry-run")) != 4 {
		t.Fatalf("the usable route should answer, the others skipped untried, got %d, %q, stderr: %s", got.code, got.stdout, got.stderr)
	}

	if !strings.Contains(got.stderr, "route glm-5.3 skipped: "+foreign+" is not the caller's own\n") ||
		!strings.Contains(got.stderr, "route kimi-k3 skipped: "+unreadable+" is not a readable file\n") {
		t.Errorf("each skipped layer should be named with why, got: %s", got.stderr)
	}
}

func TestNoRouteKeepsTheOutputAsItWas(t *testing.T) {
	f := newFixture(t, "")
	f.runner.DryRuns = faults(1, process.Result{})

	got := f.run(f.draft("capture/full.md"))
	if got.code != 0 || got.stdout != "captured\n" || strings.Contains(got.stderr, "route") {
		t.Errorf("a caller naming no route should see no route line, got %q, stderr: %s", got.stdout, got.stderr)
	}
}

func TestHelpNamesTheBytesTheHashCovers(t *testing.T) {
	f := newFixture(t, "")

	got := f.run("--help")
	if got.code != 0 || got.stderr != "" || !strings.HasPrefix(got.stdout, "usage: agentic-capture <draft.md>") {
		t.Fatalf("help should print the usage on stdout and exit cleanly, got: %+v", got)
	}

	if !strings.Contains(got.stdout, "--preflight-done <sha256>          the sha256 of the draft file's bytes as read from disk, front matter included") {
		t.Errorf("help should name the bytes the hash covers, got: %s", got.stdout)
	}

	if len(f.runner.Calls) != 0 {
		t.Error("help should run nothing")
	}
}

func digestOf(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:])
}

func TestAPreflightTheCallerRan(t *testing.T) {
	cases := []struct {
		name    string
		hash    func(own string) string
		dryRuns int
		says    string
	}{
		{name: "matching", hash: func(own string) string { return own }, says: "pre-flight skipped: the caller ran it on these exact bytes\n"},
		{name: "matching in upper case", hash: strings.ToUpper, says: "pre-flight skipped: the caller ran it on these exact bytes\n"},
		{name: "differing", hash: func(string) string { return strings.Repeat("0", 64) }, dryRuns: 1,
			says: "--preflight-done names " + strings.Repeat("0", 64) + ", but the draft hashes to "},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "")
			path := f.draft("capture/full.md")

			got := f.run(path, "--preflight-done", tc.hash(digestOf(t, path)), "--preflight-route", "glm-5.3="+f.layer("glm.yaml", 0o600))
			captures := f.runner.CallsOf("capture")

			if got.code != 0 || len(captures) != 1 || captures[0][len(captures[0])-1] != "--preflight-verified" {
				t.Fatalf("the capture should run once, recorded as verified, got %d, stderr: %s", got.code, got.stderr)
			}

			if len(f.runner.CallsOf("dry-run")) != tc.dryRuns {
				t.Errorf("the pre-flight should run only where the hash differs, got: %v", f.runner.Calls)
			}

			if !strings.Contains(got.stderr, tc.says) {
				t.Errorf("the caller should learn whether its pre-flight stood, got: %s", got.stderr)
			}

			if tc.dryRuns == 0 && strings.Contains(got.stdout, "AGENTIC_CAPTURE_ROUTE") {
				t.Errorf("no route answered, so none should be named, got: %q", got.stdout)
			}
		})
	}

	t.Run("a mismatch names the draft's own hash", func(t *testing.T) {
		f := newFixture(t, "")
		path := f.draft("capture/full.md")

		got := f.run(path, "--preflight-done", strings.Repeat("0", 64))
		if !strings.Contains(got.stderr, "the draft hashes to "+digestOf(t, path)+"; running the pre-flight\n") {
			t.Errorf("the line should name the hash the caller should have sent, got: %s", got.stderr)
		}
	})

	t.Run("the draft lint still blocks a match", func(t *testing.T) {
		f := newFixture(t, "grounding/full.yaml")
		path := f.draft("capture/full.md")
		f.runner.Lint = process.Result{Code: 1, Stdout: "x:1:1:House.Passive:passive voice\n"}

		got := f.run(path, "--preflight-done", digestOf(t, path))
		if got.code != 1 || len(f.runner.CallsOf("capture")) != 0 || len(f.runner.CallsOf("dry-run")) != 0 {
			t.Errorf("a lint finding should stop the run, a caller's pre-flight notwithstanding, got %d, stderr: %s", got.code, got.stderr)
		}
	})
}

const schemaRefused = "sdd: refusing to write — graph incompatible with this binary\n" +
	"  graph schema version 1 is older than this binary's schema version 2; run `sdd migrate` to upgrade the graph\n"

func TestAGraphOnAnOlderSchemaNeedsMigrateNotADraftEdit(t *testing.T) {
	t.Run("at the pre-flight, with a route named", func(t *testing.T) {
		f := newFixture(t, "grounding/area-prefix.yaml")
		f.runner.DryRuns = []process.Result{{Code: 1, Stderr: schemaRefused}}

		got := f.routed("glm-5.3=" + f.layer("glm.yaml", 0o600))
		if got.code != 1 || !strings.Contains(got.stderr, "the graph needs `sdd migrate` before sdd writes to it; the draft did not cause this") {
			t.Errorf("the refusal should name the migration, not the draft, got: %+v", got)
		}

		if !strings.Contains(got.stderr, "graph schema version 1 is older") {
			t.Errorf("sdd's own reason should follow, got: %+v", got)
		}

		if strings.Contains(got.stderr, "pre-flight refused the draft") {
			t.Errorf("the draft should not be blamed, got: %+v", got)
		}

		if len(f.runner.CallsOf("dry-run")) != 1 || len(f.runner.CallsOf("capture")) != 0 {
			t.Errorf("nothing should be retried, routed or captured, got: %v", f.runner.Calls)
		}

		if len(f.pauses.Waited) != 0 {
			t.Errorf("a schema refusal should never wait for a retry, got: %v", f.pauses.Waited)
		}
	})

	t.Run("at the capture step", func(t *testing.T) {
		f := newFixture(t, "grounding/area-prefix.yaml")
		f.runner.Capture = process.Result{Code: 1, Stderr: schemaRefused}

		got := f.run(f.draft("capture/full.md"))
		if got.code != 1 || !strings.Contains(got.stderr, "the graph needs `sdd migrate` before sdd writes to it; the draft did not cause this") {
			t.Errorf("the refusal should name the migration, not the draft, got: %+v", got)
		}

		if strings.Count(got.stderr, "graph schema version 1 is older") != 1 {
			t.Errorf("sdd's own reason should be printed once, got: %+v", got)
		}

		if len(f.runner.CallsOf("capture")) != 1 {
			t.Errorf("the capture should run once, got: %v", f.runner.Calls)
		}
	})
}

func TestCaptureNeedsTheSddFork(t *testing.T) {
	cases := []struct {
		name    string
		version process.Result
		fails   bool
		fork    bool
	}{
		{name: "a fork release", version: process.Result{Stdout: "sdd version 0.18.0-alpha.2.fza.22\n"}, fork: true},
		{name: "a local fork build", version: process.Result{Stdout: "sdd version 0.18.0-alpha.2+fza21\n"}, fork: true},
		{name: "upstream", version: process.Result{Stdout: "sdd version 0.17.0\n"}},
		{name: "a dev build", version: process.Result{Stdout: "sdd version dev\n"}},
		{name: "another program printing fza", version: process.Result{Stdout: "fza-tool 1.0\n"}},
		{name: "a failing fork", version: process.Result{Stdout: "sdd version 0.18.0-alpha.2.fza.22\n", Stderr: "panic: broken\n", Code: 2}},
		{name: "no sdd at all", fails: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "grounding/area-prefix.yaml")
			f.runner.Version = tc.version
			f.runner.VersionFails = tc.fails

			got := f.run(f.draft("capture/full.md"))

			if tc.fork {
				if got.code != 0 || len(f.runner.CallsOf("capture")) != 1 {
					t.Errorf("the fork should capture, got: %+v", got)
				}

				return
			}

			if got.code != 1 || !strings.Contains(got.stderr, "agentic-capture needs sdd built from github.com/fza/sdd") ||
				!strings.Contains(got.stderr, "brew install fza/tap/sdd") {
				t.Errorf("another sdd should be refused, naming the fork and its install, got: %+v", got)
			}

			if len(f.runner.Calls) != 1 {
				t.Errorf("nothing should run after the version, got: %v", f.runner.Calls)
			}
		})
	}
}

func TestCaptureOffOnTheMachine(t *testing.T) {
	cases := []struct {
		options string
		says    string
	}{
		{`"capture_enabled":false`, "agentic-capture: off on this machine (plugin option capture_enabled)"},
		{`"grounding_gate_enabled":false`, "agentic-capture: off while grounding gate is off (plugin option grounding_gate_enabled)"},
	}

	for _, tc := range cases {
		t.Run(tc.options, func(t *testing.T) {
			root := t.TempDir()
			settings := filepath.Join(root, "settings.json")
			test.WriteFile(t, settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{`+tc.options+`}}}}`)

			runner := &test.Runner{}
			captured := &test.Captured{}
			code := capture.Main(context.Background(), []string{"entry.md"},
				capture.Env{WorkingDir: root, Runner: runner, Settings: []string{settings}}, captured.Streams(""))

			if code != 1 || strings.TrimSpace(captured.Err.String()) != tc.says || len(runner.Calls) != 0 {
				t.Errorf("capture should refuse, naming the option, and run nothing, got %d %q", code, captured.Err.String())
			}
		})
	}
}
