package grounding_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/grounding"
	"github.com/fza/agentic-helpers/source/hooks/internal/process"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const (
	stagedName   = "body.md"
	stagedAsked  = "Stage the file again with `mcp__sdd__stage_attachment`, or report the body as `body`."
	stagedLinted = "  body:1:5:Vale.Spelling:'grnd'"
)

func stage(input map[string]any) map[string]any {
	return mcp("stage_attachment", with(input, "name", stagedName))
}

// stagedAs is sdd's answer to a staging as Claude Code hands it on, its JSON
// text as one string: the handle, and the sha256 of the bytes it staged.
func stagedAs(t *testing.T, text string) string {
	t.Helper()

	sum := sha256.Sum256([]byte(text))

	answer, err := json.Marshal(map[string]any{"handle": stagedName, "sha256": hex.EncodeToString(sum[:])})
	if err != nil {
		t.Fatalf("encoding the answer: %v", err)
	}

	return string(answer)
}

func byHandle() map[string]any {
	return report(map[string]any{"bodyHandle": stagedName, "topics": []any{"area-hooks"}})
}

// lintAnswering makes the project's lint answer with answer, its `{body}`
// standing for the file it read, and returns what it read on each run.
func lintAnswering(env grounding.Env, answer process.Result) *[]string {
	read := linted(env)
	runner := env.Runner.(*test.Runner)
	before := runner.Before
	runner.Before = func(argv []string) {
		before(argv)

		if argv[0] == "sh" {
			runner.Lint = answer
			runner.Lint.Stdout = strings.ReplaceAll(answer.Stdout, "{body}", argv[len(argv)-1])
		}
	}

	return read
}

