package grounding_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/grounding"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const (
	literalRead  = "python3 scripts/graph.py search --term 'ssh_host_key_changed'"
	semanticRead = "python3 scripts/graph.py search --query 'a host key that changed'"
	areaRead     = `python3 scripts/graph.py view --layout "topic(area-hooks):as-list"`
	misspelled   = "python3 scripts/graph.py search --terms 'ssh_host_key_changed'"
)

type result struct {
	code   int
	stderr string
}

// areaProject is a project carrying a graph and an `area-` listing prefix.
func areaProject(t *testing.T) grounding.Env {
	t.Helper()

	env := bare(t)
	test.WriteFile(t, filepath.Join(env.ProjectDir, ".sdd", "grounding.yaml"), test.Fixture(t, "grounding/area-prefix.yaml"))

	return env
}

// bare is a project carrying a graph and no listing prefix.
func bare(t *testing.T) grounding.Env {
	t.Helper()

	root := t.TempDir()
	mkdir(t, filepath.Join(root, ".sdd"))

	return grounding.Env{ProjectDir: root, WorkingDir: root, Home: t.TempDir()}
}

func mkdir(t *testing.T, path string) {
	t.Helper()

	err := os.MkdirAll(path, 0o755)
	if err != nil {
		t.Fatalf("creating %s: %v", path, err)
	}
}

func run(t *testing.T, env grounding.Env, mode string, payload map[string]any) result {
	t.Helper()

	stdin, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encoding the payload: %v", err)
	}

	return runRaw(t, env, mode, string(stdin))
}

func runRaw(t *testing.T, env grounding.Env, mode string, stdin string) result {
	t.Helper()

	captured := &test.Captured{}
	code := grounding.Main(context.Background(), []string{mode}, env, captured.Streams(stdin))

	return result{code: code, stderr: captured.Err.String()}
}

func bash(command string) map[string]any {
	return map[string]any{"session_id": "s1", "tool_name": "Bash", "tool_input": map[string]any{"command": command}}
}

func ask(session string) map[string]any {
	return map[string]any{"session_id": session, "tool_name": "AskUserQuestion", "tool_input": map[string]any{"questions": []any{}}}
}

func edit(path string) map[string]any {
	return map[string]any{"session_id": "s1", "tool_name": "Edit", "tool_input": map[string]any{"file_path": path}}
}

func with(payload map[string]any, key string, value any) map[string]any {
	copied := map[string]any{key: value}
	for name, held := range payload {
		if name != key {
			copied[name] = held
		}
	}

	return copied
}

func record(t *testing.T, env grounding.Env, payloads ...map[string]any) {
	t.Helper()

	for _, payload := range payloads {
		run(t, env, "record", payload)
	}
}

func ground(t *testing.T, env grounding.Env) {
	t.Helper()

	record(t, env, bash(literalRead), bash(semanticRead), bash(areaRead))
}

func refuses(t *testing.T, got result, says string) {
	t.Helper()

	if got.code != 2 {
		t.Fatalf("the call should be refused, got exit %d, stderr: %s", got.code, got.stderr)
	}

	if !strings.Contains(got.stderr, says) {
		t.Errorf("the refusal should explain itself, got: %s", got.stderr)
	}
}

func passes(t *testing.T, got result) {
	t.Helper()

	if got.code != 0 {
		t.Errorf("the call should pass, got exit %d, stderr: %s", got.code, got.stderr)
	}
}

func TestQuestionNeedsEveryRead(t *testing.T) {
	cases := []struct {
		name    string
		reads   []string
		missing string
	}{
		{name: "no reads", missing: "neither read"},
		{name: "misspelled search alone", reads: []string{misspelled}, missing: "No `area-` listing ran this turn."},
		{name: "area listing alone", reads: []string{areaRead}, missing: "No `--query` search ran this turn."},
		{name: "semantic mode alone", reads: []string{semanticRead, areaRead}, missing: "No `--term` search ran this turn."},
		{name: "literal mode alone", reads: []string{literalRead, areaRead}, missing: "No `--query` search ran this turn."},
		{name: "show alone", reads: []string{"python3 scripts/graph.py show 20260829-122703-d-tac-kuz"}, missing: "neither read"},
		{name: "facet listing is no area", reads: []string{`python3 scripts/graph.py view --layout "topic(deployment):as-list"`, literalRead, semanticRead}, missing: "No `area-` listing"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := areaProject(t)
			for _, read := range tc.reads {
				record(t, env, bash(read))
			}

			refuses(t, run(t, env, "gate", ask("s1")), tc.missing)
		})
	}
}

