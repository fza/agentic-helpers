package grounding_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/grounding"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const (
	unreadable = "which cannot be read to scan for a credential; it must exist before the Bash call that captures it"
	fromStdin  = "which cannot be scanned for a credential; attach a file instead"
)

// headed writes a draft carrying the header lines given and a clean body, and
// returns its path.
func headed(t *testing.T, env grounding.Env, header string) string {
	t.Helper()

	draft := filepath.Join(env.ProjectDir, ".sdd", "tmp", "drafts", "attached.md")
	test.WriteFile(t, draft, "---\ntype: s\nlayer: tac\n"+header+"---\n\nA body.\n")

	return draft
}

func TestEveryAttachmentIsScanned(t *testing.T) {
	pem := test.SecretFixture(t, "positive/private-key.txt")

	cases := []struct {
		name    string
		command func(t *testing.T, env grounding.Env) string
		says    string
	}{
		{
			name: "a capture attaching a file under another name",
			command: func(t *testing.T, env grounding.Env) string {
				test.WriteFile(t, filepath.Join(env.ProjectDir, "keys", "deploy.key"), pem)

				return "agentic-capture " + headed(t, env, "attach: [keys/deploy.key:deploy.txt]\n")
			},
			says: leaked + "private-key, keys/deploy.key, line 1)",
		},
		{
			name: "a capture attaching from where sdd runs, not the working directory",
			command: func(t *testing.T, env grounding.Env) string {
				test.WriteFile(t, filepath.Join(env.ProjectDir, "keys", "deploy.key"), pem)

				return "agentic-capture " + headed(t, env, "attach: keys/deploy.key\n")
			},
			says: leaked + "private-key, keys/deploy.key, line 1)",
		},
		{
			name: "a plain sdd new attaching a file",
			command: func(t *testing.T, env grounding.Env) string {
				test.WriteFile(t, filepath.Join(env.WorkingDir, "deploy.key"), pem)

				return "sdd new d tac body --attach deploy.key"
			},
			says: leaked + "private-key, deploy.key, line 1)",
		},
		{
			name: "a plain sdd new attaching a file under another name",
			command: func(t *testing.T, env grounding.Env) string {
				test.WriteFile(t, filepath.Join(env.WorkingDir, "deploy.key"), pem)

				return "sdd new d tac body --attach=deploy.key:deploy.txt"
			},
			says: leaked + "private-key, deploy.key, line 1)",
		},
		{
			name: "a credential outranks what cannot be scanned",
			command: func(t *testing.T, env grounding.Env) string {
				test.WriteFile(t, filepath.Join(env.WorkingDir, "deploy.key"), pem)

				return "sdd new d tac body --attach -:pasted.txt --attach deploy.key"
			},
			says: leaked + "private-key, deploy.key, line 1)",
		},
		{
			name: "a capture attaching standard input",
			command: func(t *testing.T, env grounding.Env) string {
				return "agentic-capture " + headed(t, env, "attach: ['-:pasted.txt']\n")
			},
			says: "GROUNDING GATE: refused. The capture attaches standard input as pasted.txt, " + fromStdin,
		},
		{
			name: "a plain sdd new attaching standard input",
			command: func(*testing.T, grounding.Env) string {
				return "printf x | sdd new d tac body --attach -:pasted.txt"
			},
			says: "GROUNDING GATE: refused. The capture attaches standard input as pasted.txt, " + fromStdin,
		},
		{
			name: "a capture attaching a file not on disk",
			command: func(t *testing.T, env grounding.Env) string {
				return "agentic-capture " + headed(t, env, "attach: [keys/absent.key]\n")
			},
			says: "GROUNDING GATE: refused. The capture names keys/absent.key, " + unreadable,
		},
		{
			name: "a plain sdd new attaching a file not on disk",
			command: func(*testing.T, grounding.Env) string {
				return "sdd new d tac body --attach absent.key"
			},
			says: "GROUNDING GATE: refused. The capture names absent.key, " + unreadable,
		},
		{
			name: "a draft written in the same call",
			command: func(*testing.T, grounding.Env) string {
				return "printf 'x' > later.md && agentic-capture later.md"
			},
			says: "GROUNDING GATE: refused. The capture names later.md, " + unreadable,
		},
		{
			name: "a draft whose header does not parse",
			command: func(t *testing.T, env grounding.Env) string {
				return "agentic-capture " + headed(t, env, "attach: [unclosed\n")
			},
			says: "attached.md's header, " + unreadable,
		},
		{
			name: "drafts find hands over",
			command: func(*testing.T, grounding.Env) string {
				return `find . -name '*.md' -exec agentic-capture {} \;`
			},
			says: "GROUNDING GATE: refused. The capture names {}, " + unreadable,
		},
		{
			name: "a draft handed over with no name in the call",
			command: func(*testing.T, grounding.Env) string {
				return "ls drafts | xargs agentic-capture"
			},
			says: "GROUNDING GATE: refused. The capture names no draft that can be read to scan for a credential; name the draft file in the call",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := areaProject(t)
			env.WorkingDir = filepath.Join(env.ProjectDir, "sub")
			mkdir(t, env.WorkingDir)
			command := tc.command(t, env)
			ground(t, env)

			refuses(t, run(t, env, "gate", bash(command)), tc.says)
		})
	}
}