func TestEngineCaptureLintsAStagedBody(t *testing.T) {
	finding := process.Result{Code: 1, Stdout: "{body}:1:5:Vale.Spelling:'grnd'\n"}

	cases := []struct {
		name   string
		staged func(t *testing.T, env grounding.Env) []map[string]any
		gated  map[string]any
		answer process.Result
		read   []string
		says   []string
	}{
		{
			name: "a staged body with findings is refused naming them",
			staged: func(t *testing.T, _ grounding.Env) []map[string]any {
				return []map[string]any{answered(stage(map[string]any{"content": "Use grnd here."}), stagedAs(t, "Use grnd here."))}
			},
			gated:  byHandle(),
			answer: finding,
			read:   []string{"Use grnd here.\n"},
			says:   []string{"draft lint (`draft_lint` in `.sdd/grounding.yaml`) reports", stagedLinted},
		},
		{
			name: "a clean staged body passes",
			staged: func(t *testing.T, _ grounding.Env) []map[string]any {
				return []map[string]any{answered(stage(map[string]any{"content": "Clean text."}), stagedAs(t, "Clean text."))}
			},
			gated: byHandle(),
			read:  []string{"Clean text.\n"},
		},
		{
			name: "a start naming a handle is linted",
			staged: func(t *testing.T, _ grounding.Env) []map[string]any {
				return []map[string]any{answered(stage(map[string]any{"content": "Use grnd here."}), stagedAs(t, "Use grnd here."))}
			},
			gated:  startCapture(map[string]any{"bodyHandle": stagedName, "topics": []any{"area-hooks"}}),
			answer: finding,
			read:   []string{"Use grnd here.\n"},
			says:   []string{stagedLinted},
		},
		{
			name: "a handle in a chooser answer's fields is linted",
			staged: func(t *testing.T, _ grounding.Env) []map[string]any {
				return []map[string]any{answered(stage(map[string]any{"content": "Use grnd here."}), stagedAs(t, "Use grnd here."))}
			},
			gated:  report(map[string]any{"chooser": "playback", "choice": "adjust", "fields": map[string]any{"bodyHandle": stagedName}}),
			answer: finding,
			read:   []string{"Use grnd here.\n"},
			says:   []string{stagedLinted},
		},
		{
			name: "a staged file edited by patches is linted as patched",
			staged: func(t *testing.T, _ grounding.Env) []map[string]any {
				return []map[string]any{
					answered(stage(map[string]any{"content": "One two three."}), stagedAs(t, "One two three.")),
					answered(stage(map[string]any{"patches": []any{map[string]any{"old": "two", "new": "zwei"}}}), stagedAs(t, "One zwei three.")),
					answered(stage(map[string]any{"patches": []any{map[string]any{"old": "three", "new": "drei"}}}), stagedAs(t, "One zwei drei.")),
				}
			},
			gated: byHandle(),
			read:  []string{"One zwei drei.\n"},
		},
		{
			name: "a patch sdd refuses leaves the record unchanged",
			staged: func(t *testing.T, _ grounding.Env) []map[string]any {
				return []map[string]any{
					answered(stage(map[string]any{"content": "One two."}), stagedAs(t, "One two.")),
					answered(stage(map[string]any{"patches": []any{map[string]any{"old": "three", "new": "drei"}}}), map[string]any{"is_error": true}),
					answered(stage(map[string]any{"patches": []any{map[string]any{"old": "two", "new": "zwei"}}}), []any{map[string]any{"type": "text", "text": "pair 1: old text not found"}}),
				}
			},
			gated: byHandle(),
			read:  []string{"One two.\n"},
		},
		{
			name: "a patch sdd applied that the gate cannot drops the record",
			staged: func(t *testing.T, _ grounding.Env) []map[string]any {
				return []map[string]any{
					answered(stage(map[string]any{"content": "One two."}), stagedAs(t, "One two.")),
					answered(stage(map[string]any{"patches": []any{map[string]any{"old": "three", "new": "drei"}}}), stagedAs(t, "One two drei.")),
				}
			},
			gated: byHandle(),
			says:  []string{stagedAsked},
		},
		{
			name: "a patch the gate cannot apply drops the record where sdd answers no hash",
			staged: func(*testing.T, grounding.Env) []map[string]any {
				return []map[string]any{
					answered(stage(map[string]any{"content": "One two."}), map[string]any{"handle": stagedName}),
					answered(stage(map[string]any{"patches": []any{map[string]any{"old": "three", "new": "drei"}}}), map[string]any{"handle": stagedName}),
				}
			},
			gated: byHandle(),
			says:  []string{stagedAsked},
		},
		{
			name: "text whose hash differs from sdd's is not recorded",
			staged: func(t *testing.T, _ grounding.Env) []map[string]any {
				return []map[string]any{answered(stage(map[string]any{"content": "Clean text."}), stagedAs(t, "Other text."))}
			},
			gated: byHandle(),
			says:  []string{stagedAsked},
		},
		{
			name: "an answer carrying no hash records the text as staged",
			staged: func(*testing.T, grounding.Env) []map[string]any {
				return []map[string]any{answered(stage(map[string]any{"content": "Clean text."}), map[string]any{"handle": stagedName})}
			},
			gated: byHandle(),
			read:  []string{"Clean text.\n"},
		},
		{
			name: "a body staged by path is linted from that file's text",
			staged: func(t *testing.T, env grounding.Env) []map[string]any {
				path := filepath.Join(env.ProjectDir, "drafts", "body.md")
				test.WriteFile(t, path, "Use grnd from the file.\n")

				return []map[string]any{answered(stage(map[string]any{"path": path}), stagedAs(t, "Use grnd from the file.\n"))}
			},
			gated:  byHandle(),
			answer: finding,
			read:   []string{"Use grnd from the file.\n\n"},
			says:   []string{stagedLinted},
		},
		{
			name: "a path that cannot be read records nothing",
			staged: func(t *testing.T, env grounding.Env) []map[string]any {
				return []map[string]any{answered(stage(map[string]any{"path": filepath.Join(env.ProjectDir, "absent.md")}), stagedAs(t, "Clean text."))}
			},
			gated: byHandle(),
			says:  []string{stagedAsked},
		},
		{
			name: "a header block is not linted as body",
			staged: func(t *testing.T, _ grounding.Env) []map[string]any {
				text := "---\nlayer: tactical\ntopics: [area-hooks]\n---\nBody line.\n"

				return []map[string]any{answered(stage(map[string]any{"content": text}), stagedAs(t, text))}
			},
			gated: byHandle(),
			read:  []string{"Body line.\n\n"},
		},
		{
			name: "a header block behind a byte order mark and CRLF lines is not linted as body",
			staged: func(t *testing.T, _ grounding.Env) []map[string]any {
				text := "\uFEFF---\r\nlayer: tactical\r\n---\r\nBody line.\r\n"

				return []map[string]any{answered(stage(map[string]any{"content": text}), stagedAs(t, text))}
			},
			gated: byHandle(),
			read:  []string{"Body line.\r\n\n"},
		},
		{
			name: "a `---` line that does not open the file is body",
			staged: func(t *testing.T, _ grounding.Env) []map[string]any {
				text := "Title\n---\nBody line."

				return []map[string]any{answered(stage(map[string]any{"content": text}), stagedAs(t, text))}
			},
			gated: byHandle(),
			read:  []string{"Title\n---\nBody line.\n"},
		},
		{
			name: "an opening line with no closing one leaves the whole file body",
			staged: func(t *testing.T, _ grounding.Env) []map[string]any {
				text := "---\nNo header closes."

				return []map[string]any{answered(stage(map[string]any{"content": text}), stagedAs(t, text))}
			},
			gated: byHandle(),
			read:  []string{"---\nNo header closes.\n"},
		},
		{
			name: "a whole body beside a staged file is linted as before",
			staged: func(t *testing.T, _ grounding.Env) []map[string]any {
				return []map[string]any{answered(stage(map[string]any{"content": "Staged text."}), stagedAs(t, "Staged text."))}
			},
			gated: report(map[string]any{"body": "Reported text."}),
			read:  []string{"Reported text.\n"},
		},
		{
			name: "a patch after the capture dropped its handle edits the staged body",
			staged: func(t *testing.T, _ grounding.Env) []map[string]any {
				return []map[string]any{
					answered(stage(map[string]any{"content": "One two."}), stagedAs(t, "One two.")),
					answered(byHandle(), served(t, captureInstance, "running")),
				}
			},
			gated: report(map[string]any{"bodyHandle": nil, "bodyPatch": []any{map[string]any{"old": "two", "new": "zwei"}}}),
			read:  []string{"One zwei.\n"},
		},
		{
			name: "a patch after a handle the gate holds no text for asks for the whole body",
			staged: func(t *testing.T, _ grounding.Env) []map[string]any {
				return []map[string]any{
					answered(report(map[string]any{"body": "One two."}), served(t, captureInstance, "running")),
					answered(byHandle(), served(t, captureInstance, "running")),
				}
			},
			gated: report(map[string]any{"bodyHandle": nil, "bodyPatch": []any{map[string]any{"old": "two", "new": "zwei"}}}),
			says:  []string{"Report `body` in full once"},
		},
		{
			name: "another engine tool's answer naming the handle changes nothing",
			staged: func(t *testing.T, _ grounding.Env) []map[string]any {
				return []map[string]any{
					answered(stage(map[string]any{"content": "Clean text."}), stagedAs(t, "Clean text.")),
					answered(mcp("read_attachment", map[string]any{"handle": stagedName}), map[string]any{"handle": stagedName, "content": "Clean text."}),
				}
			},
			gated: byHandle(),
			read:  []string{"Clean text.\n"},
		},
		{
			name: "a staging whose input does not decode records nothing",
			staged: func(t *testing.T, _ grounding.Env) []map[string]any {
				return []map[string]any{answered(stage(map[string]any{"content": "Clean text.", "patches": "two"}), stagedAs(t, "Clean text."))}
			},
			gated: byHandle(),
			says:  []string{stagedAsked},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := started(t, "lint.yaml", nil)
			for _, call := range tc.staged(t, env) {
				passes(t, run(t, env, "record", call))
			}

			read := lintAnswering(env, tc.answer)

			got := run(t, env, "gate", tc.gated)
			if !slices.Equal(*read, tc.read) {
				t.Errorf("the lint should read the body the staged file holds, got %q", *read)
			}

			if tc.says == nil {
				passes(t, got)

				return
			}

			refuses(t, got, tc.says...)
		})
	}
}