func TestQuestionPassesOnEveryRead(t *testing.T) {
	env := areaProject(t)
	ground(t, env)

	passes(t, run(t, env, "gate", ask("s1")))
}

func TestNoRefusalSpellsAFlagTheToolDoesNotTake(t *testing.T) {
	for _, payload := range []map[string]any{ask("s1"), bash("python3 scripts/graph.py search --term x 2>/dev/null")} {
		got := run(t, areaProject(t), "gate", payload)

		if !strings.Contains(got.stderr, "--term ") || strings.Contains(got.stderr, "--terms") {
			t.Errorf("the refusal should name the real flag only, got: %s", got.stderr)
		}
	}
}

func TestDraftEdits(t *testing.T) {
	env := areaProject(t)

	refuses(t, run(t, env, "gate", edit("/x/.tmp/drafts/ssh-access/01-block.md")), "No `--query`")
	passes(t, run(t, env, "gate", edit("/x/docs/fdbox/07-cli.md")))
	passes(t, run(t, env, "gate", with(edit("/x/.tmp/drafts/ssh-access/01-block.md"), "tool_name", "Read")))
}

func ledgerWith(t *testing.T, env grounding.Env, draft string, history []map[string]string) {
	t.Helper()

	held, err := json.Marshal(map[string]any{"drafts": map[string]any{draft: map[string]any{"hash": "x", "history": history}}})
	if err != nil {
		t.Fatalf("encoding the ledger: %v", err)
	}

	test.WriteFile(t, filepath.Join(env.ProjectDir, ".sdd", "autopilot", "turns", "20260901-000000-d-tac-aaa.json"), string(held))
}

func TestVerifiedDrafts(t *testing.T) {
	t.Run("a fix to a verified draft owes no fresh reads", func(t *testing.T) {
		env := areaProject(t)
		draft := filepath.Join(env.ProjectDir, ".tmp", "drafts", "fix.md")
		ledgerWith(t, env, draft, []map[string]string{{"agent": "verify-1", "verdict": "findings"}})

		passes(t, run(t, env, "gate", edit(draft)))
	})

	t.Run("a registered draft nobody verified still owes the reads", func(t *testing.T) {
		env := areaProject(t)
		draft := filepath.Join(env.ProjectDir, ".tmp", "drafts", "new.md")
		ledgerWith(t, env, draft, []map[string]string{})

		refuses(t, run(t, env, "gate", edit(draft)), "GROUNDING GATE")
	})

	t.Run("another draft's verification opens nothing", func(t *testing.T) {
		env := areaProject(t)
		ledgerWith(t, env, filepath.Join(env.ProjectDir, ".tmp", "drafts", "other.md"), []map[string]string{{"agent": "verify-1", "verdict": "clean"}})

		refuses(t, run(t, env, "gate", edit(filepath.Join(env.ProjectDir, ".tmp", "drafts", "new.md"))), "GROUNDING GATE")
	})
}

func TestAnotherRepository(t *testing.T) {
	env := areaProject(t)
	other := filepath.Join(filepath.Dir(env.ProjectDir), filepath.Base(env.ProjectDir)+"-other")
	mkdir(t, filepath.Join(other, ".git"))

	passes(t, run(t, env, "gate", bash("cd "+other+" && sdd show abc --down 2")))

	for _, command := range []string{
		"cd /x && sdd show abc --down 2",
		"sdd show abc --down 2",
		"cd " + env.ProjectDir + " && sdd show abc --down 2",
		"cd " + env.ProjectDir + "/source && sdd search --term x 2>/dev/null",
		"cd source && sdd show abc --down 2",
	} {
		t.Run(command, func(t *testing.T) {
			refuses(t, run(t, env, "gate", bash(command)), "GROUNDING GATE")
		})
	}
}

