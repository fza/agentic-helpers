package tools_test

import (
	"cmp"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

var credentialRefusal = regexp.MustCompile(`^agentic-tools: (\S+) looks like a credential \(([a-z0-9-]+(?:, [a-z0-9-]+)*), line (\d+)\); the library never holds one$`)

// checkCredentialRefusal holds a refusal to the wording the agent-tools skill
// quotes, naming where the credential lies and on which line, never the
// credential itself.
func checkCredentialRefusal(t *testing.T, text string, where string, line string, rule string, credential string) {
	t.Helper()

	parts := credentialRefusal.FindStringSubmatch(strings.TrimSpace(text))
	if parts == nil {
		t.Fatalf("the refusal should follow the credential wording, got %q", text)
	}

	if parts[1] != where || parts[3] != line || !strings.Contains(parts[2], rule) {
		t.Errorf("the refusal should name the file or flag, the rule and the line, got %q", text)
	}

	if strings.Contains(text, credential) {
		t.Error("the refusal must never repeat the credential")
	}
}

func TestWritesRefuseACredential(t *testing.T) {
	credential := test.SecretFixture(t, "positive/github-pat.txt")
	token := credential[strings.Index(credential, "ghp_"):strings.LastIndex(credential, `"`)]

	cases := []struct {
		name  string
		args  func(h *harness) []string
		where string
		line  string
		rule  string
	}{
		{
			name: "add, the script",
			args: func(h *harness) []string {
				return []string{"add", "leak-tool", "--from", h.file("scripts/leak.py", "import os\n"+credential+"\n"),
					"--summary", "A tool holding a token", "--usage", "python3 leak.py"}
			},
			where: "leak.py",
			line:  "2",
		},
		{
			name: "add, a file of a folder",
			args: func(h *harness) []string {
				h.file("scripts/leak/main.sh", "echo fine\n")
				h.file("scripts/leak/conf/env.sh", credential+"\n")

				return []string{"add", "leak-tool", "--from", filepath.Join(h.repo, "scripts", "leak"),
					"--summary", "A tool holding a token", "--usage", "sh main.sh"}
			},
			where: "conf/env.sh",
			line:  "1",
		},
		{
			name: "add, a file named as a key store",
			args: func(h *harness) []string {
				h.file("scripts/signer/sign.sh", "openssl smime -sign\n")
				h.file("scripts/signer/client.p12", "binary\n")

				return []string{"add", "leak-tool", "--from", filepath.Join(h.repo, "scripts", "signer"),
					"--summary", "Sign a file", "--usage", "sh sign.sh"}
			},
			where: "client.p12",
			line:  "1",
			rule:  "pkcs12-file",
		},
		{
			name: "add, the summary",
			args: func(h *harness) []string {
				return []string{"add", "leak-tool", "--from", h.file("scripts/leak.py", "print(1)\n"),
					"--summary", "Uses " + token, "--usage", "python3 leak.py"}
			},
			where: "--summary",
			line:  "1",
		},
		{
			name: "add, the usage",
			args: func(h *harness) []string {
				return []string{"add", "leak-tool", "--from", h.file("scripts/leak.py", "print(1)\n"),
					"--summary", "A tool", "--usage", "python3 leak.py\n" + credential}
			},
			where: "--usage",
			line:  "2",
		},
		{
			name: "add, the distinct reason",
			args: func(h *harness) []string {
				return []string{"add", "leak-tool", "--from", h.file("scripts/leak.py", "print(1)\n"),
					"--summary", "A tool", "--usage", "python3 leak.py", "--distinct", "uses " + token}
			},
			where: "--distinct",
			line:  "1",
		},
		{
			name: "update, the new script",
			args: func(h *harness) []string {
				return []string{"update", "word-count", "--from", h.file("scripts/word-count.py", credential+"\n"),
					"--why", "token"}
			},
			where: "word-count.py",
			line:  "1",
		},
		{
			name: "update, the inputs",
			args: func(h *harness) []string {
				return []string{"update", "word-count", "--inputs", "a file\n" + credential, "--why", "inputs"}
			},
			where: "--inputs",
			line:  "2",
		},
		{
			name: "update, the outputs",
			args: func(h *harness) []string {
				return []string{"update", "word-count", "--outputs", credential, "--why", "outputs"}
			},
			where: "--outputs",
			line:  "1",
		},
		{
			name: "update, the reason",
			args: func(h *harness) []string {
				return []string{"update", "word-count", "--summary", "Count words anew", "--why", "uses " + token}
			},
			where: "--why",
			line:  "1",
		},
		{
			name: "update forking a foreign tool whose files hold one",
			args: func(h *harness) []string {
				other := h.elsewhere(otherRepo)
				other.tool("line-count", "Count the lines of a file quickly")
				test.WriteFile(t, filepath.Join(other.store(), "line-count", "v1", "line-count.py"), "x = 1\n"+credential+"\n")

				return []string{"update", "line-count", "--from", h.file("scripts/line-count.py", "print(0)\n"),
					"--summary", "Count the lines, blank ones too", "--why", "blank"}
			},
			where: "line-count.py",
			line:  "2",
		},
		{
			name: "merge, the survivor's next files",
			args: func(h *harness) []string {
				h.tool("count-words", "Count words another way", "--distinct", "another way")

				return []string{"merge", "word-count", "count-words", "--from", h.file("merged/wc.py", credential+"\n"),
					"--why", "one tool"}
			},
			where: "wc.py",
			line:  "1",
		},
		{
			name: "add, a file named after a path gitleaks allowlists",
			args: func(h *harness) []string {
				h.file("scripts/leak/main.sh", "echo fine\n")
				h.file("scripts/leak/package-lock.json", "{}\n"+credential+"\n")

				return []string{"add", "leak-tool", "--from", filepath.Join(h.repo, "scripts", "leak"),
					"--summary", "A tool holding a token", "--usage", "sh main.sh"}
			},
			where: "package-lock.json",
			line:  "2",
		},
		{
			name: "update forking a foreign tool whose description holds one",
			args: func(h *harness) []string {
				other := h.elsewhere(otherRepo)
				other.tool("line-count", "Count the lines of a file quickly")

				described := filepath.Join(other.store(), "line-count", "v1", "tool.json")
				test.WriteFile(t, described, strings.Replace(other.read("line-count/v1/tool.json"),
					"Count the lines of a file quickly", "Count lines with "+token, 1))
				_ = os.Remove(filepath.Join(other.store(), "index.json"))

				return []string{"update", "line-count", "--summary", "Count the lines, blank ones too", "--why", "blank"}
			},
			where: "--summary",
			line:  "1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.tool("word-count", "Count the words of standard input")

			args := tc.args(h)
			current := h.read("word-count/current")
			calls := h.embedder.Calls()

			got := h.run("", args...)

			if got.code != 1 || !strings.HasPrefix(got.err, "refused: ") {
				t.Fatalf("the write should be refused, exit %d: %s%s", got.code, got.out, got.err)
			}

			checkCredentialRefusal(t, strings.TrimPrefix(got.err, "refused: "), tc.where, tc.line, cmp.Or(tc.rule, "github-pat"), token)

			if h.embedder.Calls() != calls {
				t.Error("a refused write should never reach the embedding endpoint")
			}

			if test.Exists(t, filepath.Join(h.store(), "leak-tool")) || test.Exists(t, filepath.Join(h.store(), "line-count")) ||
				h.read("word-count/current") != current {
				t.Error("a refused write should keep nothing")
			}
		})
	}
}