func TestAnEmptyHandleLeavesTheBodyLintedAsBefore(t *testing.T) {
	env := started(t, "lint.yaml", nil)
	read := lintAnswering(env, process.Result{})

	passes(t, run(t, env, "gate", report(map[string]any{"bodyHandle": "", "body": "Reported text."})))

	if !slices.Equal(*read, []string{"Reported text.\n"}) {
		t.Errorf("a report naming no handle should have its body linted as before, got %q", *read)
	}
}

func TestAStagingBeforeAnyLedgerIsRecorded(t *testing.T) {
	env := configured(t, "lint.yaml")
	passes(t, run(t, env, "record", answered(stage(map[string]any{"content": "Clean text."}), stagedAs(t, "Clean text."))))

	ground(t, env)
	record(t, env, bash(`sdd view --layout "active:as-counts"`))
	record(t, env, answered(startCapture(nil), served(t, captureInstance, "running")))

	read := lintAnswering(env, process.Result{})
	passes(t, run(t, env, "gate", byHandle()))

	if !slices.Equal(*read, []string{"Clean text.\n"}) {
		t.Errorf("a staging that opened the session's ledger should be linted, got %q", *read)
	}
}

func TestEngineCaptureAsksForAnUnrecordedHandleOnce(t *testing.T) {
	cases := []struct {
		name  string
		gated map[string]any
	}{
		{name: "a report", gated: byHandle()},
		{name: "start params", gated: startCapture(map[string]any{"bodyHandle": stagedName, "topics": []any{"area-hooks"}})},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := started(t, "lint.yaml", nil)
			read := lintAnswering(env, process.Result{})

			refuses(t, run(t, env, "gate", tc.gated), "GROUNDING GATE: refused. This `bodyHandle` names `body.md`, a staged file the gate holds no record of", stagedAsked)
			passes(t, run(t, env, "gate", tc.gated))

			if len(*read) != 0 {
				t.Errorf("a handle the gate holds no text for should go unlinted, got %q", *read)
			}
		})
	}
}