func TestEvidenceIsPerTurnAndPerReader(t *testing.T) {
	t.Run("a new turn discards the evidence", func(t *testing.T) {
		env := areaProject(t)
		ground(t, env)
		passes(t, run(t, env, "gate", ask("s1")))

		run(t, env, "turn", map[string]any{"session_id": "s1"})

		refuses(t, run(t, env, "gate", ask("s1")), "neither read")
	})

	t.Run("one session never answers for another", func(t *testing.T) {
		env := areaProject(t)
		ground(t, env)

		passes(t, run(t, env, "gate", ask("s1")))
		refuses(t, run(t, env, "gate", ask("s2")), "neither read")
	})

	t.Run("a subagent's reads never open the session's gate", func(t *testing.T) {
		env := areaProject(t)
		for _, read := range []string{literalRead, semanticRead, areaRead} {
			record(t, env, with(bash(read), "agent_id", "a1"))
		}

		refuses(t, run(t, env, "gate", ask("s1")), "No `--query` search ran this turn.")
		passes(t, run(t, env, "gate", with(ask("s1"), "agent_id", "a1")))
		refuses(t, run(t, env, "gate", with(ask("s1"), "agent_id", "a2")), "neither read")
	})
}

func TestTheListingRecordsWhichAreaItRead(t *testing.T) {
	env := areaProject(t)
	record(t, env, bash(areaRead), bash(`python3 scripts/graph.py view --layout "topic(area-bare-host-deployment):as-counts"`), bash(areaRead))

	data, err := os.ReadFile(filepath.Join(env.ProjectDir, ".sdd", "autopilot", "turns", "s1.json"))
	if err != nil {
		t.Fatalf("reading the ledger: %v", err)
	}

	var held struct {
		Listings []string `json:"listings"`
	}

	err = json.Unmarshal(data, &held)
	if err != nil {
		t.Fatalf("decoding the ledger: %v", err)
	}

	if strings.Join(held.Listings, ",") != "area-hooks,area-bare-host-deployment" {
		t.Errorf("each area should be recorded once, in reading order, got: %v", held.Listings)
	}
}

func TestARecordKeepsFieldsAnotherToolWrote(t *testing.T) {
	env := areaProject(t)
	ledger := filepath.Join(env.ProjectDir, ".sdd", "autopilot", "turns", "s1.json")
	test.WriteFile(t, ledger, `{"search": 0, "query": 0, "term": 0, "listings": [], "subject": "20260901-000000-d-tac-aaa"}`)

	record(t, env, bash(literalRead))

	data, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("reading the ledger: %v", err)
	}

	if !strings.Contains(string(data), `"subject":"20260901-000000-d-tac-aaa"`) || !strings.Contains(string(data), `"term":1`) {
		t.Errorf("the read should be recorded beside the foreign field, got: %s", data)
	}
}

func TestStreams(t *testing.T) {
	discarding := []string{
		"python3 scripts/graph.py search --term x 2>/dev/null",
		"python3 scripts/graph.py search --term x 2> /dev/null",
		"python3 scripts/graph.py show abc >/dev/null",
		"python3 scripts/graph.py view --layout y &>/dev/null",
		"python3 scripts/graph.py search --term x >/dev/null 2>&1",
		"python3 scripts/graph.py search --term x 2>&1 >/dev/null",
		"python3 scripts/graph.py info 1>/dev/null",
		"cd /x && python3 scripts/graph.py search --term x 2>/dev/null | grep e",
		"mkdir -p q 2>/dev/null; python3 scripts/graph.py search --term x",
		"cd /x\npython3 - <<'PY'\nprint('hello')\nPY\npython3 scripts/graph.py search --term x 2>/dev/null",
	}
	for _, command := range discarding {
		t.Run(command, func(t *testing.T) {
			refuses(t, run(t, areaProject(t), "gate", bash(command)), "discarded stream")
		})
	}

	attached := []string{
		"python3 scripts/graph.py search --term x --limit 8 | grep -E '^  [0-9]'",
		"python3 scripts/graph.py show 20260907-164635-d-tac-vkm --up 1 --down 2",
		areaRead,
		"grep -r sdd docs/ 2>/dev/null",
		"ls .sdd/ 2>/dev/null",
		"python3 x.py 2>/dev/null",
	}
	for _, command := range attached {
		t.Run(command, func(t *testing.T) {
			passes(t, run(t, areaProject(t), "gate", bash(command)))
		})
	}
}

