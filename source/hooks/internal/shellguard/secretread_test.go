package shellguard_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/shellguard"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const (
	refusedRead  = "holds a credential ("
	skipFixtures = `"shell_guard_secret_read_skip":"~/project=fixtures/**"`
	certOnly     = "-----BEGIN CERTIFICATE-----\nMIIBszCCAVmgAwIBAgIUQ2xlYXJseU5vdEFLZXlBdEFsbDAKBggqhkjOPQQDAjAP\n-----END CERTIFICATE-----\n"
)

// read is the decision on a Read of the file, allow where the hook answered
// nothing, and the reason.
func (g guard) read(tool string, input map[string]any) (string, string) {
	g.t.Helper()

	for key, value := range input {
		text, isText := value.(string)
		if isText {
			input[key] = g.placeholders.Replace(text)
		}
	}

	payload, err := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse", "cwd": g.tree.Project, "tool_name": tool, "tool_input": input,
	})
	if err != nil {
		g.t.Fatalf("encoding the call: %v", err)
	}

	var out, errs bytes.Buffer

	status := shellguard.Run(context.Background(), []string{"read"}, g.env, shellguard.Streams{In: bytes.NewReader(payload), Out: &out, Err: &errs})
	if status != 0 {
		g.t.Fatalf("the read hook should answer, exit %d: %s", status, errs.String())
	}

	if out.Len() == 0 {
		return "allow", ""
	}

	var decoded reply

	err = json.Unmarshal(out.Bytes(), &decoded)
	if err != nil {
		g.t.Fatalf("decoding the answer: %v", err)
	}

	return decoded.HookSpecificOutput.PermissionDecision, decoded.HookSpecificOutput.PermissionDecisionReason
}

func (g guard) readFile(name string) (string, string) {
	g.t.Helper()

	return g.read("Read", map[string]any{"file_path": name})
}

func TestAReadOfAConfigFileWithASecretKeyIsRefusedWithItsLinesWithheld(t *testing.T) {
	g := newGuard(t, true, "")
	test.WriteFile(t, filepath.Join(g.tree.Project, "app.yaml"), "db:\n  user: app\n  password: hunter2\n  port: 5432\n")

	decision, reason := g.readFile("app.yaml")

	want := "agentic-shell-guard: app.yaml holds a credential (secret-named keys password); reading it is refused. " +
		"Its lines, those inside a credential withheld:\n" +
		"     1\tdb:\n     2\t  user: app\n     3\t[withheld]\n     4\t  port: 5432"
	if decision != "deny" || reason != want {
		t.Errorf("a config file holding a secret-named key should be refused with its other lines handed back:\n%s", reason)
	}

	if strings.Contains(reason, "hunter2") {
		t.Error("the refusal should never carry the secret")
	}
}

func TestAConfigValueKeptElsewhereReadsAsIs(t *testing.T) {
	g := newGuard(t, true, "")
	test.WriteFile(t, filepath.Join(g.tree.Project, "app.yaml"),
		"password: ${DB_PASSWORD}\ntoken_url: https://auth.example/token\nmax_tokens: 4096\nkey: app.kubernetes.io/name\n")

	decision, _ := g.readFile("app.yaml")
	if decision != "allow" {
		t.Error("a reference, a key past its secret word and a bare key should read as is")
	}
}

func TestEachSecretKeySpellingIsRefused(t *testing.T) {
	for _, tc := range []struct{ file, text string }{
		{"app.yaml", "oauth:\n  client_secret: q8Lx2v\n"},
		{"app.json", "{\n  \"apiKey\": \"q8Lx2v\"\n}\n"},
		{".env", "API_TOKEN=q8Lx2v\n"},
		{"settings.toml", "[db]\npassword = \"q8Lx2v\"\n"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			g := newGuard(t, true, "")
			test.WriteFile(t, filepath.Join(g.tree.Project, tc.file), tc.text)

			decision, reason := g.readFile(tc.file)
			if decision != "deny" || !strings.Contains(reason, refusedRead) || strings.Contains(reason, "q8Lx2v") {
				t.Errorf("a secret-named key with a real value should be refused, withheld: %s", reason)
			}
		})
	}
}