func TestEngineCaptureLintsARestagedHandle(t *testing.T) {
	env := started(t, "lint.yaml", nil)
	read := lintAnswering(env, process.Result{})

	refuses(t, run(t, env, "gate", byHandle()), stagedAsked)
	record(t, env, answered(stage(map[string]any{"content": "Clean text."}), stagedAs(t, "Clean text.")))
	passes(t, run(t, env, "gate", byHandle()))

	if !slices.Equal(*read, []string{"Clean text.\n"}) {
		t.Errorf("a file staged again after the refusal should be linted, got %q", *read)
	}
}

func TestStagingReadsEveryAnswerShape(t *testing.T) {
	answer := map[string]any{"handle": stagedName}
	text, err := json.Marshal(answer)
	if err != nil {
		t.Fatalf("encoding the answer: %v", err)
	}

	cases := []struct {
		name     string
		response any
		records  bool
	}{
		{name: "the answer's JSON as one string", response: string(text), records: true},
		{name: "the answer itself", response: answer, records: true},
		{name: "structured content", response: map[string]any{"structuredContent": answer}, records: true},
		{name: "a text block in the result", response: map[string]any{"content": []any{map[string]any{"type": "text", "text": string(text)}}}, records: true},
		{name: "the bare list of blocks", response: []any{map[string]any{"type": "text", "text": string(text)}}, records: true},
		{name: "an answer naming no handle", response: map[string]any{"sha256": "00"}},
		{name: "an answer that is no JSON", response: "staged"},
		{name: "a text block that is no JSON", response: []any{map[string]any{"type": "text", "text": "staged body.md"}}},
		{name: "a number", response: 7},
		{name: "a failed staging", response: map[string]any{"is_error": true, "handle": stagedName}},
		{name: "no answer"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := started(t, "lint.yaml", nil)
			staging := stage(map[string]any{"content": "Clean text."})
			if tc.response != nil {
				staging = answered(staging, tc.response)
			}

			passes(t, run(t, env, "record", staging))
			passes(t, run(t, env, "gate", stage(map[string]any{"content": "Clean text."})))

			ledger, err := os.ReadFile(filepath.Join(env.ProjectDir, ".sdd", "tmp", "grounding-gate", "s1.json"))
			if err != nil {
				t.Fatalf("reading the ledger: %v", err)
			}

			if strings.Contains(string(ledger), "Clean text.") != tc.records {
				t.Errorf("the ledger should hold the staged text only for an answer naming its handle, got %s", ledger)
			}

			got := run(t, env, "gate", byHandle())
			if tc.records {
				passes(t, got)

				return
			}

			refuses(t, got, stagedAsked)
		})
	}
}

