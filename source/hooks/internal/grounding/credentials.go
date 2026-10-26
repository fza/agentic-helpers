package grounding

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/secrets"
)

// leaksCredential refuses a graph write holding what looks like a credential:
// a Bash write's command text, an `agentic-capture` draft and the files its
// header attaches, and the report or params an sdd engine call carries. An
// entry is immutable and every session, worktree and seat reads the graph, so
// a credential written to it can never be taken back: the scan runs whichever
// sdd is installed, and before any read or seat exemption is weighed. A scan
// that could not finish refuses too.
func leaksCredential(ctx context.Context, env Env, payload hookio.Payload) string {
	switch payload.ToolName {
	case "Bash":
		return commandLeaks(ctx, env, payload.ToolInput.Command)
	case mcpNext:
		return engineLeaks(ctx, payload, "report")
	case mcpStartProcedure:
		return engineLeaks(ctx, payload, "params")
	}

	return ""
}

func commandLeaks(ctx context.Context, env Env, command string) string {
	write := writeIn(command)
	if write == writesNothing {
		return ""
	}

	found := credentialIn(ctx, command, "", "the command")
	if found != "" || write != writesThroughCapture {
		return found
	}

	for _, named := range draftPaths(env, command) {
		content, err := os.ReadFile(named.path)
		if err != nil {
			continue
		}

		found = credentialIn(ctx, string(content), "", named.name)
		if found != "" {
			return found
		}

		for _, attached := range attachments(env, content) {
			found = credentialIn(ctx, attached.content, attached.name, attached.name)
			if found != "" {
				return found
			}
		}
	}

	return ""
}

// engineLeaks scans the field of an engine call that carries what lands in
// the graph. Its shape is the procedure's, so every key and string in it is
// read as opaque text, one per line in key order; the JSON spelling itself is
// not scanned, since its escapes (`\"`, `\n`) hide a token from the rules.
func engineLeaks(ctx context.Context, payload hookio.Payload, field string) string {
	var fields map[string]json.RawMessage

	err := json.Unmarshal(payload.ToolInput.Raw, &fields)
	if err != nil {
		return ""
	}

	var carried any

	err = json.Unmarshal(fields[field], &carried)
	if err != nil {
		return ""
	}

	text := strings.Join(texts(carried), "\n")

	return credentialIn(ctx, text, "", fmt.Sprintf("the %s %s", payload.ToolName, field))
}

func texts(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []any:
		var found []string
		for _, item := range typed {
			found = append(found, texts(item)...)
		}

		return found
	case map[string]any:
		var found []string
		for _, key := range slices.Sorted(maps.Keys(typed)) {
			found = append(found, key)
			found = append(found, texts(typed[key])...)
		}

		return found
	}

	return nil
}

func credentialIn(ctx context.Context, text string, name string, where string) string {
	findings := secrets.Scan(ctx, text, name)
	if len(findings) == 0 {
		return ""
	}

	rules, line := secrets.Describe(findings)

	return refusal("credential.txt", "{rules}", rules, "{where}", where, "{line}", strconv.Itoa(line))
}

type attachment struct {
	name    string
	content string
}

// attachments reads the files a draft's header names under `attach`, as
// agentic-capture hands them to `sdd new --attach`. A file it cannot read is
// left for the capture to refuse.
func attachments(env Env, draft []byte) []attachment {
	rest, opens := strings.CutPrefix(string(draft), "---\n")

	header, _, closes := strings.Cut(rest, "\n---\n")
	if !opens || !closes {
		return nil
	}

	var front struct {
		Attach any `yaml:"attach"`
	}

	err := yaml.Unmarshal([]byte(header), &front)
	if err != nil {
		return nil
	}

	var named []string

	switch value := front.Attach.(type) {
	case string:
		named = []string{value}
	case []any:
		for _, item := range value {
			named = append(named, fmt.Sprint(item))
		}
	}

	var read []attachment

	for _, name := range named {
		path := name
		if !filepath.IsAbs(path) {
			path = filepath.Join(env.WorkingDir, path)
		}

		content, err := os.ReadFile(path)
		if err == nil {
			read = append(read, attachment{name: name, content: string(content)})
		}
	}

	return read
}
