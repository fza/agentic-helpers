// Package grounding refuses a question or a draft edit until the turn behind
// it has read the decision graph: a literal search, a semantic search, and a
// listing.
//
// One search mode alone misses what the other finds, and an empty search reads
// as absence. A listing cannot miss that way, because it enumerates rather
// than matching words. A project naming a listing prefix in
// `.sdd/grounding.yaml` is held to a listing of one topic carrying it; every
// other project to a view naming every topic. `sdd show` alone never satisfies the gate: following references
// reaches only entries something already cited.
//
// It also refuses a capture whose draft records a gap, a read discarding a
// stream (a wrong flag prints usage to stderr, and discarding it turns a
// refusal into an empty result), and a show stopping short of `--down 2 --up 1`.
//
// Modes:
//
//	record  PostToolUse on Bash: notes which graph reads a command performed
//	turn    UserPromptSubmit: starts a fresh turn, so evidence never carries over
//	gate    PreToolUse: refuses a bad read, and refuses AskUserQuestion and a
//	        draft edit until this turn carries every read
package grounding

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/graphconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
)

//go:embed refusals
var refusals embed.FS

var ErrUsage = errors.New("usage: agentic-grounding-gate record|turn|gate")

const (
	graphDirName = ".sdd"

	// A hook exit code Claude Code reads as a refusal, handing stderr to the
	// agent.
	refused = 2
)

var (
	everyTopic  = regexp.MustCompile(`(?:sdd|graph\.py)\s+view\b[^\n]*\bactive:as-counts\b`)
	search      = regexp.MustCompile(`(?:sdd|graph\.py)\s+search\b`)
	view        = regexp.MustCompile(`(?:sdd|graph\.py)\s+view\b`)
	semantic    = regexp.MustCompile(`(?:sdd|graph\.py)\s+search\b[^\n]*--query\b`)
	literal     = regexp.MustCompile(`(?:sdd|graph\.py)\s+search\b[^\n]*--term\b`)
	draftPath   = regexp.MustCompile(`[/\\]drafts?[/\\]`)
	toNull      = regexp.MustCompile(`(?:&>>?|(?:[12]?>>?)(?:&\s*[12])?)\s*\|?\s*/dev/null|>\s*&\s*/dev/null`)
	show        = regexp.MustCompile(`(?:sdd|graph\.py)\s+show\b[^\n|;&]*`)
	downDepth   = regexp.MustCompile(`--down[=\s]+(\d+)\b`)
	upDepth     = regexp.MustCompile(`--up[=\s]+(\d+)\b`)
	entryID     = regexp.MustCompile(`\b\d{8}-\d{6}-[sd]-([a-z]{3})-[a-z0-9]{3}\b`)
	subjectFlag = regexp.MustCompile(`--entry[=\s]+\S+`)
	capture     = regexp.MustCompile(`\bagentic-capture\b`)
	draftName   = regexp.MustCompile(`[\w./-]+\.md\b`)
	gapKind     = regexp.MustCompile(`(?m)^kind:\s*gap\s*$`)
	enters      = regexp.MustCompile(`\bcd\s+(?:'([^']+)'|"([^"]+)"|([^\s;&|]+))`)
)

var gatedTools = map[string]bool{"AskUserQuestion": true, "Write": true, "Edit": true, "NotebookEdit": true}

// Env is where a hook invocation runs. ProjectDir is empty when the client set
// none.
type Env struct {
	ProjectDir string
	WorkingDir string
	Home       string
}

func (env Env) project() string {
	if env.ProjectDir != "" {
		return env.ProjectDir
	}

	return env.WorkingDir
}

// ledgerDir keeps each session's evidence under the graph's own scratch
// directory, which sdd ignores.
func (env Env) ledgerDir() string {
	return filepath.Join(env.graphDir(), "tmp", "grounding-gate")
}

func (env Env) graphDir() string {
	return graphconfig.GraphDir(env.project())
}

// Main runs one hook invocation. A project carrying no graph gets no answer
// rather than a refusal it cannot act on.
func Main(ctx context.Context, args []string, env Env, streams hookio.Streams) int {
	if len(args) != 1 || (args[0] != "record" && args[0] != "turn" && args[0] != "gate") {
		_, _ = fmt.Fprintln(streams.Err, ErrUsage)

		return 1
	}

	if env.graphDir() == "" {
		return 0
	}

	// A payload that is no object carries nothing to read, and a non-zero exit
	// on a prompt blocks the prompt itself.
	payload, err := hookio.ReadPayload(streams.In)
	if err != nil {
		payload = hookio.Payload{}
	}

	switch args[0] {
	case "record":
		record(ctx, env, payload)

		return 0
	case "turn":
		turn(ctx, env, payload)

		return 0
	default:
		refusal := gate(ctx, env, payload)
		if refusal == "" {
			return 0
		}

		_, _ = fmt.Fprint(streams.Err, refusal)

		return refused
	}
}