func TestStagingWithoutDraftLint(t *testing.T) {
	env := started(t, "area-prefix.yaml", nil)
	read := lintAnswering(env, process.Result{})

	record(t, env, answered(stage(map[string]any{"content": "Use grnd here."}), stagedAs(t, "Use grnd here.")))
	passes(t, run(t, env, "gate", byHandle()))

	ledger, err := os.ReadFile(filepath.Join(env.ProjectDir, ".sdd", "tmp", "grounding-gate", "s1.json"))
	if err != nil {
		t.Fatalf("reading the ledger: %v", err)
	}

	if strings.Contains(string(ledger), "Use grnd here.") {
		t.Errorf("a project without a draft lint should record no staged text, got %s", ledger)
	}

	test.WriteFile(t, filepath.Join(env.ProjectDir, ".sdd", "grounding.yaml"), test.Fixture(t, "grounding/lint.yaml"))
	refuses(t, run(t, env, "gate", byHandle()), stagedAsked)

	if len(*read) != 0 {
		t.Errorf("a project without a draft lint should lint nothing, got %q", *read)
	}
}

func TestALedgerWrittenBeforeStagingStillReads(t *testing.T) {
	env := configured(t, "lint.yaml")
	ledger := filepath.Join(env.ProjectDir, ".sdd", "tmp", "grounding-gate", "s1.json")
	test.WriteFile(t, ledger, test.Fixture(t, "ledgers/before-staging.json"))
	read := lintAnswering(env, process.Result{})

	passes(t, run(t, env, "gate", report(map[string]any{"bodyPatch": []any{map[string]any{"old": "two", "new": "zwei"}}})))

	if !slices.Equal(*read, []string{"One zwei.\n"}) {
		t.Errorf("a capture an earlier ledger holds should keep its body for a patch, got %q", *read)
	}

	record(t, env, answered(stage(map[string]any{"content": "Clean text."}), stagedAs(t, "Clean text.")))

	data, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("reading the ledger: %v", err)
	}

	var written map[string]json.RawMessage

	err = json.Unmarshal(data, &written)
	if err != nil {
		t.Fatalf("decoding the ledger: %v", err)
	}

	for _, kept := range []string{"captures", "carried", "query", "listings"} {
		if written[kept] == nil {
			t.Errorf("a staging should keep what the ledger held, lost %s in %s", kept, data)
		}
	}

	if !strings.Contains(string(written["captures"]), "One two.") || !strings.Contains(string(written["carried"]), "another tool") {
		t.Errorf("a staging should keep the captures and the keys another tool wrote, got %s", data)
	}
}