func TestARangeInsideAKeyBlockIsRefusedAndAClearRangeReadsAsIs(t *testing.T) {
	g := newGuard(t, true, "")
	key := test.SecretFixture(t, "positive/private-key.txt")
	keyLines := strings.Count(key, "\n") + 1
	test.WriteFile(t, filepath.Join(g.tree.Project, "tls.yaml"),
		"name: edge\nport: 443\ntls_key: |\n  "+strings.ReplaceAll(key, "\n", "\n  ")+"\nreplicas: 2\n")

	decision, reason := g.read("Read", map[string]any{"file_path": "tls.yaml", "offset": 5, "limit": 2})
	if decision != "deny" || !strings.Contains(reason, "a private key") ||
		!strings.Contains(reason, "     5\t[withheld]\n     6\t[withheld]") || strings.Contains(reason, "replicas") {
		t.Errorf("a range starting inside a key block should be refused, only its own lines handed back, withheld:\n%s", reason)
	}

	decision, _ = g.read("Read", map[string]any{"file_path": "tls.yaml", "offset": 1, "limit": 2})
	if decision != "allow" {
		t.Error("a range clear of every credential should read as is")
	}

	decision, _ = g.read("Read", map[string]any{"file_path": "tls.yaml", "offset": 4 + keyLines, "limit": 5})
	if decision != "allow" {
		t.Error("a range past the key block should read as is")
	}
}

func TestPrivateKeyFilesAreRefusedAndACertificateReadsAsIs(t *testing.T) {
	g := newGuard(t, true, "")
	key := test.SecretFixture(t, "positive/private-key.txt")
	test.WriteFile(t, filepath.Join(g.tree.Home, ".ssh", "id_ed25519"), key+"\n")
	test.WriteFile(t, filepath.Join(g.tree.Project, "bundle.pem"), certOnly+key+"\n")
	test.WriteFile(t, filepath.Join(g.tree.Project, "store.p12"), "\x30\x82\x01\x00binary")
	test.WriteFile(t, filepath.Join(g.tree.Project, "site.crt"), certOnly)

	for _, name := range []string{"{home}/.ssh/id_ed25519", "bundle.pem"} {
		decision, reason := g.readFile(name)
		if decision != "deny" || !strings.Contains(reason, "(a private key)") {
			t.Errorf("%s should be refused as holding a private key: %s", name, reason)
		}
	}

	decision, reason := g.readFile("bundle.pem")
	if !strings.Contains(reason, "-----BEGIN CERTIFICATE-----") {
		t.Errorf("a bundle's certificate lines should be handed back: %s", reason)
	}

	decision, reason = g.readFile("store.p12")
	if decision != "deny" || !strings.HasSuffix(reason, "store.p12 holds a credential (a private key store by its path); reading it is refused") {
		t.Errorf("a binary key store should be refused by its path, with no copy: %s", reason)
	}

	decision, _ = g.readFile("site.crt")
	if decision != "allow" {
		t.Error("a certificate holding no private key should read as is")
	}
}

func TestACredentialStoreIsRefusedWhole(t *testing.T) {
	g := newGuard(t, true, "")
	test.WriteFile(t, filepath.Join(g.tree.Home, ".netrc"), "machine example.org\nlogin app\npassword q8Lx2v\n")

	decision, reason := g.readFile("{home}/.netrc")
	if decision != "deny" || !strings.Contains(reason, "(a credential store by name)") || strings.Contains(reason, "machine") {
		t.Errorf("a credential store should be refused with every line withheld: %s", reason)
	}
}

