package grounding_test

import (
	"context"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/grounding"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const leaked = "GROUNDING GATE: refused. The capture holds what looks like a credential ("

func secretSamples(t *testing.T, directory string) map[string]string {
	t.Helper()

	entries, err := fs.ReadDir(test.SecretFixtures(t), directory)
	if err != nil {
		t.Fatalf("listing the %s samples: %v", directory, err)
	}

	found := map[string]string{}
	for _, entry := range entries {
		found[entry.Name()] = test.SecretFixture(t, path.Join(directory, entry.Name()))
	}

	return found
}

// newWithBody is a plain `sdd new` whose body is the text, as an agent hands a
// body through a here-document.
func newWithBody(text string) string {
	return "sdd new d tac \"$(cat <<'END-OF-BODY'\n" + text + "\nEND-OF-BODY\n)\""
}

// draftHolding writes a draft whose body is the text, its header attaching
// the files named, and returns its path.
func draftHolding(t *testing.T, env grounding.Env, body string, attached ...string) string {
	t.Helper()

	header := "---\ntype: s\nlayer: tac\nkind: fact\n"
	if len(attached) > 0 {
		header += "attach: [" + strings.Join(attached, ", ") + "]\n"
	}

	draft := filepath.Join(env.ProjectDir, ".sdd", "tmp", "drafts", "leak.md")
	test.WriteFile(t, draft, header+"---\n\nA body.\n"+body+"\n")

	return draft
}

func TestAGraphWriteHoldingACredentialIsRefused(t *testing.T) {
	for name, sample := range secretSamples(t, "positive") {
		t.Run("sdd new "+name, func(t *testing.T) {
			env := areaProject(t)
			ground(t, env)

			refuses(t, run(t, env, "gate", bash(newWithBody(sample))), leaked)
		})

		t.Run("a capture's draft "+name, func(t *testing.T) {
			env := areaProject(t)
			draft := draftHolding(t, env, sample)
			ground(t, env)
			record(t, env, save("Write", draft))

			refuses(t, run(t, env, "gate", bash("agentic-capture "+draft)), leaked)
		})

		t.Run("an engine report "+name, func(t *testing.T) {
			report := map[string]any{"instance": "i-1", "session": "s-1", "report": map[string]any{"body": sample}}
			refuses(t, run(t, areaProject(t), "gate", mcp("next", report)), leaked)
		})
	}
}

func TestTheCredentialRefusalNamesWhereAndNeverTheText(t *testing.T) {
	sample := test.SecretFixture(t, "positive/github-pat.txt")
	pem := test.SecretFixture(t, "positive/private-key.txt")

	cases := []struct {
		name    string
		prepare func(t *testing.T, env grounding.Env) map[string]any
		where   string
	}{
		{
			name:    "the command",
			prepare: func(*testing.T, grounding.Env) map[string]any { return bash(newWithBody(sample)) },
			where:   "generic-api-key, github-pat, the command, line 2)",
		},
		{
			name: "the draft",
			prepare: func(t *testing.T, env grounding.Env) map[string]any {
				return bash("agentic-capture " + draftHolding(t, env, sample))
			},
			where: "generic-api-key, github-pat, {draft}, line 8)",
		},
		{
			name: "an attached file",
			prepare: func(t *testing.T, env grounding.Env) map[string]any {
				test.WriteFile(t, filepath.Join(env.WorkingDir, "notes", "deploy.key"), pem)

				return bash("agentic-capture " + draftHolding(t, env, "", "notes/deploy.key"))
			},
			where: "private-key, notes/deploy.key, line 1)",
		},
		{
			name: "a file attached alone",
			prepare: func(t *testing.T, env grounding.Env) map[string]any {
				test.WriteFile(t, filepath.Join(env.WorkingDir, "deploy.key"), pem)
				draft := filepath.Join(env.ProjectDir, ".sdd", "tmp", "drafts", "leak.md")
				test.WriteFile(t, draft, "---\ntype: s\nattach: deploy.key\n---\n\nA body.\n")

				return bash("agentic-capture " + draft)
			},
			where: "private-key, deploy.key, line 1)",
		},
		{
			name: "an engine report",
			prepare: func(*testing.T, grounding.Env) map[string]any {
				return mcp("next", map[string]any{"instance": "i-1", "report": map[string]any{"body": sample}})
			},
			where: "generic-api-key, github-pat, the mcp__sdd__next report, line 2)",
		},
		{
			name: "a list in an engine report",
			prepare: func(*testing.T, grounding.Env) map[string]any {
				return mcp("next", map[string]any{"instance": "i-1", "report": map[string]any{"refs": []any{"first", sample}}})
			},
			where: "generic-api-key, github-pat, the mcp__sdd__next report, line 3)",
		},
		{
			name: "an engine start",
			prepare: func(*testing.T, grounding.Env) map[string]any {
				return mcp("start_procedure", map[string]any{"canonical": "capture", "params": map[string]any{"body": sample}})
			},
			where: "generic-api-key, github-pat, the mcp__sdd__start_procedure params, line 2)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := areaProject(t)
			payload := tc.prepare(t, env)

			got := run(t, env, "gate", payload)

			where := strings.ReplaceAll(tc.where, "{draft}", filepath.Join(env.ProjectDir, ".sdd", "tmp", "drafts", "leak.md"))
			want := leaked + where + "; the graph is shared and immutable\n"
			if got.code != 2 || got.stderr != want {
				t.Errorf("the refusal should name the rules, where and the line, got exit %d: %q", got.code, got.stderr)
			}

			if strings.Contains(got.stderr, "ghp_") || strings.Contains(got.stderr, "PRIVATE KEY") {
				t.Error("the refusal should never print the credential")
			}
		})
	}
}

func TestAWriteHoldingNoCredentialIsJudgedAsBefore(t *testing.T) {
	cleared := secretSamples(t, "negative")
	cleared["clean.txt"] = test.SecretFixture(t, "clean.txt")

	for name, sample := range cleared {
		t.Run(name, func(t *testing.T) {
			env := areaProject(t)
			owesEveryRead(t, run(t, env, "gate", bash(newWithBody(sample))))

			ground(t, env)
			passes(t, run(t, env, "gate", bash(newWithBody(sample))))
			passes(t, run(t, env, "gate", mcp("next", map[string]any{"instance": "i-1", "report": map[string]any{"body": sample}})))
		})
	}

	t.Run("a credential the command only mentions is no write", func(t *testing.T) {
		sample := test.SecretFixture(t, "positive/github-pat.txt")
		passes(t, run(t, areaProject(t), "gate", bash("grep -c x <<'EOF'\n"+sample+"\nEOF")))
		passes(t, run(t, areaProject(t), "gate", mcp("next", map[string]any{"instance": "i-1", "label": sample})))
	})

	for name, draft := range map[string]string{
		"a draft opening with no header":    "attach: [deploy.key]\n---\n\nA body.\n",
		"a draft whose header never closes": "---\nattach: [deploy.key]\nkind: fact\n",
	} {
		t.Run(name+" attaches nothing", func(t *testing.T) {
			env := areaProject(t)
			test.WriteFile(t, filepath.Join(env.WorkingDir, "deploy.key"), test.SecretFixture(t, "positive/private-key.txt"))
			path := filepath.Join(env.ProjectDir, ".sdd", "tmp", "drafts", "odd.md")
			test.WriteFile(t, path, draft)
			ground(t, env)

			passes(t, run(t, env, "gate", bash("agentic-capture "+path)))
		})
	}

	t.Run("a file a plain sdd new names is no draft", func(t *testing.T) {
		env := areaProject(t)
		test.WriteFile(t, filepath.Join(env.WorkingDir, "notes.md"), test.SecretFixture(t, "positive/github-pat.txt"))
		ground(t, env)

		passes(t, run(t, env, "gate", bash("sdd new d tac 'as notes.md says'")))
	})
}

func TestACredentialIsRefusedForAnExemptSeat(t *testing.T) {
	env := configured(t, "exempt-seats.yaml")
	processes := test.NewProcesses()
	env.Processes, env.OwnerName = processes, "claude"
	test.ClaimSeat(t, test.MemoryStore(t, env.Roots, env.ProjectDir, env.Checkout), "reviewer", test.Session)

	passes(t, run(t, env, "gate", as(test.Session, bash("sdd new d tac body"))))
	refuses(t, run(t, env, "gate", as(test.Session, bash(newWithBody(test.SecretFixture(t, "positive/github-pat.txt"))))), leaked)
}

func TestAnInterruptDuringTheScanIsNoCredential(t *testing.T) {
	const interrupted = "GROUNDING GATE: refused. The hook was cut short before its credential scan finished"

	sample := test.SecretFixture(t, "positive/github-pat.txt")

	cases := []struct {
		name    string
		prepare func(t *testing.T, env grounding.Env) map[string]any
	}{
		{
			name:    "a clean sdd new",
			prepare: func(*testing.T, grounding.Env) map[string]any { return bash(newWithBody("A body with nothing secret in it.")) },
		},
		{
			name:    "an sdd new holding a credential",
			prepare: func(*testing.T, grounding.Env) map[string]any { return bash(newWithBody(sample)) },
		},
		{
			name: "a capture's draft",
			prepare: func(t *testing.T, env grounding.Env) map[string]any {
				return bash("agentic-capture " + draftHolding(t, env, ""))
			},
		},
		{
			name: "an engine report",
			prepare: func(*testing.T, grounding.Env) map[string]any {
				return mcp("next", map[string]any{"instance": "i-1", "session": "s-1", "report": map[string]any{"body": "clean"}})
			},
		},
		{
			name: "a staged file",
			prepare: func(*testing.T, grounding.Env) map[string]any {
				return mcp("stage_attachment", map[string]any{"name": "a.txt", "content": "clean"})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := areaProject(t)
			payload := tc.prepare(t, env)
			ground(t, env)

			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			got := runIn(ctx, t, env, "gate", payload)
			refuses(t, got, interrupted)

			if strings.Contains(got.stderr, leaked) || strings.Contains(got.stderr, "reads owed") {
				t.Errorf("an interrupted scan should refuse as unfinished, never as a credential or as reads owed, got: %s", got.stderr)
			}
		})
	}
}