func TestShowDepth(t *testing.T) {
	short := []struct {
		command string
		says    string
	}{
		{command: "--up 0 --down 0", says: "--down 2 --up 1"},
		{command: "--down 0 --up 1", says: "`--down 0`"},
		{command: "--down=0 --up 1", says: "`--down 0`"},
		{command: "--down 1 --up 1", says: "`--down 1`"},
		{command: "--down 2", says: "no `--up`"},
		{command: "--down 2 --up 0", says: "`--up 0`"},
		{command: "", says: "no `--down` and no `--up`"},
		{command: "--down 00", says: "`--down 0`"},
		{command: "--down 1 --up 0", says: "`--down 1` and `--up 0`"},
	}
	for _, tc := range short {
		t.Run("short "+tc.command, func(t *testing.T) {
			refuses(t, run(t, areaProject(t), "gate", bash("python3 scripts/graph.py show 20260907-164635-d-tac-vkm "+tc.command)), tc.says)
		})
	}

	t.Run("a list is short too", func(t *testing.T) {
		refuses(t, run(t, areaProject(t), "gate", bash("python3 scripts/graph.py show 20260912-152909-d-cpt-ony 20260829-122703-d-tac-kuz --down 0")), "--down 2 --up 1")
	})

	t.Run("a sufficient depth names nothing short", func(t *testing.T) {
		got := run(t, areaProject(t), "gate", bash("python3 scripts/graph.py show 20260907-164635-d-tac-vkm --down 2"))
		if strings.Contains(got.stderr, "--down 2` reads short") {
			t.Error("only the depth that fell short should be named")
		}
	})

	for _, command := range []string{"--up 1 --down 2", "--down 2 --up 1", "--down=3 --up=2"} {
		t.Run("deep "+command, func(t *testing.T) {
			passes(t, run(t, areaProject(t), "gate", bash("python3 scripts/graph.py show 20260907-164635-d-tac-vkm "+command)))
		})
	}
}

func TestProcessLayerRulesEntry(t *testing.T) {
	cases := []struct {
		name    string
		command string
		passes  bool
	}{
		{name: "rules entry alone", command: "show 20260828-160000-s-prc-rfk --up 0 --down 0", passes: true},
		{name: "mixed with another layer", command: "show 20260828-160000-s-prc-rfk 20260907-164635-d-tac-vkm --down 0"},
		{name: "subject flag ignored for the read", command: "show 20260828-160000-s-prc-rfk --up 0 --down 0 --entry 20260831-185605-d-tac-1oz", passes: true},
		{name: "subject flag smuggles nothing", command: "show 20260831-185605-d-tac-1oz --down 0 --entry 20260828-160000-s-prc-rfk"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := run(t, areaProject(t), "gate", bash("python3 scripts/graph.py "+tc.command))
			if tc.passes {
				passes(t, got)
			} else {
				refuses(t, got, "--down 2 --up 1")
			}
		})
	}
}

func TestAHeredocBodyIsData(t *testing.T) {
	quoting := "cd /x\npython3 - <<'PY'\n" +
		"text = '''never run: sdd search --term x 2>/dev/null\n" +
		"and never: sdd show 20260907-164635-d-tac-vkm --down 0'''\n" +
		"PY"

	passes(t, run(t, areaProject(t), "gate", bash(quoting)))

	unquoted := "cat > notes.md <<EOF\nsdd search --term x 2>/dev/null\nsdd show 20260907-164635-d-tac-vkm --down 0\nEOF\necho done"
	passes(t, run(t, areaProject(t), "gate", bash(unquoted)))

	unterminated := "cat <<EOF\nsdd info 2>/dev/null"
	refuses(t, run(t, areaProject(t), "gate", bash(unterminated)), "discarded stream")
}