func TestALinkIsJudgedByTheFileItPointsAt(t *testing.T) {
	g := newGuard(t, true, "")
	test.WriteFile(t, filepath.Join(g.tree.Outside, "secrets.env"), "API_TOKEN=q8Lx2v\n")

	err := os.Symlink(filepath.Join(g.tree.Outside, "secrets.env"), filepath.Join(g.tree.Project, "linked.txt"))
	if err != nil {
		t.Fatalf("linking: %v", err)
	}

	decision, _ := g.readFile("linked.txt")
	if decision != "deny" {
		t.Error("a link should be judged as the file it resolves to")
	}
}

func TestANotebookIsJudgedForPrivateKeys(t *testing.T) {
	g := newGuard(t, true, "")
	key := test.SecretFixture(t, "positive/private-key.txt")
	test.WriteFile(t, filepath.Join(g.tree.Project, "nb.ipynb"), "{\"cells\":[]}\n"+key+"\n")

	decision, _ := g.read("NotebookRead", map[string]any{"notebook_path": "nb.ipynb"})
	if decision != "deny" {
		t.Error("a notebook holding a private key should be refused")
	}
}

func TestAPlainBashReaderOfACredentialFileIsRefusedBeforeItRuns(t *testing.T) {
	g := newGuard(t, true, "")
	test.WriteFile(t, filepath.Join(g.tree.Project, ".env"), "DEBUG=1\nAPI_TOKEN=q8Lx2v\n")

	decision, reason := g.judge("cat .env", "{project}")
	if decision != "deny" || !strings.Contains(reason, ".env holds a credential (secret-named keys API_TOKEN)") ||
		!strings.Contains(reason, "     1\tDEBUG=1\n     2\t[withheld]") {
		t.Errorf("a plain reader of a credential file should be refused, its lines handed back withheld: %s", reason)
	}

	decision, _ = g.judge(`cat "$CONFIG"`, "{project}")
	if decision != "allow" {
		t.Error("a reader of a path known only at run time should run")
	}

	test.WriteFile(t, filepath.Join(g.tree.Project, "sub", "app.yaml"), "password: hunter2\n")

	decision, _ = g.judge("cd sub && cat app.yaml", "{project}")
	if decision != "deny" {
		t.Error("a reader after a cd should be judged in the directory it runs in")
	}
}

func TestABashReaderOfAConfigFileTooLargeToCheckIsRefused(t *testing.T) {
	g := newGuard(t, true, "")
	line := strings.Repeat("a", 1023) + "\n"
	test.WriteFile(t, filepath.Join(g.tree.Project, "huge.json"), strings.Repeat(line, 9<<10))
	test.WriteFile(t, filepath.Join(g.tree.Project, "app.log"), strings.Repeat(line, 20<<10))

	decision, reason := g.judge("head -n 5 huge.json", "{project}")
	if decision != "deny" || !strings.HasSuffix(reason, "huge.json: file too large to check; reading it is refused") {
		t.Errorf("a config file too large to scan whole should be refused: %s", reason)
	}

	decision, _ = g.judge("tail -n 50 app.log", "{project}")
	if decision != "allow" {
		t.Error("a log too large to scan whole, holding no key in its first part, should run")
	}
}

func TestARangeOfALargeFileIsJudgedWithTheLinesBeforeIt(t *testing.T) {
	g := newGuard(t, true, "")
	key := test.SecretFixture(t, "positive/private-key.txt")
	filler := strings.Repeat(strings.Repeat("a", 1023)+"\n", 9<<10)
	test.WriteFile(t, filepath.Join(g.tree.Project, "big.log"), filler+key+"\ntail\n")

	start := 9<<10 + 3

	decision, reason := g.read("Read", map[string]any{"file_path": "big.log", "offset": start, "limit": 1})
	if decision != "deny" || !strings.Contains(reason, "[withheld]") {
		t.Errorf("a range inside a key whose first line lies before it should be refused: %s", reason)
	}

	decision, _ = g.read("Read", map[string]any{"file_path": "big.log", "offset": 1, "limit": 10})
	if decision != "allow" {
		t.Error("a range of a large file clear of every key should read as is")
	}
}