func TestTheFirstFindOfEachKindIsNamed(t *testing.T) {
	pem := test.SecretFixture(t, "positive/private-key.txt")
	sample := test.SecretFixture(t, "positive/github-pat.txt")

	env := areaProject(t)
	test.WriteFile(t, filepath.Join(env.WorkingDir, "deploy.key"), pem)
	ground(t, env)

	refuses(t, run(t, env, "gate", bash("sdd new d tac '"+sample+"' --attach deploy.key")), leaked+"generic-api-key, github-pat, the command,")
	refuses(t, run(t, env, "gate", bash("sdd new d tac body --attach -:first.txt --attach absent.key")), "standard input as first.txt")
}

func TestWhereADraftAndItsFilesAreRead(t *testing.T) {
	pem := test.SecretFixture(t, "positive/private-key.txt")

	cases := []struct {
		name    string
		command func(t *testing.T, env grounding.Env) string
		says    string
	}{
		{
			name: "a draft under the home directory",
			command: func(t *testing.T, env grounding.Env) string {
				test.WriteFile(t, filepath.Join(env.Home, "d.md"), "---\ntype: s\n---\n\nA body.\n"+pem+"\n")

				return "agentic-capture ~/d.md"
			},
			says: leaked + "private-key, ~/d.md, line 6)",
		},
		{
			name: "a file attached from the home directory",
			command: func(t *testing.T, env grounding.Env) string {
				test.WriteFile(t, filepath.Join(env.Home, "deploy.key"), pem)

				return "sdd new d tac body --attach ~/deploy.key"
			},
			says: "GROUNDING GATE: refused. The capture names ~/deploy.key, which cannot be read to scan for a credential: it holds a shell expansion",
		},
		{
			name: "a draft relative to the project, not the working directory",
			command: func(t *testing.T, env grounding.Env) string {
				test.WriteFile(t, filepath.Join(env.ProjectDir, "drafts", "d.md"), "---\ntype: s\n---\n\nA body.\n"+pem+"\n")

				return "agentic-capture drafts/d.md"
			},
			says: leaked + "private-key, drafts/d.md, line 6)",
		},
		{
			name: "a draft outside every graph attaching from the working directory's",
			command: func(t *testing.T, env grounding.Env) string {
				test.WriteFile(t, filepath.Join(env.ProjectDir, "deploy.key"), pem)
				draft := filepath.Join(t.TempDir(), "d.md")
				test.WriteFile(t, draft, "---\ntype: s\nattach: deploy.key\n---\n\nA body.\n")

				return "agentic-capture " + draft
			},
			says: leaked + "private-key, deploy.key, line 1)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := areaProject(t)
			env.WorkingDir = filepath.Join(env.ProjectDir, "sub")
			mkdir(t, env.WorkingDir)
			command := tc.command(t, env)
			ground(t, env)

			refuses(t, run(t, env, "gate", bash(command)), tc.says)
		})
	}
}