func TestBareReadsPass(t *testing.T) {
	for _, command := range []string{
		"sdd search --term x",
		`sdd view --layout "topic(area-hooks):as-list"`,
		"sdd show 20260907-164635-d-tac-vkm --down 2 --up 1",
		"sdd info",
		"python3 scripts/graph.py search --term x",
	} {
		t.Run(command, func(t *testing.T) {
			passes(t, run(t, areaProject(t), "gate", bash(command)))
		})
	}
}

func TestEverySpellingOfACallIsSeen(t *testing.T) {
	calls := []string{
		"(sdd search --term x 2>/dev/null)",
		"cd /x && (sdd info 2>/dev/null)",
		"cat x | sdd search --term y 2>/dev/null",
		"cat x |sdd search --term y 2>/dev/null",
		"bash -c 'sdd search --term x 2>/dev/null'",
		`sh -c "sdd info >/dev/null"`,
		"env sdd search --term x 2>/dev/null",
		"env SDD_HOME=/x sdd info 2>/dev/null",
		"/opt/homebrew/bin/sdd search --term x 2>/dev/null",
		"echo 20260907-164635-d-tac-vkm | xargs sdd show --down 2 --up 1 2>/dev/null",
		`bash -c 'bash -c "sdd info 2>/dev/null"'`,
		"echo it\\'s ; sdd show 20260907-164635-d-tac-vkm --down 3 --up 1 2>/dev/null ; echo x'",
	}
	for _, command := range calls {
		t.Run("call "+command, func(t *testing.T) {
			refuses(t, run(t, areaProject(t), "gate", bash(command)), "discarded stream")
		})
	}

	mentions := []string{
		"grep -rn sdd docs/ 2>/dev/null",
		"echo 'sdd is the tool' 2>/dev/null",
		`grep -n "Bash(sdd new" skill.md 2>/dev/null`,
		`grep -rn "captured\\|sdd new" file.md 2>/dev/null`,
		`grep -E "a|sdd show" x 2>/dev/null`,
		"echo 'allowed-tools: Bash(sdd *)' 2>/dev/null",
		"grep -c 'sdd show' file.md 2>/dev/null",
		`grep -n "bash -c 'sdd info'" skill.md 2>/dev/null`,
		"bash -c 'ls docs/' 2>/dev/null",
	}
	for _, command := range mentions {
		t.Run("mention "+command, func(t *testing.T) {
			passes(t, run(t, areaProject(t), "gate", bash(command)))
		})
	}
}

func TestRefusalsNameTheToolItself(t *testing.T) {
	for _, payload := range []map[string]any{
		ask("s1"),
		bash("sdd show 20260907-164635-d-tac-vkm --down 1"),
		bash("sdd search --term x 2>/dev/null"),
	} {
		got := run(t, areaProject(t), "gate", payload)

		if !strings.Contains(got.stderr, "sdd ") || strings.Contains(got.stderr, "graph.py") || strings.Contains(got.stderr, "--entry") {
			t.Errorf("a refusal should suggest plain sdd calls, got: %s", got.stderr)
		}
	}
}

func TestWhatCountsAsAReadPerformed(t *testing.T) {
	groundWith := func(t *testing.T, env grounding.Env, response any) {
		t.Helper()

		for _, command := range []string{"python3 scripts/graph.py search --query 'a subject' --term x", areaRead} {
			payload := bash(command)
			if response != nil {
				payload = with(payload, "tool_response", response)
			}

			record(t, env, payload)
		}
	}

	cases := []struct {
		name     string
		response any
		opens    bool
	}{
		{name: "reads that ran", response: map[string]any{"exit_code": 0}, opens: true},
		{name: "no recorded answer", opens: true},
		{name: "an answer that is no object", response: "done", opens: true},
		{name: "refused with a non-zero exit", response: map[string]any{"exit_code": 1}},
		{name: "marked an error", response: map[string]any{"is_error": true}},
		{name: "interrupted", response: map[string]any{"interrupted": true}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := areaProject(t)
			groundWith(t, env, tc.response)

			got := run(t, env, "gate", ask("s1"))
			if tc.opens {
				passes(t, got)
			} else {
				refuses(t, got, "GROUNDING GATE")
			}
		})
	}

	t.Run("a mention of a read counts for nothing", func(t *testing.T) {
		env := areaProject(t)
		record(t, env, bash("echo 'sdd search --query foo --term bar'"), bash(`echo 'sdd view --layout "topic(area-hooks):as-list"'`))

		refuses(t, run(t, env, "gate", ask("s1")), "neither read")
	})
}

