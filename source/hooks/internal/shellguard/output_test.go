package shellguard_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/shellguard"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const marker = "[agentic-shell-guard: credential withheld ("

type outputReply struct {
	HookSpecificOutput struct {
		HookEventName     string  `json:"hookEventName"`
		UpdatedToolOutput any     `json:"updatedToolOutput"`
		AdditionalContext *string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// outputCall is one tool call's output put to the hook: whether it answered,
// and what it answered.
type outputCall struct {
	answered bool
	reply    outputReply
	status   int
	stderr   string
}

func (g guard) output(ctx context.Context, tool string, input any, response any) outputCall {
	g.t.Helper()

	fields, isObject := input.(map[string]any)
	for key, value := range fields {
		text, isText := value.(string)
		if isText && isObject {
			fields[key] = g.placeholders.Replace(text)
		}
	}

	var out, errs bytes.Buffer

	payload := test.PostToolUse(g.t, g.tree.Project, tool, input, response)
	status := shellguard.Run(ctx, []string{"output"}, g.env, shellguard.Streams{In: bytes.NewReader(payload), Out: &out, Err: &errs})

	call := outputCall{answered: out.Len() > 0, status: status, stderr: errs.String()}
	if !call.answered {
		return call
	}

	decoder := json.NewDecoder(&out)
	decoder.UseNumber()

	err := decoder.Decode(&call.reply)
	if err != nil {
		g.t.Fatalf("decoding the answer: %v", err)
	}

	return call
}

// providerFiles writes the provider config outside the project, inside it,
// and as a file symlink inside it pointing outside.
func providerFiles(t *testing.T, g guard) (string, string, string) {
	t.Helper()

	content, token, key := test.ProviderConfig(t)
	test.WriteFile(t, filepath.Join(g.tree.Outside, "config.yaml"), content)
	test.WriteFile(t, filepath.Join(g.tree.Project, "config.yaml"), content)
	test.WriteFile(t, filepath.Join(g.tree.Project, "show.sh"), "cat ../../outside/config.yaml\n")

	err := os.Symlink(filepath.Join(g.tree.Outside, "config.yaml"), filepath.Join(g.tree.Project, "linked.yaml"))
	if err != nil {
		t.Fatalf("linking the outside file: %v", err)
	}

	return content, token, key
}

func readResponse(path string, content string) map[string]any {
	lines := strings.Count(content, "\n")

	return map[string]any{"type": "text", "file": map[string]any{
		"filePath": path, "content": content, "numLines": lines, "startLine": 1, "totalLines": lines,
	}}
}

func bashResponse(stdout string) map[string]any {
	return map[string]any{"stdout": stdout, "stderr": "", "interrupted": false, "isImage": false}
}

// assertWithheld holds every string of the redacted response to the original
// line by line: a line holding the token or a line of the key is the marker,
// naming its rule, and every other line is unchanged. Every other value keeps
// its place and value.
func assertWithheld(t *testing.T, original any, redacted any, token string, key string) {
	t.Helper()

	keyLines := strings.Split(key, "\n")

	switch typed := original.(type) {
	case string:
		text, isText := redacted.(string)
		if !isText {
			t.Fatalf("a string should stay a string, got %#v", redacted)
		}

		was, now := strings.Split(typed, "\n"), strings.Split(text, "\n")
		if len(was) != len(now) {
			t.Fatalf("a redaction should keep every line, got %q", text)
		}

		for index, line := range was {
			switch {
			case strings.Contains(line, token):
				if !strings.HasPrefix(now[index], marker) || !strings.Contains(now[index], "github-pat") {
					t.Errorf("the token's line should be withheld naming its rule, got %q", now[index])
				}
			case contains(keyLines, line):
				if !strings.HasPrefix(now[index], marker) || !strings.Contains(now[index], "private-key") {
					t.Errorf("every line of the key should be withheld naming its rule, got %q", now[index])
				}
			case now[index] != line:
				t.Errorf("a line holding no credential should reach the agent unchanged, got %q", now[index])
			}
		}
	case map[string]any:
		mapped, isMap := redacted.(map[string]any)
		if !isMap || len(mapped) != len(typed) {
			t.Fatalf("an object should keep its keys, got %#v", redacted)
		}

		for name, value := range typed {
			assertWithheld(t, value, mapped[name], token, key)
		}
	case []any:
		listed, isList := redacted.([]any)
		if !isList || len(listed) != len(typed) {
			t.Fatalf("a list should keep its items, got %#v", redacted)
		}

		for index, value := range typed {
			assertWithheld(t, value, listed[index], token, key)
		}
	default:
		if !reflect.DeepEqual(normalised(t, original), normalised(t, redacted)) {
			t.Errorf("a value other than text should reach the agent as the tool returned it, got %#v", redacted)
		}
	}
}

func contains(lines []string, line string) bool {
	for _, candidate := range lines {
		if candidate == line {
			return true
		}
	}

	return false
}

// normalised is a value as a JSON round trip with numbers kept reads it.
func normalised(t *testing.T, value any) any {
	t.Helper()

	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encoding %v: %v", value, err)
	}

	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()

	var decoded any

	err = decoder.Decode(&decoded)
	if err != nil {
		t.Fatalf("decoding %s: %v", encoded, err)
	}

	return decoded
}

func TestOutputFromOutsideTheProjectIsWithheldLineByLine(t *testing.T) {
	cases := []struct {
		name     string
		tool     string
		input    map[string]any
		bash     bool
		withheld bool
	}{
		{name: "Read outside", tool: "Read", input: map[string]any{"file_path": "{outside}/config.yaml"}, withheld: true},
		{name: "Edit outside", tool: "Edit", input: map[string]any{"file_path": "{outside}/config.yaml"}, withheld: true},
		{name: "Edit inside", tool: "Edit", input: map[string]any{"file_path": "{project}/config.yaml"}, withheld: true},
		{name: "Edit a linked file", tool: "Edit", input: map[string]any{"file_path": "{project}/linked.yaml"}, withheld: true},
		{name: "Write outside", tool: "Write", input: map[string]any{"file_path": "{outside}/config.yaml"}, withheld: true},
		{name: "Write inside", tool: "Write", input: map[string]any{"file_path": "{project}/config.yaml"}, withheld: true},
		{name: "NotebookEdit outside", tool: "NotebookEdit", input: map[string]any{"notebook_path": "{outside}/config.yaml"}, withheld: true},
		{name: "NotebookEdit inside", tool: "NotebookEdit", input: map[string]any{"notebook_path": "{project}/config.yaml"}, withheld: true},
		{name: "Read inside", tool: "Read", input: map[string]any{"file_path": "{project}/config.yaml"}},
		{name: "Read relative inside", tool: "Read", input: map[string]any{"file_path": "config.yaml"}},
		{name: "Read through a linked directory", tool: "Read", input: map[string]any{"file_path": "{project}/link/config.yaml"}, withheld: true},
		{name: "Read a linked file", tool: "Read", input: map[string]any{"file_path": "{project}/linked.yaml"}, withheld: true},
		{name: "Read the home directory", tool: "Read", input: map[string]any{"file_path": "{home}/config.yaml"}, withheld: true},
		{name: "Read naming no path", tool: "Read", input: map[string]any{}, withheld: true},
		{name: "NotebookRead outside", tool: "NotebookRead", input: map[string]any{"notebook_path": "{outside}/config.yaml"}, withheld: true},
		{name: "NotebookRead inside", tool: "NotebookRead", input: map[string]any{"notebook_path": "{project}/config.yaml"}},
		{name: "Grep outside", tool: "Grep", input: map[string]any{"pattern": "token", "path": "{outside}"}, withheld: true},
		{name: "Grep in the working directory", tool: "Grep", input: map[string]any{"pattern": "token"}},
		{name: "Grep inside", tool: "Grep", input: map[string]any{"pattern": "token", "path": "{project}/sub"}},
		{name: "cat outside", tool: "Bash", bash: true, input: map[string]any{"command": "cat {outside}/config.yaml"}, withheld: true},
		{name: "cat through $HOME", tool: "Bash", bash: true, input: map[string]any{"command": `cat "$HOME"/config.yaml`}, withheld: true},
		{name: "cd then cat", tool: "Bash", bash: true, input: map[string]any{"command": "cd {outside} && cat config.yaml"}, withheld: true},
		{name: "a script", tool: "Bash", bash: true, input: map[string]any{"command": "./show.sh"}, withheld: true},
		{name: "a script run by bash", tool: "Bash", bash: true, input: map[string]any{"command": "bash show.sh"}, withheld: true},
		{name: "cat inside", tool: "Bash", bash: true, input: map[string]any{"command": "cat config.yaml"}},
		{name: "grep inside", tool: "Bash", bash: true, input: map[string]any{"command": "grep token {project}/config.yaml"}},
		{name: "Bash naming no command", tool: "Bash", bash: true, input: map[string]any{}, withheld: true},
		{name: "WebFetch", tool: "WebFetch", input: map[string]any{"url": "https://example.com/config"}, withheld: true},
		{name: "an MCP tool", tool: "mcp__notes__read", input: map[string]any{"id": "x"}, withheld: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newGuard(t, false, "")
			content, token, key := providerFiles(t, g)

			var response any = readResponse(g.placeholders.Replace("{outside}/config.yaml"), content)
			if tc.bash {
				response = bashResponse(content)
			}

			if strings.HasPrefix(tc.tool, "mcp__") {
				response = []any{map[string]any{"type": "text", "text": content}}
			}

			if tc.tool == "Edit" {
				response = map[string]any{
					"filePath": "config.yaml", "oldString": "provider: openai", "newString": "provider: ollama",
					"originalFile": content, "structuredPatch": []any{}, "userModified": false, "replaceAll": false,
				}
			}

			if tc.tool == "WebFetch" {
				response = map[string]any{"result": content, "code": 200, "durationMs": 12}
			}

			call := g.output(context.Background(), tc.tool, tc.input, response)
			if call.status != 0 {
				t.Fatalf("the hook should answer, exit %d: %s", call.status, call.stderr)
			}

			if !tc.withheld {
				if call.answered {
					t.Errorf("output from inside the project should reach the agent untouched, got %+v", call.reply)
				}

				return
			}

			if !call.answered {
				t.Fatal("a credential in output from outside the project should be withheld")
			}

			assertWithheld(t, response, call.reply.HookSpecificOutput.UpdatedToolOutput, token, key)

			if call.reply.HookSpecificOutput.HookEventName != "PostToolUse" || call.reply.HookSpecificOutput.AdditionalContext != nil {
				t.Errorf("a finished scan should leave its inline markers to speak and add no note, got %+v", call.reply.HookSpecificOutput)
			}
		})
	}
}

func TestAToolInputItCannotReadCountsAsOutside(t *testing.T) {
	for _, tool := range []string{"Read", "Grep", "NotebookRead", "Edit", "Write", "NotebookEdit", "Bash"} {
		t.Run(tool, func(t *testing.T) {
			g := newGuard(t, false, `"shell_guard_secret_output":"files"`)
			content, _, _ := test.ProviderConfig(t)

			call := g.output(context.Background(), tool, "cat config.yaml", bashResponse(content))
			if !call.answered {
				t.Error("a file tool's input that is no object should count as reading outside the project")
			}
		})
	}
}

func TestEditToolOutputIsScannedWhateverItsPath(t *testing.T) {
	modes := []struct {
		options  string
		withheld bool
	}{
		{options: `"shell_guard_secret_output":"all"`, withheld: true},
		{options: `"shell_guard_secret_output":"files"`, withheld: true},
		{options: `"shell_guard_secret_output":"off"`, withheld: false},
	}

	for _, mode := range modes {
		for _, tool := range []string{"Edit", "Write", "NotebookEdit"} {
			t.Run(mode.options+" "+tool, func(t *testing.T) {
				g := newGuard(t, false, mode.options)
				content, _, _ := providerFiles(t, g)

				input := map[string]any{"file_path": "{project}/config.yaml", "notebook_path": "{project}/config.yaml"}

				call := g.output(context.Background(), tool, input, readResponse("config.yaml", content))
				if call.status != 0 || call.answered != mode.withheld {
					t.Errorf("an edit tool's output should be scanned in every mode but off, even inside the project, got %+v", call)
				}
			})
		}
	}
}

func TestCleanOutputFromOutsideIsUntouched(t *testing.T) {
	g := newGuard(t, false, "")

	call := g.output(context.Background(), "Read", map[string]any{"file_path": "{outside}/notes.md"},
		readResponse("notes.md", test.Fixture(t, "secrets/clean.txt")))
	if call.status != 0 || call.answered {
		t.Errorf("output holding no credential should reach the agent untouched, got %+v", call)
	}
}

func TestTheOptionsDecideWhichOutputIsScanned(t *testing.T) {
	tools := []string{"WebFetch", "mcp__sdd__search", "Read", "Edit", "Write", "NotebookEdit"}

	cases := []struct {
		name     string
		options  string
		withheld []bool
	}{
		{name: "all unless set", options: ``, withheld: []bool{true, true, true, true, true, true}},
		{name: "all", options: `"shell_guard_secret_output":"all"`, withheld: []bool{true, true, true, true, true, true}},
		{name: "files", options: `"shell_guard_secret_output":"files"`, withheld: []bool{false, false, true, true, true, true}},
		{name: "off", options: `"shell_guard_secret_output":"off"`, withheld: []bool{false, false, false, false, false, false}},
		{name: "a tool skipped by name", options: `"shell_guard_secret_output_skip":"WebFetch, Read, Write"`, withheld: []bool{false, true, false, true, false, true}},
		{name: "a tool skipped by pattern", options: `"shell_guard_secret_output_skip":"mcp__sdd__*"`, withheld: []bool{true, false, true, true, true, true}},
		{name: "a pattern that does not compile skips nothing", options: `"shell_guard_secret_output_skip":"[mcp"`, withheld: []bool{true, true, true, true, true, true}},
		{name: "shell-guard off", options: `"shell_guard_enabled":false`, withheld: []bool{false, false, false, false, false, false}},
	}

	for _, tc := range cases {
		for index, tool := range tools {
			t.Run(tc.name+" "+tool, func(t *testing.T) {
				g := newGuard(t, false, tc.options)
				content, _, _ := test.ProviderConfig(t)

				call := g.output(context.Background(), tool, map[string]any{"file_path": "{outside}/config.yaml", "notebook_path": "{outside}/config.yaml"}, bashResponse(content))
				if call.status != 0 {
					t.Fatalf("the hook should answer, exit %d: %s", call.status, call.stderr)
				}

				if call.answered != tc.withheld[index] {
					t.Errorf("the options should decide whether this tool's output is scanned, withheld %v", call.answered)
				}
			})
		}
	}
}

func TestAScanThatCannotFinishWithholdsEveryString(t *testing.T) {
	g := newGuard(t, false, "")

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	response := map[string]any{"stdout": "first\nsecond", "stderr": "warning", "empty": "", "interrupted": false,
		"isImage": false, "lines": []any{"third", 3}}

	call := g.output(cancelled, "Bash", map[string]any{"command": "./build.sh"}, response)
	if !call.answered {
		t.Fatal("a scan that could not finish should withhold the output rather than pass it")
	}

	got := call.reply.HookSpecificOutput.UpdatedToolOutput
	want := normalised(t, map[string]any{
		"stdout": "[agentic-shell-guard: credential withheld (scan-incomplete)]", "stderr": "[agentic-shell-guard: credential withheld (scan-incomplete)]",
		"empty": "", "interrupted": false, "isImage": false,
		"lines": []any{"[agentic-shell-guard: credential withheld (scan-incomplete)]", 3},
	})

	if !reflect.DeepEqual(got, want) {
		t.Errorf("every string should be withheld and every other value kept in place, got %#v", got)
	}

	note := "agentic-shell-guard: the credential scan of this Bash output could not finish, so all of it was withheld (4 line(s)); the source is unchanged"
	said := call.reply.HookSpecificOutput.AdditionalContext
	if said == nil || *said != note {
		t.Errorf("the note should say the scan could not finish, got %+v", call.reply.HookSpecificOutput)
	}
}

func TestAnEditsUnfinishedScanCarriesNoNote(t *testing.T) {
	for _, tool := range []string{"Edit", "Write", "NotebookEdit"} {
		t.Run(tool, func(t *testing.T) {
			g := newGuard(t, false, "")

			cancelled, cancel := context.WithCancel(context.Background())
			cancel()

			call := g.output(cancelled, tool, map[string]any{"file_path": "{project}/config.yaml"}, map[string]any{"filePath": "config.yaml", "content": "x"})
			if !call.answered {
				t.Fatal("a scan that could not finish should withhold the output rather than pass it")
			}

			if call.reply.HookSpecificOutput.AdditionalContext != nil {
				t.Errorf("an edit's result should carry its markers and no note, got %q", *call.reply.HookSpecificOutput.AdditionalContext)
			}
		})
	}
}

func TestAnAnswerItCannotWriteSaysTheOutputWentUnscanned(t *testing.T) {
	g := newGuard(t, false, "")
	content, _, _ := test.ProviderConfig(t)

	var errs bytes.Buffer

	payload := test.PostToolUse(t, g.tree.Project, "WebFetch", map[string]any{}, bashResponse(content))
	status := shellguard.Run(context.Background(), []string{"output"}, g.env, shellguard.Streams{
		In: bytes.NewReader(payload), Out: test.FailingWriter{Err: os.ErrClosed}, Err: &errs,
	})
	if status != 1 || !strings.Contains(errs.String(), "agentic-shell-guard: could not answer; the tool output reached the agent unscanned") {
		t.Errorf("an answer that cannot be written should say on stderr that the output went unscanned, exit %d: %s", status, errs.String())
	}
}

func TestAnUnreadableCallSaysTheOutputWentUnscanned(t *testing.T) {
	g := newGuard(t, false, "")

	var out, errs bytes.Buffer

	status := shellguard.Run(context.Background(), []string{"output"}, g.env, shellguard.Streams{
		In: strings.NewReader("{"), Out: &out, Err: &errs,
	})
	if status != 1 || out.Len() != 0 || !strings.Contains(errs.String(), "agentic-shell-guard: could not read the tool output; it reached the agent unscanned") {
		t.Errorf("an unreadable call should say on stderr, without blocking, that the output went unscanned, exit %d: %s", status, errs.String())
	}
}