func TestAnAttachmentIsScannedByItsNameInTheEntry(t *testing.T) {
	env := areaProject(t)
	test.WriteFile(t, filepath.Join(env.WorkingDir, "bundle.bin"), "opaque bytes")
	ground(t, env)

	passes(t, run(t, env, "gate", bash("sdd new d tac body --attach bundle.bin")))
	refuses(t, run(t, env, "gate", bash("sdd new d tac body --attach bundle.bin:client.p12")), leaked+"pkcs12-file, bundle.bin, line 1)")
}

func TestACleanAttachmentPasses(t *testing.T) {
	env := areaProject(t)
	test.WriteFile(t, filepath.Join(env.WorkingDir, "run.log"), test.SecretFixture(t, "clean.txt"))
	ground(t, env)

	passes(t, run(t, env, "gate", bash("sdd new d tac body --attach run.log --attach=run.log:copy.log")))
	passes(t, run(t, env, "gate", bash("agentic-capture "+headed(t, env, "attach: [run.log]\n"))))
}

func TestWhatCannotBeScannedIsRefusedLast(t *testing.T) {
	env := areaProject(t)
	absent := "agentic-capture " + filepath.Join(env.ProjectDir, "absent.md")

	refuses(t, run(t, env, "gate", bash(absent)), "neither read")

	ground(t, env)
	refuses(t, run(t, env, "gate", bash(absent)), unreadable)
}

func TestWhatCannotBeScannedIsRefusedForAnExemptSeat(t *testing.T) {
	env := configured(t, "exempt-seats.yaml")
	processes := test.NewProcesses()
	env.Processes, env.OwnerName = processes, "claude"
	test.ClaimSeat(t, test.MemoryStore(env.ProjectDir, env.Home, env.Checkout), "reviewer", test.Session)

	refuses(t, run(t, env, "gate", as(test.Session, bash("sdd new d tac body --attach -:pasted.txt"))), fromStdin)
}

func TestAStagedFileIsScanned(t *testing.T) {
	sample := test.SecretFixture(t, "positive/github-pat.txt")

	cases := []struct {
		name  string
		input func(t *testing.T, env grounding.Env) map[string]any
		says  string
	}{
		{
			name: "inline content",
			input: func(*testing.T, grounding.Env) map[string]any {
				return map[string]any{"name": "env.txt", "content": sample}
			},
			says: leaked + "generic-api-key, github-pat, the staged env.txt, line 2)",
		},
		{
			name: "a patch",
			input: func(*testing.T, grounding.Env) map[string]any {
				return map[string]any{"name": "env.txt", "patches": []any{map[string]any{"old": "a", "new": sample}}}
			},
			says: leaked + "generic-api-key, github-pat, the staged env.txt, line 5)",
		},
		{
			name: "a local file",
			input: func(t *testing.T, env grounding.Env) map[string]any {
				test.WriteFile(t, filepath.Join(env.WorkingDir, "env.txt"), sample)

				return map[string]any{"name": "env.txt", "path": "env.txt"}
			},
			says: leaked + "generic-api-key, github-pat, env.txt, line 1)",
		},
		{
			name: "any other field",
			input: func(*testing.T, grounding.Env) map[string]any {
				return map[string]any{"name": "env.txt", "content": "plain", "note": sample}
			},
			says: leaked + "generic-api-key, github-pat, the staged env.txt, line 6)",
		},
		{
			name: "a local file, by its staged name",
			input: func(t *testing.T, env grounding.Env) map[string]any {
				test.WriteFile(t, filepath.Join(env.WorkingDir, "vars.txt"), `administrator_login_password = "thisIsDog11"`)

				return map[string]any{"name": "main.tf", "path": "vars.txt"}
			},
			says: leaked + "hashicorp-tf-password, vars.txt, line 1)",
		},
		{
			name: "a local file not on disk",
			input: func(*testing.T, grounding.Env) map[string]any {
				return map[string]any{"name": "env.txt", "path": "absent.txt"}
			},
			says: "GROUNDING GATE: refused. The capture names absent.txt, " + unreadable,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := areaProject(t)
			refuses(t, run(t, env, "gate", mcp("stage_attachment", tc.input(t, env))), tc.says)
		})
	}

	t.Run("a clean file passes", func(t *testing.T) {
		passes(t, run(t, areaProject(t), "gate", mcp("stage_attachment", map[string]any{"name": "notes.txt", "content": "plain notes"})))
	})
}