func TestASkipEntryLetsAReadThroughOnlyAsAPluginOption(t *testing.T) {
	skipped := newGuard(t, true, skipFixtures)
	test.WriteFile(t, filepath.Join(skipped.tree.Project, "fixtures", "app.yaml"), "password: hunter2\n")

	decision, _ := skipped.readFile("fixtures/app.yaml")
	if decision != "allow" {
		t.Error("a path a plugin-option skip entry names should read as is")
	}

	decision, _ = skipped.judge("cat fixtures/app.yaml", "{project}")
	if decision != "allow" {
		t.Error("a Bash reader of a skipped path should run")
	}

	g := newGuard(t, true, "")
	test.WriteFile(t, filepath.Join(g.tree.Project, "fixtures", "app.yaml"), "password: hunter2\n")

	config := filepath.Join(t.TempDir(), "config.yaml")
	test.WriteFile(t, config, "shell_guard_secret_read_skip: \"~/project=fixtures/**\"\n")
	g.env.Settings = append([]string{config}, g.env.Settings...)

	decision, _ = g.readFile("fixtures/app.yaml")
	if decision != "deny" {
		t.Error("a skip entry in config.yaml should change nothing")
	}
}

func TestOffLetsReadsThroughButNotFromConfigYAML(t *testing.T) {
	off := newGuard(t, true, `"shell_guard_secret_read":"off"`)
	test.WriteFile(t, filepath.Join(off.tree.Project, ".env"), "API_TOKEN=q8Lx2v\n")

	decision, _ := off.readFile(".env")
	if decision != "allow" {
		t.Error("off should let a read through")
	}

	decision, _ = off.judge("cat .env", "{project}")
	if decision != "allow" {
		t.Error("off should let a Bash reader through")
	}

	call := off.output(context.Background(), "Grep", map[string]any{"pattern": "TOKEN", "path": "{project}"},
		map[string]any{"content": ".env:1:API_TOKEN=q8Lx2v"})
	if call.answered {
		t.Error("off should leave a search's lines as printed")
	}

	g := newGuard(t, true, `"shell_guard_secret_read":"deny"`)
	test.WriteFile(t, filepath.Join(g.tree.Project, ".env"), "API_TOKEN=q8Lx2v\n")

	config := filepath.Join(t.TempDir(), "config.yaml")
	test.WriteFile(t, config, "shell_guard_secret_read: \"off\"\n")
	g.env.Settings = append([]string{config}, g.env.Settings...)

	decision, _ = g.readFile(".env")
	if decision != "deny" {
		t.Error("off in config.yaml over a plugin deny should change nothing")
	}
}

func TestSearchesWithholdEachLineFromAFileHoldingACredential(t *testing.T) {
	g := newGuard(t, true, "")
	key := test.SecretFixture(t, "positive/private-key.txt")
	keyLine := strings.SplitN(key, "\n", 3)[1]
	test.WriteFile(t, filepath.Join(g.tree.Project, "app.yaml"), "user: app\npassword: hunter2\n")
	test.WriteFile(t, filepath.Join(g.tree.Project, "id_rsa"), key+"\n")
	test.WriteFile(t, filepath.Join(g.tree.Project, ".netrc"), "machine example.org password q8Lx2v\n")
	test.WriteFile(t, filepath.Join(g.tree.Project, "notes.md"), "the password: is in the vault\n")

	content := "app.yaml:1:user: app\napp.yaml:2:password: hunter2\nid_rsa:" + keyLine +
		"\n.netrc:1:machine example.org password q8Lx2v\nnotes.md:1:the password: is in the vault"

	for _, tc := range []struct {
		name  string
		tool  string
		input map[string]any
		reply any
	}{
		{"Grep", "Grep", map[string]any{"pattern": "password", "path": "{project}"}, map[string]any{"mode": "content", "content": content}},
		{"grep -rn", "Bash", map[string]any{"command": "grep -rn password ."}, map[string]any{"stdout": content, "stderr": ""}},
		{"xargs grep -n", "Bash", map[string]any{"command": "git ls-files | xargs grep -n password"}, map[string]any{"stdout": content}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			call := g.output(context.Background(), tc.tool, tc.input, tc.reply)

			got, _ := json.Marshal(call.reply.HookSpecificOutput.UpdatedToolOutput)
			text := string(got)

			if !call.answered || strings.Contains(text, "hunter2") || strings.Contains(text, keyLine) || strings.Contains(text, "q8Lx2v") {
				t.Errorf("each line inside a credential of its file should be withheld: %s", text)
			}

			for _, kept := range []string{"app.yaml:1:user: app", "notes.md:1:the password: is in the vault"} {
				if !strings.Contains(text, kept) {
					t.Errorf("a line holding no credential should be kept: %s", text)
				}
			}

			for _, rule := range []string{"secret-key-name", "private-key", "credential-store"} {
				if !strings.Contains(text, marker+rule+")]") {
					t.Errorf("each withheld line should name what it held: %s", text)
				}
			}
		})
	}
}