func TestGapCapture(t *testing.T) {
	captureOf := func(path string) string {
		return "python3 scripts/graph.py capture " + path + " --entry 20260901-000000-d-tac-aaa"
	}

	t.Run("a gap draft is refused", func(t *testing.T) {
		env := areaProject(t)
		draft := filepath.Join(env.ProjectDir, "draft.md")
		test.WriteFile(t, draft, test.Fixture(t, "drafts/gap.md"))

		refuses(t, run(t, env, "gate", bash(captureOf(draft))), "carries `kind: gap`")
	})

	for _, kind := range []string{"fact", "done", "question", "insight"} {
		t.Run(kind+" passes", func(t *testing.T) {
			env := areaProject(t)
			draft := filepath.Join(env.ProjectDir, "draft.md")
			test.WriteFile(t, draft, test.Fixture(t, "drafts/"+kind+".md"))

			passes(t, run(t, env, "gate", bash(captureOf(draft))))
		})
	}

	t.Run("reading a gap draft is not capturing it", func(t *testing.T) {
		env := areaProject(t)
		draft := filepath.Join(env.ProjectDir, "draft.md")
		test.WriteFile(t, draft, test.Fixture(t, "drafts/gap.md"))

		passes(t, run(t, env, "gate", bash("cat "+draft)))
	})

	t.Run("another checkout's draft is not this one", func(t *testing.T) {
		env := areaProject(t)
		test.WriteFile(t, filepath.Join(env.ProjectDir, ".claude", "worktrees", "probe", "draft.md"), test.Fixture(t, "drafts/gap.md"))
		env.WorkingDir = filepath.Join(env.ProjectDir, "here")
		test.WriteFile(t, filepath.Join(env.WorkingDir, "draft.md"), test.Fixture(t, "drafts/fact.md"))

		passes(t, run(t, env, "gate", bash(captureOf("draft.md"))))
	})

	t.Run("a gap draft in the working directory is refused", func(t *testing.T) {
		env := areaProject(t)
		env.WorkingDir = filepath.Join(env.ProjectDir, "local")
		test.WriteFile(t, filepath.Join(env.WorkingDir, "draft.md"), test.Fixture(t, "drafts/gap.md"))

		refuses(t, run(t, env, "gate", bash(captureOf("draft.md"))), "carries `kind: gap`")
	})

	t.Run("a draft that is not on disk passes", func(t *testing.T) {
		env := areaProject(t)

		passes(t, run(t, env, "gate", bash(captureOf(filepath.Join(env.ProjectDir, "absent.md")))))
	})
}

func TestTheTurnHookNeverBlocksAPrompt(t *testing.T) {
	t.Run("a payload that is no object", func(t *testing.T) {
		passes(t, runRaw(t, areaProject(t), "turn", `"a string"`))
	})

	t.Run("a session name carrying a separator", func(t *testing.T) {
		passes(t, run(t, areaProject(t), "turn", map[string]any{"session_id": "a/b/c"}))
	})

	t.Run("a ledger directory that is a file", func(t *testing.T) {
		env := areaProject(t)
		test.WriteFile(t, filepath.Join(env.ProjectDir, ".sdd", "autopilot", "turns"), "occupied")

		passes(t, run(t, env, "turn", map[string]any{"session_id": "s1"}))
	})

	t.Run("a ledger that only partly decodes refuses", func(t *testing.T) {
		env := areaProject(t)
		test.WriteFile(t, filepath.Join(env.ProjectDir, ".sdd", "autopilot", "turns", "s1.json"),
			test.Fixture(t, "ledgers/mistyped.json"))

		refuses(t, run(t, env, "gate", ask("s1")), "neither read")
	})

	t.Run("an unreadable ledger refuses rather than passing", func(t *testing.T) {
		env := areaProject(t)
		test.WriteFile(t, filepath.Join(env.ProjectDir, ".sdd", "autopilot", "turns", "s1.json"), "{ not json")

		refuses(t, run(t, env, "gate", ask("s1")), "neither read")
	})
}