// record credits the reads a command performed. Two readings of one command:
// the shell-stripped form says whether a call happened, because crediting a
// quoted mention lets an `echo` satisfy the gate; the raw form says what the
// call asked for, because the layout and search mode live inside quotes.
func record(ctx context.Context, env Env, payload hookio.Payload) {
	command := payload.ToolInput.Command
	if command == "" || payload.Failed() {
		return
	}

	spoken := shellOnly(command)

	path, err := ledgerPath(env.ledgerDir(), payload)
	if err != nil {
		return
	}

	held := loadEvidence(ctx, path)
	changed := false

	if search.MatchString(spoken) {
		held.Search++
		changed = true

		if semantic.MatchString(command) {
			held.Query++
		}

		if literal.MatchString(command) {
			held.Term++
		}
	}

	prefix, err := env.listingPrefix()
	if err != nil {
		return
	}

	if view.MatchString(spoken) {
		for _, listing := range listingsIn(prefix, command) {
			held.addListing(listing)
			changed = true
		}
	}

	if changed {
		// A ledger that cannot be written holds this turn's reads nowhere, and
		// the gate then refuses: the safe side.
		_ = saveEvidence(ctx, path, held)
	}
}

// turn starts fresh. A turn that failed to clear would leave the last one's
// evidence standing, so a failure removes the file rather than leaving it.
func turn(ctx context.Context, env Env, payload hookio.Payload) {
	path, err := ledgerPath(env.ledgerDir(), payload)
	if err != nil {
		return
	}

	err = saveEvidence(ctx, path, freshEvidence())
	if err != nil {
		_ = os.Remove(path)
	}
}

// gate returns the refusal this call earns, or nothing.
func gate(ctx context.Context, env Env, payload hookio.Payload) string {
	if payload.ToolName == "Bash" {
		return readsBadly(env, payload.ToolInput.Command)
	}

	if !gated(payload) {
		return ""
	}

	prefix, err := env.listingPrefix()
	if err != nil {
		return fmt.Sprintf("GROUNDING GATE: refused. %v\n\nFix `.sdd/grounding.yaml`, then ask again.\n", err)
	}

	path, err := ledgerPath(env.ledgerDir(), payload)

	held := freshEvidence()
	if err == nil {
		held = loadEvidence(ctx, path)
	}

	haveSemantic, haveLiteral, haveListing := held.Query > 0, held.Term > 0, len(held.Listings) > 0
	if haveSemantic && haveLiteral && haveListing {
		return ""
	}

	var have, missing []string

	if haveSemantic {
		have = append(have, "a semantic query")
	} else {
		missing = append(missing, "No `--query` search ran this turn.")
	}

	if haveLiteral {
		have = append(have, "a literal term search")
	} else {
		missing = append(missing, "No `--term` search ran this turn.")
	}

	switch {
	case haveListing:
		have = append(have, "a listing of "+strings.Join(held.Listings, ", "))
	case prefix != "":
		missing = append(missing, "No `"+prefix+"` listing ran this turn.")
	default:
		missing = append(missing, "No listing of every topic ran this turn.")
	}

	carries := strings.Join(have, " and ")
	if carries == "" {
		carries = "neither read"
	}

	return refusal("reads.txt", "{listing}", listingCommand(prefix), "{have}", carries,
		"{missing}", strings.Join(missing, " "))
}

func gated(payload hookio.Payload) bool {
	if payload.ToolName == "AskUserQuestion" {
		return true
	}

	if !gatedTools[payload.ToolName] {
		return false
	}

	target := payload.ToolInput.FilePath

	return draftPath.MatchString(target)
}

func readsBadly(env Env, raw string) string {
	if elsewhere(env, raw) {
		return ""
	}

	// A heredoc body is data whatever its shape, so it is dropped before the
	// payload scan as well as before the call scan.
	written := stripHeredocs(raw)
	spoken := shellOnly(raw)

	texts := []string{spoken}
	for _, inner := range carried(written, 0) {
		texts = append(texts, shellOnly(inner))
	}

	for _, check := range []func() string{
		func() string { return capturesAGap(env, spoken) },
		func() string { return discardsAStream(texts, raw) },
		func() string { return hidesTheDownstream(texts) },
	} {
		found := check()
		if found != "" {
			return found
		}
	}

	return ""
}

// capturesAGap refuses a capture whose draft records a problem rather than an
// answer. The graph is append-only, so a gap captured by mistake stays.
func capturesAGap(env Env, command string) string {
	if !capture.MatchString(command) {
		return ""
	}

	draft := gapDraft(env, command)
	if draft == "" {
		return ""
	}

	return refusal("gap.txt", "{draft}", draft)
}

// gapDraft names the first draft in the command recording a gap. A relative
// name resolves against the working directory and the project root only: every
// other checkout is somebody else's.
func gapDraft(env Env, command string) string {
	project := env.ProjectDir
	if project == "" {
		project = "."
	}

	bases := []string{env.WorkingDir}
	if project != env.WorkingDir {
		bases = append(bases, project)
	}

	for _, candidate := range draftName.FindAllString(command, -1) {
		for _, base := range bases {
			path := candidate
			if !filepath.IsAbs(path) {
				path = filepath.Join(base, candidate)
			}

			if gapKind.Match(head(path)) {
				return candidate
			}
		}
	}

	return ""
}