func TestAGrepOfASkippedPathKeepsItsLines(t *testing.T) {
	skipped := newGuard(t, true, skipFixtures)
	test.WriteFile(t, filepath.Join(skipped.tree.Project, "fixtures", "app.yaml"), "password: hunter2\n")

	call := skipped.output(context.Background(), "Grep", map[string]any{"pattern": "password", "path": "{project}"},
		map[string]any{"content": "fixtures/app.yaml:1:password: hunter2"})
	if call.answered {
		t.Error("a skipped path's lines should reach the agent as the search printed them")
	}
}

func TestABashRefusalHandsBackAtMostItsLimitOfLines(t *testing.T) {
	g := newGuard(t, true, "")
	test.WriteFile(t, filepath.Join(g.tree.Project, ".env"), "API_TOKEN=q8Lx2v\n"+strings.Repeat("DEBUG=1\n", 2499))

	decision, reason := g.judge("cat .env", "{project}")
	if decision != "deny" || !strings.Contains(reason, "  2000\tDEBUG=1\n... 500 more line(s) not shown") ||
		strings.Contains(reason, "  2001\t") {
		t.Errorf("a refusal should hand back a bounded copy and say how much it left out: %s", reason[max(0, len(reason)-200):])
	}
}

func TestALongLineIsCutInTheCopy(t *testing.T) {
	g := newGuard(t, true, "")
	test.WriteFile(t, filepath.Join(g.tree.Project, "app.yaml"), "password: q8Lx2v\nnote: "+strings.Repeat("é", 2500)+"\n")

	_, reason := g.readFile("app.yaml")
	if !strings.HasSuffix(reason, "     2\tnote: "+strings.Repeat("é", 1994)) {
		t.Error("a line in the copy should be cut at the length Read cuts it to, counted in characters")
	}
}

func TestAReadCallThatCannotBeReadIsRefused(t *testing.T) {
	g := newGuard(t, true, "")

	var out, errs bytes.Buffer

	status := shellguard.Run(context.Background(), []string{"read"}, g.env, shellguard.Streams{In: strings.NewReader("{"), Out: &out, Err: &errs})
	if status != 2 || errs.String() != "agentic-shell-guard: could not read the call; refusing\n" {
		t.Errorf("a call the hook cannot read should be blocked, got %d %q", status, errs.String())
	}
}

func TestAReadNamingNoLimitIsJudgedOverWhatReadReturns(t *testing.T) {
	g := newGuard(t, true, "")
	test.WriteFile(t, filepath.Join(g.tree.Project, "app.yaml"), strings.Repeat("port: 1\n", 2400)+"password: q8Lx2v\n")

	decision, _ := g.readFile("app.yaml")
	if decision != "allow" {
		t.Error("a credential past the lines a Read returns by default should not refuse it")
	}

	decision, _ = g.read("Read", map[string]any{"file_path": "app.yaml", "offset": 2000})
	if decision != "deny" {
		t.Error("a Read whose default range reaches the credential should be refused")
	}
}
