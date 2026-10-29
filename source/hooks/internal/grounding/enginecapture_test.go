package grounding_test

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/grounding"
	"github.com/fza/agentic-helpers/source/hooks/internal/process"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const captureInstance = "inst-7"

// served is an engine answer as Claude Code hands it to a hook: the serve's
// JSON text as one string.
func served(t *testing.T, instance string, status string) string {
	t.Helper()

	serve, err := json.Marshal(map[string]any{"instance": instance, "procedure": "capture", "status": status})
	if err != nil {
		t.Fatalf("encoding the serve: %v", err)
	}

	return string(serve)
}

func startCapture(params map[string]any) map[string]any {
	return mcp("start_procedure", map[string]any{"canonical": "capture", "params": params})
}

func report(fields map[string]any) map[string]any {
	return mcp("next", map[string]any{"instance": captureInstance, "report": fields})
}

func answered(payload map[string]any, response any) map[string]any {
	return with(payload, "tool_response", response)
}

// started is a project whose capture `captureInstance` started with the given
// params and went through.
func started(t *testing.T, fixture string, params map[string]any) grounding.Env {
	t.Helper()

	env := configured(t, fixture)
	ground(t, env)
	record(t, env, bash(`sdd view --layout "active:as-counts"`))
	record(t, env, answered(startCapture(params), served(t, captureInstance, "running")))

	return env
}

// linted is what the project's lint read on each run.
func linted(env grounding.Env) *[]string {
	read := &[]string{}
	runner := env.Runner.(*test.Runner)
	runner.Before = func(argv []string) {
		if argv[0] != "sh" {
			return
		}

		data, err := os.ReadFile(argv[len(argv)-1])
		if err == nil {
			*read = append(*read, string(data))
		}
	}

	return read
}

func TestEngineCaptureTopicPrefix(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
		params  map[string]any
		calls   []map[string]any
		gated   map[string]any
		says    string
	}{
		{
			name:    "start params without a prefixed topic are refused",
			fixture: "area-prefix.yaml",
			gated:   startCapture(map[string]any{"topics": []any{"hooks"}}),
			says:    "These `topics` carry none opening with `area-`",
		},
		{
			name:    "start params carrying a prefixed topic pass",
			fixture: "area-prefix.yaml",
			gated:   startCapture(map[string]any{"topics": []any{"hooks", "area-hooks"}}),
		},
		{
			name:    "a report without a prefixed topic is refused",
			fixture: "area-prefix.yaml",
			gated:   report(map[string]any{"body": "Text.", "topics": []any{"hooks"}}),
			says:    "These `topics` carry none opening with `area-`",
		},
		{
			name:    "an adjust carrying topics in its fields is held to the prefix",
			fixture: "area-prefix.yaml",
			gated:   report(map[string]any{"chooser": "playback", "choice": "adjust", "fields": map[string]any{"topics": []any{"hooks"}}}),
			says:    "These `topics` carry none opening with `area-`",
		},
		{
			name:    "a confirm is refused while no prefixed topic was seen",
			fixture: "area-prefix.yaml",
			calls:   []map[string]any{report(map[string]any{"body": "Text."})},
			gated:   report(map[string]any{"chooser": "playback", "choice": "confirm"}),
			says:    "holds no topic opening with `area-`",
		},
		{
			name:    "a confirm passes once a report carried a prefixed topic",
			fixture: "area-prefix.yaml",
			calls:   []map[string]any{report(map[string]any{"topics": []any{"area-hooks"}})},
			gated:   report(map[string]any{"chooser": "playback", "choice": "confirm"}),
		},
		{
			name:    "a confirm passes on topics the start params seeded",
			fixture: "area-prefix.yaml",
			params:  map[string]any{"topics": []any{"area-hooks"}},
			gated:   report(map[string]any{"chooser": "playback", "choice": "confirm"}),
		},
		{
			name:    "later topics replace the seeded ones",
			fixture: "area-prefix.yaml",
			params:  map[string]any{"topics": []any{"area-hooks"}},
			calls:   []map[string]any{report(map[string]any{"chooser": "guideReview", "choice": "revise", "fields": map[string]any{"topics": []any{"hooks"}}})},
			gated:   report(map[string]any{"chooser": "playback", "choice": "confirm"}),
			says:    "holds no topic opening with `area-`",
		},
		{
			name:    "a confirm to a capture the gate never saw start passes",
			fixture: "area-prefix.yaml",
			gated:   mcp("next", map[string]any{"instance": "other", "report": map[string]any{"chooser": "playback", "choice": "confirm"}}),
		},
		{
			name:    "a project setting neither key passes a confirm",
			fixture: "enabled.yaml",
			gated:   report(map[string]any{"chooser": "playback", "choice": "confirm"}),
		},
		{
			name:    "a project setting neither key passes unprefixed topics",
			fixture: "enabled.yaml",
			gated:   report(map[string]any{"topics": []any{"hooks"}}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := started(t, tc.fixture, tc.params)
			for _, call := range tc.calls {
				record(t, env, answered(call, served(t, captureInstance, "running")))
			}

			got := run(t, env, "gate", tc.gated)
			if tc.says == "" {
				passes(t, got)

				return
			}

			refuses(t, got, tc.says)
		})
	}
}