func head(path string) []byte {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}

	defer func() { _ = file.Close() }()

	buffer := make([]byte, 4096)
	read, _ := file.Read(buffer)

	return buffer[:read]
}

func discardsAStream(texts []string, raw string) string {
	for _, text := range texts {
		if reachesTool(text) && toNull.MatchString(text) {
			return refusal("stream.txt", "{command}", strings.TrimSpace(raw))
		}
	}

	return ""
}

func hidesTheDownstream(texts []string) string {
	for _, command := range texts {
		missing := readsTooShallow(command)
		if missing == "" {
			continue
		}

		// The subject a read is recorded against is not what it reads, so its
		// layer never decides whether the read may stop short.
		read := subjectFlag.ReplaceAllString(command, " ")

		layers := map[string]bool{}
		for _, match := range entryID.FindAllStringSubmatch(read, -1) {
			layers[match[1]] = true
		}

		// A process-layer rules entry's downstream chain is every entry ever
		// captured under it, and reading that settles nothing.
		if len(layers) == 1 && layers["prc"] {
			continue
		}

		return refusal("downstream.txt", "{command}", strings.TrimSpace(command), "{missing}", missing)
	}

	return ""
}

// readsTooShallow says what a show falls short of, in the words the refusal
// needs. A body is immutable, so a surface it names keeps that spelling after a
// later entry renamed it: the rename lives downstream. `--up` shows what the
// entry answered, which says whether it still applies.
func readsTooShallow(command string) string {
	found := show.FindString(command)
	if found == "" {
		return ""
	}

	var short []string

	for _, reach := range []struct {
		flag    string
		pattern *regexp.Regexp
		wanted  int
	}{
		{flag: "down", pattern: downDepth, wanted: 2},
		{flag: "up", pattern: upDepth, wanted: 1},
	} {
		given := reach.pattern.FindStringSubmatch(found)
		if given == nil {
			short = append(short, "no `--"+reach.flag+"`")

			continue
		}

		reached, err := strconv.Atoi(given[1])
		if err != nil || reached < reach.wanted {
			short = append(short, fmt.Sprintf("`--%s %d`", reach.flag, reached))
		}
	}

	if len(short) == 0 {
		return ""
	}

	return strings.Join(short, " and ") + " reads short of `--down 2 --up 1`."
}

// elsewhere reports whether the command starts by walking into another
// repository. The rules here derive from this project's graph, and a session
// working on a different tree answers to that tree.
func elsewhere(env Env, command string) bool {
	here := graphconfig.RealPath(env.project())

	for _, match := range enters.FindAllStringSubmatch(command, -1) {
		target := match[1] + match[2] + match[3]
		target = expand(env, target)

		if !filepath.IsAbs(target) {
			continue
		}

		walked := graphconfig.RealPath(target)
		if walked == here || strings.HasPrefix(walked, here+string(filepath.Separator)) {
			continue
		}

		// A repository of its own, rather than a path that merely reads as one.
		for _, mark := range []string{".git", graphDirName} {
			info, err := os.Stat(filepath.Join(walked, mark))
			if err == nil && info.IsDir() {
				return true
			}
		}
	}

	return false
}

// expand resolves `~` and environment variables the way the shell would,
// leaving an unset variable as written.
func expand(env Env, path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		path = env.Home + path[1:]
	}

	return os.Expand(path, func(name string) string {
		value, set := os.LookupEnv(name)
		if !set {
			return "$" + name
		}

		return value
	})
}

func refusal(name string, replacements ...string) string {
	text, err := refusals.ReadFile("refusals/" + name)
	if err != nil {
		return fmt.Sprintf("GROUNDING GATE: refused, and the refusal text %s is missing: %v\n", name, err)
	}

	return strings.NewReplacer(replacements...).Replace(string(text))
}

// listingPrefix is the topic prefix this project's listing has to carry, or
// empty where the project names none.
func (env Env) listingPrefix() (string, error) {
	config, err := graphconfig.Load(env.graphDir())
	if err != nil {
		return "", fmt.Errorf("the project's graph config: %w", err)
	}

	return config.ListingPrefix, nil
}

// listingsIn names every listing a view command performed: each topic carrying
// the prefix, or, with no prefix, the view of every topic.
func listingsIn(prefix string, command string) []string {
	if prefix == "" {
		if everyTopic.MatchString(command) {
			return []string{"every topic"}
		}

		return nil
	}

	pattern := regexp.MustCompile(`(?:sdd|graph\.py)\s+view\b[^\n]*topic\(\s*"?` + regexp.QuoteMeta(prefix))

	var found []string

	for _, match := range pattern.FindAllStringIndex(command, -1) {
		name, _, _ := strings.Cut(command[match[1]:], ")")
		found = append(found, prefix+strings.Trim(strings.TrimSpace(name), `"`))
	}

	return found
}

func listingCommand(prefix string) string {
	if prefix == "" {
		return `view --layout "active:as-counts"`
	}

	return `view --layout "topic(` + prefix + `<name>):as-list"`
}