func TestOnlyALiteralAttachmentPathTheGateCanPlaceIsRead(t *testing.T) {
	const (
		expansion = "it holds a shell expansion"
		moved     = "it is relative after a directory change"
		launched  = "it is relative behind a launcher"
		patterned = "it holds a shell glob or brace"
	)

	cases := []struct {
		command string
		where   string
		why     string
	}{
		{command: "cd sub && sdd new d tac body --attach run.log", where: "run.log", why: moved},
		{command: "(cd sub; sdd new d tac body --attach=run.log:copy.log)", where: "run.log", why: moved},
		{command: "pushd sub && sdd new d tac body --attach run.log", where: "run.log", why: moved},
		{command: "bash -c 'cd sub && sdd new d tac body --attach run.log'", where: "run.log", why: moved},
		{command: "sdd new d tac body --attach run.log; cd sub", where: "run.log", why: moved},
		{command: "env -C sub sdd new d tac body --attach run.log", where: "run.log", why: launched},
		{command: "env FOO=1 sdd new d tac body --attach run.log", where: "run.log", why: launched},
		{command: "sudo -iD sub sdd new d tac body --attach run.log", where: "run.log", why: launched},
		{command: "nice -n 5 sdd new d tac body --attach run.log", where: "run.log", why: launched},
		{command: "timeout 5 sdd new d tac body --attach run.log", where: "run.log", why: launched},
		{command: "echo x | xargs sdd new d tac body --attach run.log", where: "run.log", why: launched},
		{command: "flock /tmp/lock sdd new d tac body --attach run.log", where: "run.log", why: launched},
		{command: "env agentic-capture {draft}", where: "run.log", why: launched},
		{command: "cd sub && agentic-capture {draft}", where: "run.log", why: moved},
		{command: "sdd new d tac body --attach '$HOME/run.log'", where: "$HOME/run.log", why: expansion},
		{command: `sdd new d tac body --attach "$PWD/run.log"`, where: "$PWD/run.log", why: expansion},
		{command: "sdd new d tac body --attach ~/run.log", where: "~/run.log", why: expansion},
		{command: "sdd new d tac body --attach 'logs~/run.log'", where: "logs~/run.log", why: expansion},
		{command: "sdd new d tac body --attach run*.log", where: "run*.log", why: patterned},
		{command: "sdd new d tac body --attach=ru?.log", where: "--attach=ru?.log", why: patterned},
		{command: "sdd new d tac body --attach 'run'[.]log", where: "run[.]log", why: patterned},
		{command: "sdd new d tac body --attach 'run'[.log", where: "run[.log", why: patterned},
		{command: "sdd new d tac body --attach run.{log,txt}", where: "run.{log,txt}", why: patterned},
		{command: "sdd new d tac body --attach=@(run).log:copy.log", where: "--attach=@(run).log:copy.log", why: patterned},
		{command: "sdd new d tac body --attach /tmp/*.log", where: "/tmp/*.log", why: patterned},
		{command: "sdd new d tac x --att{ach,x} run.log", where: "--att{ach,x}", why: patterned},
		{command: "sdd new d tac x --attac?=run.log", where: "--attac?=run.log", why: patterned},
		{command: "sdd new d tac body* --attach run.log", where: "body*", why: patterned},
		{command: "agentic-capture .sdd/tmp/drafts/attache?.md", where: ".sdd/tmp/drafts/attache?.md", why: patterned},
		{command: "agentic-capture --preflight-route={a,b} {draft}", where: "--preflight-route={a,b}", why: patterned},
	}

	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			env := areaProject(t)
			test.WriteFile(t, filepath.Join(env.WorkingDir, "run.log"), test.SecretFixture(t, "clean.txt"))
			headed(t, env, "attach: [run.log]\n")
			command := strings.ReplaceAll(tc.command, "{draft}", ".sdd/tmp/drafts/attached.md")

			refuses(t, run(t, env, "gate", bash(command)), "neither read")

			ground(t, env)
			refuses(t, run(t, env, "gate", bash(command)),
				"GROUNDING GATE: refused. The capture names "+tc.where+", which cannot be read to scan for a credential: "+tc.why+
					"; attach it by a literal absolute path")
		})
	}

	t.Run("a literal absolute path is read wherever the command goes", func(t *testing.T) {
		env := areaProject(t)
		attached := filepath.Join(env.WorkingDir, "run.log")
		test.WriteFile(t, attached, test.SecretFixture(t, "clean.txt"))
		ground(t, env)

		passes(t, run(t, env, "gate", bash("cd sub && sdd new d tac body --attach "+attached)))
		passes(t, run(t, env, "gate", bash("env -C sub sdd new d tac body --attach "+attached)))
	})

	t.Run("a literal relative path with no move and no launcher is read", func(t *testing.T) {
		env := areaProject(t)
		test.WriteFile(t, filepath.Join(env.WorkingDir, "run.log"), test.SecretFixture(t, "clean.txt"))
		ground(t, env)

		passes(t, run(t, env, "gate", bash("sdd new d tac body --attach run.log")))
		passes(t, run(t, env, "gate", bash("time sdd new d tac body --attach run.log")))
		passes(t, run(t, env, "gate", bash("flock /tmp/lock cd sub; sdd new d tac body --attach run.log")))
	})

	t.Run("a quoted or escaped glob is read as the file it names", func(t *testing.T) {
		env := areaProject(t)
		test.WriteFile(t, filepath.Join(env.WorkingDir, "run*.{log}"), test.SecretFixture(t, "clean.txt"))
		ground(t, env)

		passes(t, run(t, env, "gate", bash("sdd new d tac body --attach 'run*.{log}'")))
		passes(t, run(t, env, "gate", bash(`sdd new d tac body --attach "run*.{log}"`)))
		passes(t, run(t, env, "gate", bash(`sdd new d tac body --attach run\*.\{log\}`)))
		passes(t, run(t, env, "gate", bash(`sdd new d tac body --attach=$'run*.{log}'`)))
		passes(t, run(t, env, "gate", bash(`sdd new d tac 'a body * {a,b}' --attach 'run*.{log}'`)))
	})

	t.Run("a draft header's glob is no shell word", func(t *testing.T) {
		env := areaProject(t)
		test.WriteFile(t, filepath.Join(env.WorkingDir, "run*.log"), test.SecretFixture(t, "clean.txt"))
		headed(t, env, "attach: ['run*.log']\n")
		ground(t, env)

		passes(t, run(t, env, "gate", bash("agentic-capture .sdd/tmp/drafts/attached.md")))
	})

	t.Run("standard input is refused as standard input", func(t *testing.T) {
		env := areaProject(t)
		ground(t, env)

		refuses(t, run(t, env, "gate", bash("cd sub && sdd new d tac body --attach -:pasted.txt")), "standard input as pasted.txt")
	})

	t.Run("a staged file named through an expansion", func(t *testing.T) {
		refuses(t, run(t, areaProject(t), "gate", mcp("stage_attachment", map[string]any{"name": "a.txt", "path": "$HOME/a.txt"})),
			"The capture names $HOME/a.txt, which cannot be read to scan for a credential: "+expansion)
	})
}

func TestACredentialUnderANameTheAllowlistSkipsIsStillFound(t *testing.T) {
	pem := test.SecretFixture(t, "positive/private-key.txt")

	for _, name := range []string{"deploy.bin", "logo.png"} {
		t.Run(name, func(t *testing.T) {
			env := areaProject(t)
			test.WriteFile(t, filepath.Join(env.WorkingDir, "key.txt"), pem)
			ground(t, env)

			refuses(t, run(t, env, "gate", bash("sdd new d tac body --attach key.txt:"+name)), leaked+"private-key, key.txt, line 1)")
			refuses(t, run(t, env, "gate", bash("agentic-capture "+headed(t, env, "attach: ['key.txt:"+name+"']\n"))), leaked+"private-key, key.txt, line 1)")
			refuses(t, run(t, env, "gate", mcp("stage_attachment", map[string]any{"name": name, "content": pem})), leaked+"private-key, the staged "+name)
			refuses(t, run(t, env, "gate", mcp("stage_attachment", map[string]any{"name": name, "path": "key.txt"})), leaked+"private-key, key.txt, line 1)")
		})
	}
}