func TestEngineCaptureReadsEveryServeShape(t *testing.T) {
	serve := map[string]any{"instance": captureInstance, "procedure": "capture", "status": "running"}

	cases := []struct {
		name     string
		response func(t *testing.T) any
	}{
		{name: "the serve's JSON as one string", response: func(t *testing.T) any { return served(t, captureInstance, "running") }},
		{name: "the serve's JSON in a text block", response: func(t *testing.T) any {
			return []any{map[string]any{"type": "text", "text": served(t, captureInstance, "running")}}
		}},
		{name: "the serve as structured content", response: func(*testing.T) any { return map[string]any{"structuredContent": serve} }},
		{name: "the serve itself", response: func(*testing.T) any { return serve }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := configured(t, "area-prefix.yaml")
			ground(t, env)
			record(t, env, bash(`sdd view --layout "active:as-counts"`))
			record(t, env, answered(startCapture(nil), tc.response(t)))

			got := run(t, env, "gate", report(map[string]any{"body": "Text.", "topics": []any{"hooks"}}))
			refuses(t, got, "These `topics` carry none opening with `area-`")
		})
	}
}

func TestEngineCaptureLintsTheBody(t *testing.T) {
	cases := []struct {
		name   string
		calls  []map[string]any
		gated  map[string]any
		answer process.Result
		read   []string
		says   []string
	}{
		{
			name:   "a whole body with findings is refused naming them",
			gated:  report(map[string]any{"body": "Use grnd here."}),
			answer: process.Result{Code: 1, Stdout: "{body}:1:5:Vale.Spelling:Did you really mean 'grnd'?\n"},
			read:   []string{"Use grnd here.\n"},
			says:   []string{"draft lint (`draft_lint` in `.sdd/grounding.yaml`) reports", "  body:1:5:Vale.Spelling:Did you really mean 'grnd'?"},
		},
		{
			name:  "a clean whole body passes",
			gated: report(map[string]any{"body": "Clean text."}),
			read:  []string{"Clean text.\n"},
		},
		{
			name:   "a start seeding a body is linted",
			gated:  startCapture(map[string]any{"body": "Use grnd here.", "topics": []any{"area-hooks"}}),
			answer: process.Result{Code: 1, Stdout: "{body}:1:5:Vale.Spelling:'grnd'\n"},
			read:   []string{"Use grnd here.\n"},
			says:   []string{"  body:1:5:Vale.Spelling:'grnd'"},
		},
		{
			name:  "a patch is applied to the last whole body and the result linted",
			calls: []map[string]any{report(map[string]any{"body": "First line.\nSecond line."})},
			gated: report(map[string]any{"bodyPatch": []any{map[string]any{"old": "Second", "new": "Third"}}}),
			read:  []string{"First line.\nThird line.\n"},
		},
		{
			name: "patches build on each other",
			calls: []map[string]any{
				report(map[string]any{"body": "One two three."}),
				report(map[string]any{"bodyPatch": []any{map[string]any{"old": "two", "new": "zwei"}}}),
			},
			gated: report(map[string]any{"chooser": "playback", "choice": "adjust", "fields": map[string]any{"bodyPatch": []any{map[string]any{"old": "three", "new": "drei"}}}}),
			read:  []string{"One zwei drei.\n"},
		},
		{
			name:  "a patch whose old text is missing passes unlinted",
			calls: []map[string]any{report(map[string]any{"body": "One two."})},
			gated: report(map[string]any{"bodyPatch": []any{map[string]any{"old": "three", "new": "drei"}}}),
		},
		{
			name:  "a patch whose old text is doubled passes unlinted",
			calls: []map[string]any{report(map[string]any{"body": "two and two."})},
			gated: report(map[string]any{"bodyPatch": []any{map[string]any{"old": "two", "new": "zwei"}}}),
		},
		{
			name:  "a patch with no recorded body is refused asking for the whole body",
			gated: report(map[string]any{"bodyPatch": []any{map[string]any{"old": "two", "new": "zwei"}}}),
			says:  []string{"Report `body` in full once"},
		},
		{
			name:   "a lint failing silently refuses: the body went unread",
			gated:  report(map[string]any{"body": "Text."}),
			answer: process.Result{Code: 2, Stderr: "E100 no config"},
			read:   []string{"Text.\n"},
			says:   []string{"did not read the body this call leaves", "E100 no config"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := started(t, "lint.yaml", nil)
			for _, call := range tc.calls {
				record(t, env, answered(call, served(t, captureInstance, "running")))
			}

			read := linted(env)
			runner := env.Runner.(*test.Runner)
			before := runner.Before
			runner.Before = func(argv []string) {
				before(argv)

				if argv[0] == "sh" {
					runner.Lint = tc.answer
					runner.Lint.Stdout = strings.ReplaceAll(tc.answer.Stdout, "{body}", argv[len(argv)-1])
				}
			}

			got := run(t, env, "gate", tc.gated)
			if !slices.Equal(*read, tc.read) {
				t.Errorf("the lint should read the body the call leaves, got %q", *read)
			}

			if tc.says == nil {
				passes(t, got)

				return
			}

			refuses(t, got, tc.says...)
		})
	}
}

