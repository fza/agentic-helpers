package shellguard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/pieces"
	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/secrets"
)

const (
	markerWithheld      = "[agentic-shell-guard: credential withheld (%s)]"
	noteWithheld        = "agentic-shell-guard: %d line(s) of this %s output held what looks like a credential and were withheld; the source is unchanged"
	noteScanUnfinished  = "agentic-shell-guard: the credential scan of this %s output could not finish, so all of it was withheld (%d line(s)); the source is unchanged"
	reasonOutputUnread  = "could not read the tool output; it reached the agent unscanned"
	reasonOutputFailure = "internal failure; the tool output reached the agent unscanned"
	reasonOutputAnswer  = "could not answer; the tool output reached the agent unscanned"
)

// fileTools are the tools judged by what they read, and editTools those whose
// output is always scanned, under `all` and `files` alike; every other tool's
// output is scanned only under `all`.
var (
	fileTools = []string{"Read", "Grep", "NotebookRead", "Bash"}
	editTools = []string{"Edit", "Write", "NotebookEdit"}
)

type outputPayload struct {
	Cwd          string          `json:"cwd"`
	ToolName     string          `json:"tool_name"`
	ToolInput    json.RawMessage `json:"tool_input"`
	ToolResponse json.RawMessage `json:"tool_response"`
}

type outputInput struct {
	FilePath     string  `json:"file_path"`
	NotebookPath string  `json:"notebook_path"`
	Path         string  `json:"path"`
	Command      *string `json:"command"`
}

