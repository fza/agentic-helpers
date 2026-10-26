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

	"github.com/fza/agentic-helpers/source/hooks/internal/graphconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/sddcaps"
	"github.com/fza/agentic-helpers/source/hooks/internal/secrets"
)

const mcpStageAttachment = "mcp__sdd__stage_attachment"

// scan is what a graph write's scan found: the first credential, and the
// first file it could not scan (standard input, a file it cannot read, a
// capture naming no draft). Scanning goes on past an unscannable file, so a
// credential elsewhere still refuses first.
type scan struct {
	leaked    string
	unscanned string
}

func (held *scan) found(refused string) {
	if held.leaked == "" {
		held.leaked = refused
	}
}

func (held *scan) cannot(refused string) {
	if held.unscanned == "" {
		held.unscanned = refused
	}
}

func (held *scan) text(ctx context.Context, text string, name string, where string) {
	findings := secrets.Scan(ctx, text, name)
	if len(findings) == 0 {
		return
	}

	rules, line := secrets.Describe(findings)
	held.found(refusal("credential.txt", "{rules}", rules, "{where}", where, "{line}", strconv.Itoa(line)))
}

// leaksCredential scans a graph write for what looks like a credential: a
// Bash write's command text, the files a plain `sdd new` attaches, an
// `agentic-capture` draft and the files its header attaches, the report or
// params an sdd engine call carries, and a file staged for the engine. An
// entry is immutable and every session, worktree and seat reads the graph, so
// a credential written to it can never be taken back: the scan runs whichever
// sdd is installed. It returns the credential refusal, and apart from it the
// refusal for what it could not scan, since the gate fails closed. A scan that
// could not finish refuses as a credential. agentic-capture scans the bytes it
// sends again right before sdd runs, since a file may change after this check.
func leaksCredential(ctx context.Context, env Env, payload hookio.Payload) (string, string) {
	var held scan

	switch payload.ToolName {
	case "Bash":
		held.command(ctx, env, payload.ToolInput.Command)
	case mcpNext:
		held.engine(ctx, payload, "report")
	case mcpStartProcedure:
		held.engine(ctx, payload, "params")
	case mcpStageAttachment:
		held.staged(ctx, env, payload)
	}

	return held.leaked, held.unscanned
}

func (held *scan) command(ctx context.Context, env Env, command string) {
	if writeIn(command) == writesNothing {
		return
	}

	held.text(ctx, command, "", "the command")

	for _, stretch := range spokenIn(command, false) {
		for _, called := range stretch.calls {
			switch {
			case callsCapture(called):
				held.capture(ctx, env, command, called.words[1:])
			case callsNew(called):
				held.attached(ctx, env, attachValues(called.words[2:]), env.WorkingDir)
			}
		}
	}
}

// capture scans the draft an `agentic-capture` call names and the files its
// header attaches. A draft handed in some other way, as through `xargs`, is
// looked for among the command's `.md` names.
func (held *scan) capture(ctx context.Context, env Env, command string, args []string) {
	var names []string

	draft := draftArgument(args)
	if draft != "" {
		names = append(names, draft)
	} else {
		for _, named := range draftPaths(env, command) {
			if !slices.Contains(names, named.name) {
				names = append(names, named.name)
			}
		}
	}

	if len(names) == 0 {
		held.cannot(refusal("credential-no-draft.txt"))
	}

	for _, name := range names {
		path, content, read := readNamed(env, name)
		if !read {
			held.cannot(refusal("credential-unreadable.txt", "{where}", name))

			continue
		}

		held.text(ctx, string(content), "", name)

		values, err := headerAttachments(content)
		if err != nil {
			held.cannot(refusal("credential-unreadable.txt", "{where}", name+"'s header"))

			continue
		}

		// sdd runs where the governing `.sdd/` sits, as agentic-capture runs it,
		// and reads a relative attachment from there.
		governing := graphconfig.GraphDir(filepath.Dir(path))
		if governing == "" {
			governing = graphconfig.GraphDir(env.WorkingDir)
		}

		held.attached(ctx, env, values, filepath.Dir(governing))
	}
}

// draftArgument is the draft an `agentic-capture` call names, read as its own
// argument parsing reads it: the first word that is no flag or flag value.
func draftArgument(args []string) string {
	for index := 0; index < len(args); index++ {
		switch arg := args[index]; {
		case arg == "--preflight-done" || arg == "--preflight-limit" || arg == "--preflight-route":
			index++
		case strings.HasPrefix(arg, "-"):
		default:
			return arg
		}
	}

	return ""
}