func TestEngineCaptureAsksForTheWholeBodyOnce(t *testing.T) {
	env := started(t, "lint.yaml", nil)
	patch := report(map[string]any{"bodyPatch": []any{map[string]any{"old": "two", "new": "zwei"}}})

	refuses(t, run(t, env, "gate", patch), "Report `body` in full once")
	passes(t, run(t, env, "gate", patch))
}

func TestEngineCaptureReadsEveryResponseShape(t *testing.T) {
	serve := map[string]any{"instance": captureInstance, "procedure": "capture", "status": "running"}
	text, err := json.Marshal(serve)
	if err != nil {
		t.Fatalf("encoding the serve: %v", err)
	}

	cases := []struct {
		name     string
		response any
	}{
		{name: "the serve itself", response: serve},
		{name: "structured content", response: map[string]any{"structuredContent": serve}},
		{name: "a text block in the result", response: map[string]any{"content": []any{map[string]any{"type": "text", "text": string(text)}}}},
		{name: "the bare list of blocks", response: []any{map[string]any{"type": "text", "text": string(text)}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := configured(t, "area-prefix.yaml")
			ground(t, env)
			record(t, env, answered(startCapture(nil), tc.response))

			refuses(t, run(t, env, "gate", report(map[string]any{"chooser": "playback", "choice": "confirm"})), "holds no topic opening with `area-`")
		})
	}
}

func TestEngineCaptureForgetsAnEndedCapture(t *testing.T) {
	cases := []struct {
		name     string
		response any
		forgets  bool
	}{
		{name: "a completed capture is forgotten", response: "completed", forgets: true},
		{name: "an abandoned capture is forgotten", response: "abandoned", forgets: true},
		{name: "a running capture is kept", response: "running"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := started(t, "area-prefix.yaml", nil)
			status, _ := tc.response.(string)
			record(t, env, answered(report(map[string]any{"fidelityNote": "holds"}), served(t, captureInstance, status)))

			got := run(t, env, "gate", report(map[string]any{"chooser": "playback", "choice": "confirm"}))
			if tc.forgets {
				passes(t, got)

				return
			}

			refuses(t, got, "holds no topic opening with `area-`")
		})
	}
}

func TestEngineCaptureRecordsOnlyWhatWentThrough(t *testing.T) {
	env := started(t, "area-prefix.yaml", nil)

	failed := answered(report(map[string]any{"topics": []any{"area-hooks"}}), map[string]any{"is_error": true})
	record(t, env, failed)

	refuses(t, run(t, env, "gate", report(map[string]any{"chooser": "playback", "choice": "confirm"})), "holds no topic opening with `area-`")
}