func TestOptIn(t *testing.T) {
	t.Run("a project with no graph is left alone", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "project")
		mkdir(t, root)

		got := run(t, grounding.Env{ProjectDir: root, WorkingDir: root}, "gate", ask("opt-in"))

		passes(t, got)

		if got.stderr != "" {
			t.Error("the gate should stay silent")
		}
	})

	t.Run("a graph above the session answers for it", func(t *testing.T) {
		env := bare(t)
		below := filepath.Join(env.ProjectDir, "source", "internal")
		mkdir(t, below)

		refuses(t, run(t, grounding.Env{ProjectDir: below, WorkingDir: below}, "gate", ask("opt-in")), "search")
	})

	t.Run("an unknown mode is refused", func(t *testing.T) {
		got := runRaw(t, bare(t), "sweep", "{}")

		if got.code != 1 || !strings.Contains(got.stderr, "usage") {
			t.Error("an unknown mode should be refused with usage")
		}
	})
}

func TestListingWithoutAPrefix(t *testing.T) {
	cases := []struct {
		name    string
		listing string
		opens   bool
	}{
		{name: "a view of every topic", listing: `sdd view --layout "active:as-counts"`, opens: true},
		{name: "one topic's members", listing: `sdd view --layout "topic(hooks/toolchain):as-list"`},
		{name: "an area listing", listing: `sdd view --layout "topic(area-hooks):as-list"`},
		{name: "a mention of the view", listing: `echo 'sdd view --layout "active:as-counts"'`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := bare(t)
			record(t, env, bash("sdd search --term x"), bash("sdd search --query 'a subject'"), bash(tc.listing))

			got := run(t, env, "gate", ask("s1"))
			if tc.opens {
				passes(t, got)
			} else {
				refuses(t, got, "No listing of every topic ran this turn.")
			}
		})
	}

	t.Run("the refusal names the view", func(t *testing.T) {
		got := run(t, bare(t), "gate", ask("s1"))
		if !strings.Contains(got.stderr, `sdd view --layout "active:as-counts"`) || strings.Contains(got.stderr, "area-") {
			t.Errorf("a project without a prefix should be pointed at the view of every topic, got: %s", got.stderr)
		}
	})
}

func TestListingWithAPrefix(t *testing.T) {
	t.Run("the view of every topic is no area listing", func(t *testing.T) {
		env := areaProject(t)
		record(t, env, bash(literalRead), bash(semanticRead), bash(`python3 scripts/graph.py view --layout "active:as-counts"`))

		refuses(t, run(t, env, "gate", ask("s1")), "No `area-` listing ran this turn.")
	})

	t.Run("a quoted topic still counts", func(t *testing.T) {
		env := areaProject(t)
		record(t, env, bash(literalRead), bash(semanticRead), bash(`python3 scripts/graph.py view --layout 'topic("area-hooks"):as-list'`))

		passes(t, run(t, env, "gate", ask("s1")))
	})

	t.Run("another prefix is honoured", func(t *testing.T) {
		env := bare(t)
		test.WriteFile(t, filepath.Join(env.ProjectDir, ".sdd", "grounding.yaml"), "# the listing that grounds a question\nlisting_prefix: \"hooks/\"\n")
		record(t, env, bash("sdd search --term x"), bash("sdd search --query 'a subject'"), bash(`sdd view --layout "topic(hooks/toolchain):as-list"`))

		passes(t, run(t, env, "gate", ask("s1")))
	})
}
