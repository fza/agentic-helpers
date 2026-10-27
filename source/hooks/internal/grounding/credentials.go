package grounding

import (
	"cmp"
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

// scan is what a graph write's scan found: the first credential, whether an
// ended context cut a scan short, and the first file it could not scan
// (standard input, a file it cannot read, a capture naming no draft). Scanning
// goes on past an unscannable file, so a credential elsewhere still refuses
// first.
type scan struct {
	leaked      string
	interrupted string
	unscanned   string
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
	held.report(ctx, secrets.Scan(ctx, text, name), where)
}

// file scans a file by its name, so path rules fire on it, and where that finds
// nothing, again as text that is no file: gitleaks' global allowlist skips
// whole files by name (`.bin`, images, fonts, office files), and a credential
// attached under such a name would otherwise go unscanned. A scan by a name it
// does not skip finds all the nameless scan would.
func (held *scan) file(ctx context.Context, content string, name string, where string) {
	found := secrets.Scan(ctx, content, name)
	if len(found) == 0 {
		found = secrets.Scan(ctx, content, "")
	}

	held.report(ctx, found, where)
}

// report refuses on a finding. Once the context has ended a finding is no
// credential: a scan it cut short reports `scan-incomplete`, and the hook
// itself is being cut short, so it refuses as an unfinished scan.
func (held *scan) report(ctx context.Context, findings []secrets.Finding, where string) {
	if len(findings) == 0 {
		return
	}

	if ctx.Err() != nil {
		if held.interrupted == "" {
			held.interrupted = refusal("credential-interrupted.txt")
		}

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
// sdd is installed. It returns the refusal ahead of every other check (a
// credential, else a scan an ended context cut short), and apart from it the
// refusal for what it could not scan, since the gate fails closed. A scan that
// could not finish for any other reason refuses as a credential.
// agentic-capture scans the bytes it sends again right before sdd runs, since
// a file may change after this check.
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

	return cmp.Or(held.leaked, held.interrupted), held.unscanned
}

func (held *scan) command(ctx context.Context, env Env, command string) {
	if writeIn(command) == writesNothing {
		return
	}

	held.text(ctx, command, "", "the command")

	heard := spokenIn(command, false)
	moved := false

	for _, stretch := range heard {
		moved = moved || slices.ContainsFunc(stretch.calls, call.changesDirectory)
	}

	for _, stretch := range heard {
		for _, called := range stretch.calls {
			switch {
			case callsCapture(called):
				held.patterned(called)
				held.capture(ctx, env, command, called.words[1:], unanchored(called, moved))
			case callsNew(called):
				held.patterned(called)
				held.attached(ctx, attachValues(called.words[2:]), env.WorkingDir, unanchored(called, moved))
			}
		}
	}
}

// patterned refuses a write call holding a word the shell may expand into
// others, a glob or a brace, in any argument: the words the gate reads are
// spelled, not expanded, so `--att{ach,x} f` or `--attac?=f` may reach sdd as
// an `--attach` the gate never saw, and `run*.log` as files it never read.
func (held *scan) patterned(called call) {
	for index, patterned := range called.patterned[1:] {
		if patterned {
			held.cannot(refusal("credential-unresolved.txt", "{where}", called.words[index+1], "{why}", "it holds a shell glob or brace"))

			return
		}
	}
}

// unanchored says why a relative attachment of the call cannot be placed, or
// nothing where it can: a launcher in front of the call may run it elsewhere
// or change what it reads, and a directory change anywhere in the command may
// move the shell first. The gate refuses rather than follows either.
func unanchored(called call, moved bool) string {
	switch {
	case called.launched:
		return "it is relative behind a launcher"
	case moved:
		return "it is relative after a directory change"
	}

	return ""
}

// capture scans the draft an `agentic-capture` call names and the files its
// header attaches. A draft handed in some other way, as through `xargs`, is
// looked for among the command's `.md` names.
func (held *scan) capture(ctx context.Context, env Env, command string, args []string, unplaced string) {
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

		held.attached(ctx, values, filepath.Dir(governing), unplaced)
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

// attached scans every file the `--attach` values name, reading a relative
// one from base. Only a literal path is read: one holding `$` or `~` is
// whatever the shell expands it to (a glob or brace refuses the whole call
// first), and a relative one is refused where unplaced says why it cannot be
// placed. Standard input, and a file that cannot be read, cannot be scanned
// here either.
func (held *scan) attached(ctx context.Context, values []string, base string, unplaced string) {
	for _, value := range values {
		attached := sddcaps.Attachment(value)

		switch {
		case attached.Stdin:
			held.cannot(refusal("credential-stdin.txt", "{name}", attached.Name))

			continue
		case expands(attached.Path):
			held.cannot(refusal("credential-unresolved.txt", "{where}", attached.Path, "{why}", "it holds a shell expansion"))

			continue
		case !filepath.IsAbs(attached.Path) && unplaced != "":
			held.cannot(refusal("credential-unresolved.txt", "{where}", attached.Path, "{why}", unplaced))

			continue
		}

		path := attached.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(base, path)
		}

		content, err := os.ReadFile(path)
		if err != nil {
			held.cannot(refusal("credential-unreadable.txt", "{where}", attached.Path))

			continue
		}

		held.file(ctx, string(content), attached.Name, attached.Path)
	}
}

func expands(path string) bool {
	return strings.ContainsAny(path, "$~")
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
	input, decoded := held.decode(payload)
	if !decoded {
		return
	}

	carried, found := input[field]
	if !found {
		return
	}

	held.text(ctx, strings.Join(texts(carried), "\n"), "", fmt.Sprintf("the %s %s", payload.ToolName, field))
}

// decode reads an engine call's input. Input it cannot read cannot be scanned,
// so it is refused rather than let through unscanned.
func (held *scan) decode(payload hookio.Payload) (map[string]any, bool) {
	var input map[string]any

	err := json.Unmarshal(payload.ToolInput.Raw, &input)
	if err != nil {
		held.cannot(refusal("credential-undecodable.txt", "{tool}", payload.ToolName))

		return nil, false
	}

	return input, true
}

// staged scans a file staged for an engine entry by its name: every key and
// string of the call, its inline content and patches among them, and the
// local file its `path` names. A patch is scanned as the text it writes, not
// as the file it leaves: the staged file itself is the engine's state.
func (held *scan) staged(ctx context.Context, env Env, payload hookio.Payload) {
	input, decoded := held.decode(payload)
	if !decoded {
		return
	}

	name, _ := input["name"].(string)

	held.file(ctx, strings.Join(texts(input), "\n"), name, "the staged "+name)

	path, _ := input["path"].(string)
	if path == "" {
		return
	}

	if expands(path) {
		held.cannot(refusal("credential-unresolved.txt", "{where}", path, "{why}", "it holds a shell expansion"))

		return
	}

	_, content, read := readNamed(env, path)
	if !read {
		held.cannot(refusal("credential-unreadable.txt", "{where}", path))

		return
	}

	held.file(ctx, string(content), name, path)
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