func TestThePromptOfferNeverEmbedsAPromptHoldingACredential(t *testing.T) {
	h := newHarness(t)
	h.tool("markdown-words", "Count markdown section words")

	calls := h.embedder.Calls()
	prompt := "count markdown section words\n" + test.SecretFixture(t, "positive/github-pat.txt")

	got := h.hook("prompt", map[string]any{"prompt": prompt})

	if got != "" {
		t.Errorf("the offer should stay silent on a prompt holding a credential, agentic-memory telling the agent, got %q", got)
	}

	if h.embedder.Calls() != calls {
		t.Error("a prompt holding a credential should never reach the embedding endpoint")
	}

	if !strings.Contains(h.hook("prompt", map[string]any{"prompt": "count markdown section words"}), "markdown-words") {
		t.Error("a clean prompt should still be offered the near tool")
	}
}

func TestTheMCPAddRefusesACredential(t *testing.T) {
	h := newHarness(t)
	credential := test.SecretFixture(t, "positive/github-pat.txt")
	from := h.file("scripts/leak.py", credential+"\n")

	arguments, err := json.Marshal(map[string]any{"name": "leak-tool", "from": from, "summary": "A tool holding a token",
		"usage": "python3 leak.py"})
	if err != nil {
		t.Fatal(err)
	}

	lines := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"add","arguments":` + string(arguments) + `}}`,
	}

	out := h.must2(strings.Join(lines, "\n")+"\n", "mcp")

	var answer struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}

	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.Contains(line, `"id":2`) {
			err = json.Unmarshal([]byte(line), &answer)
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	if !answer.Result.IsError || len(answer.Result.Content) != 1 || !strings.HasPrefix(test.Readable(answer.Result.Content[0].Text), "refused: ") {
		t.Fatalf("the MCP add should be refused as an error, got %s", out)
	}

	checkCredentialRefusal(t, strings.TrimPrefix(test.Readable(answer.Result.Content[0].Text), "refused: "), "leak.py", "1", "github-pat",
		credential[strings.Index(credential, "ghp_"):strings.LastIndex(credential, `"`)])

	if test.Exists(t, filepath.Join(h.store(), "leak-tool")) {
		t.Error("a refused MCP add should keep nothing")
	}
}

func TestTheMCPUpdateRefusesACredential(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")

	credential := test.SecretFixture(t, "positive/github-pat.txt")
	token := credential[strings.Index(credential, "ghp_"):strings.LastIndex(credential, `"`)]
	current := h.read("word-count/current")
	calls := h.embedder.Calls()

	arguments, err := json.Marshal(map[string]any{"name": "word-count", "summary": "Count words anew", "why": "uses " + token})
	if err != nil {
		t.Fatal(err)
	}

	lines := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"update","arguments":` + string(arguments) + `}}`,
	}

	out := h.must2(strings.Join(lines, "\n")+"\n", "mcp")

	var answer struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}

	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.Contains(line, `"id":2`) {
			err = json.Unmarshal([]byte(line), &answer)
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	if !answer.Result.IsError || len(answer.Result.Content) != 1 || !strings.HasPrefix(test.Readable(answer.Result.Content[0].Text), "refused: ") {
		t.Fatalf("the MCP update should be refused as an error, got %s", out)
	}

	checkCredentialRefusal(t, strings.TrimPrefix(test.Readable(answer.Result.Content[0].Text), "refused: "), "--why", "1", "github-pat", token)

	if h.read("word-count/current") != current || h.embedder.Calls() != calls {
		t.Error("a refused MCP update should keep nothing and embed nothing")
	}
}