type outputAnswer struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		UpdatedToolOutput any    `json:"updatedToolOutput"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// Output answers one PostToolUse call: a tool's output from outside the
// project has every line holding what looks like a credential replaced
// before the agent sees it. The tool has run, so nothing can block it; a
// failure says so on stderr, exit 1, which Claude Code shows the user and
// which leaves the output as the tool returned it.
func Output(ctx context.Context, env Env, streams Streams) (code int) {
	defer func() {
		failure := recover()
		if failure != nil {
			_, _ = fmt.Fprintln(streams.Err, self+": "+reasonOutputFailure)
			code = 1
		}
	}()

	read, err := pluginconfig.ReadWith(env.Settings, env.Getenv)
	if err != nil {
		read = pluginconfig.Defaults()
	}

	if !pieces.ShellGuard.On(read) || read.ShellGuardSecretOutput == pluginconfig.ShellGuardOff {
		return 0
	}

	var call outputPayload

	err = json.NewDecoder(streams.In).Decode(&call)
	if err != nil {
		_, _ = fmt.Fprintln(streams.Err, self+": "+reasonOutputUnread)

		return 1
	}

	if skipped(read.ShellGuardSecretOutputSkip, call.ToolName) || !fromOutside(env, read.ShellGuardSecretOutput, call) {
		return 0
	}

	response, err := decodeResponse(call.ToolResponse)
	if err != nil {
		_, _ = fmt.Fprintln(streams.Err, self+": "+reasonOutputUnread)

		return 1
	}

	redacted, withheld, unfinished := Redact(ctx, response)
	if withheld == 0 {
		return 0
	}

	var reply outputAnswer

	reply.HookSpecificOutput.HookEventName = "PostToolUse"
	reply.HookSpecificOutput.UpdatedToolOutput = redacted
	reply.HookSpecificOutput.AdditionalContext = fmt.Sprintf(noteWithheld, withheld, call.ToolName)
	if unfinished {
		reply.HookSpecificOutput.AdditionalContext = fmt.Sprintf(noteScanUnfinished, call.ToolName, withheld)
	}

	err = json.NewEncoder(streams.Out).Encode(reply)
	if err != nil {
		_, _ = fmt.Fprintln(streams.Err, self+": "+reasonOutputAnswer)

		return 1
	}

	return 0
}

func decodeResponse(raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	var response any

	err := decoder.Decode(&response)
	if err != nil {
		return nil, fmt.Errorf("decoding the tool response: %w", err)
	}

	return response, nil
}

// skipped is whether the tool matches a name or pattern of
// `shell_guard_secret_output_skip`, comma-separated. A pattern that does not
// compile matches nothing, so a typo never lets output through.
func skipped(list string, tool string) bool {
	for _, pattern := range strings.Split(list, ",") {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}

		matched, err := path.Match(pattern, tool)
		if err == nil && matched {
			return true
		}
	}

	return false
}

// fromOutside is whether the call's output counts as coming from outside the
// project: Read, Grep and NotebookRead by the path they name, Bash by
// whether it only reads project files, every other tool under `all`. Edit,
// Write and NotebookEdit always count as outside: their path is judged only
// after the write, when a link swapped in between could steer it, and inside
// the project scanning costs only a withheld line of the file they echo
// (Edit's response carries the whole file it edited).
func fromOutside(env Env, setting string, call outputPayload) bool {
	if slices.Contains(editTools, call.ToolName) {
		return true
	}

	var input outputInput

	err := json.Unmarshal(call.ToolInput, &input)
	if err != nil && slices.Contains(fileTools, call.ToolName) {
		return true
	}

	root := projectRoot(env)

	switch call.ToolName {
	case "Read":
		return !insideProject(input.FilePath, call.Cwd, env.Home, root)
	case "NotebookRead":
		return !insideProject(input.NotebookPath, call.Cwd, env.Home, root)
	case "Grep":
		target := input.Path
		if target == "" {
			target = call.Cwd
		}

		return !insideProject(target, call.Cwd, env.Home, root)
	case "Bash":
		return input.Command == nil || !ReadsOnlyProject(*input.Command, call.Cwd, env)
	}

	return setting == pluginconfig.SecretOutputAll
}

// Redact replaces, in every string of a decoded tool response, each line
// holding what looks like a credential with the marker naming its rules, and
// returns the response in the same shape with the count of lines withheld.
// A scan that cannot finish withholds every string whole, and reports so.
func Redact(ctx context.Context, response any) (any, int, bool) {
	withheld := 0
	unfinished := false

	redacted := mapStrings(response, func(text string) string {
		if unfinished {
			return text
		}

		findings := secrets.Scan(ctx, text, "")
		if slices.ContainsFunc(findings, func(finding secrets.Finding) bool { return finding.RuleID == secrets.Incomplete }) {
			unfinished = true

			return text
		}

		replaced, lines := withholdLines(text, findings)
		withheld += lines

		return replaced
	})

	if !unfinished {
		return redacted, withheld, false
	}

	withheld = 0
	marker := fmt.Sprintf(markerWithheld, secrets.Incomplete)

	return mapStrings(response, func(text string) string {
		if text == "" {
			return text
		}

		withheld += strings.Count(text, "\n") + 1

		return marker
	}), withheld, true
}

func mapStrings(value any, replace func(string) string) any {
	switch typed := value.(type) {
	case string:
		return replace(typed)
	case []any:
		mapped := make([]any, len(typed))
		for index, item := range typed {
			mapped[index] = mapStrings(item, replace)
		}

		return mapped
	case map[string]any:
		mapped := make(map[string]any, len(typed))
		for key, item := range typed {
			mapped[key] = mapStrings(item, replace)
		}

		return mapped
	}

	return value
}

func withholdLines(text string, findings []secrets.Finding) (string, int) {
	if len(findings) == 0 {
		return text, 0
	}

	lines := strings.Split(text, "\n")
	rules := map[int][]string{}

	for _, finding := range findings {
		for line := finding.Line; line <= max(finding.LastLine, finding.Line) && line <= len(lines); line++ {
			if !slices.Contains(rules[line], finding.RuleID) {
				rules[line] = append(rules[line], finding.RuleID)
			}
		}
	}

	for line, ids := range rules {
		slices.Sort(ids)
		lines[line-1] = fmt.Sprintf(markerWithheld, strings.Join(ids, ", "))
	}

	return strings.Join(lines, "\n"), len(rules)
}