// readNamed reads a file a command names, `~` and variables expanded as the
// shell would, relative to the working directory or else to the project, the
// bases a draft is looked for in.
func readNamed(env Env, name string) (string, []byte, bool) {
	name = expand(env, name)

	bases := []string{env.WorkingDir, env.project()}
	if filepath.IsAbs(name) {
		bases = []string{""}
	}

	for _, base := range bases {
		path := filepath.Join(base, name)

		content, err := os.ReadFile(path)
		if err == nil {
			return path, content, true
		}
	}

	return "", nil, false
}

// attachValues lists the `--attach` values of a plain `sdd new`, in both the
// `--attach value` and `--attach=value` forms.
func attachValues(args []string) []string {
	var values []string

	for index := 0; index < len(args); index++ {
		value, inline := strings.CutPrefix(args[index], "--attach=")

		switch {
		case inline:
			values = append(values, value)
		case args[index] == "--attach" && index+1 < len(args):
			index++
			values = append(values, args[index])
		}
	}

	return values
}

// attached scans every file the `--attach` values name, by its name in the
// entry, reading a relative one from base. Standard input, and a file that
// cannot be read, cannot be scanned here.
func (held *scan) attached(ctx context.Context, env Env, values []string, base string) {
	for _, value := range values {
		attached := sddcaps.Attachment(value)
		if attached.Stdin {
			held.cannot(refusal("credential-stdin.txt", "{name}", attached.Name))

			continue
		}

		path := expand(env, attached.Path)
		if !filepath.IsAbs(path) {
			path = filepath.Join(base, path)
		}

		content, err := os.ReadFile(path)
		if err != nil {
			held.cannot(refusal("credential-unreadable.txt", "{where}", attached.Path))

			continue
		}

		held.text(ctx, string(content), attached.Name, attached.Path)
	}
}

// headerAttachments lists the `attach` values of a draft's header, as
// agentic-capture hands them to `sdd new --attach`. A draft carrying no header
// attaches nothing; a header that does not parse is an error, since what it
// attaches is then unknown.
func headerAttachments(draft []byte) ([]string, error) {
	rest, opens := strings.CutPrefix(string(draft), "---\n")

	header, _, closes := strings.Cut(rest, "\n---\n")
	if !opens || !closes {
		return nil, nil
	}

	var front struct {
		Attach any `yaml:"attach"`
	}

	err := yaml.Unmarshal([]byte(header), &front)
	if err != nil {
		return nil, fmt.Errorf("reading the draft's header: %w", err)
	}

	switch value := front.Attach.(type) {
	case string:
		return []string{value}, nil
	case []any:
		var named []string
		for _, item := range value {
			named = append(named, fmt.Sprint(item))
		}

		return named, nil
	}

	return nil, nil
}

// engine scans the field of an engine call that carries what lands in the
// graph. Its shape is the procedure's, so every key and string in it is read
// as opaque text, one per line in key order; the JSON spelling itself is not
// scanned, since its escapes (`\"`, `\n`) hide a token from the rules. The
// session `label`, and every other field naming the dialogue rather than the
// entry, stays out of the graph and so out of the scan.
func (held *scan) engine(ctx context.Context, payload hookio.Payload, field string) {
	var fields map[string]json.RawMessage

	err := json.Unmarshal(payload.ToolInput.Raw, &fields)
	if err != nil {
		return
	}

	var carried any

	err = json.Unmarshal(fields[field], &carried)
	if err != nil {
		return
	}

	held.text(ctx, strings.Join(texts(carried), "\n"), "", fmt.Sprintf("the %s %s", payload.ToolName, field))
}

type stagedFile struct {
	Name    string `json:"name"`
	Content string `json:"content"`
	Path    string `json:"path"`
	Patches []struct {
		New string `json:"new"`
	} `json:"patches"`
}

// staged scans a file staged for an engine entry, by its name: its inline
// content, the text each patch writes, and the local file it names.
func (held *scan) staged(ctx context.Context, env Env, payload hookio.Payload) {
	var input stagedFile

	err := json.Unmarshal(payload.ToolInput.Raw, &input)
	if err != nil {
		return
	}

	where := "the staged " + input.Name

	held.text(ctx, input.Content, input.Name, where)

	for _, patch := range input.Patches {
		held.text(ctx, patch.New, input.Name, where)
	}

	if input.Path == "" {
		return
	}

	_, content, read := readNamed(env, input.Path)
	if !read {
		held.cannot(refusal("credential-unreadable.txt", "{where}", input.Path))

		return
	}

	held.text(ctx, string(content), input.Name, input.Path)
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
