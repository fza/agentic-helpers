// Package grounding refuses a question, a draft edit or a capture until the
// turn behind it has read the decision graph: a literal search, a semantic
// search, and a listing.
//
// One search mode alone misses what the other finds, and an empty search reads
// as absence. A listing cannot miss that way, because it enumerates rather
// than matching words. A project naming a listing prefix in
// `.sdd/grounding.yaml` is held to a listing of one topic carrying it; every
// other project to a view naming every topic. `sdd show` alone never satisfies the gate: following references
// reaches only entries something already cited.
//
// A capture also passes on a grounded draft: one a `Write` or `Edit` saved, as
// it stands now, in a turn carrying every read, with the graph holding the same
// entries since. The turn that confirms a playback then need not repeat the
// reads of the turn that wrote the draft, and a draft written through `Bash`,
// or captured after the graph moved, still owes them.
//
// It also refuses a read discarding a stream (a wrong flag prints usage to
// stderr, and discarding it turns a refusal into an empty result), and a show
// stopping short of the depth `.sdd/grounding.yaml` sets, `--down 2 --up 1`
// where it sets none. A seat the file exempts owes no depth; it learns which
// seat a session holds from the carry-forward's claims.
//
// Modes:
//
//	record  PostToolUse on Bash: notes which graph reads a command performed;
//	        on Write and Edit: notes a draft saved in a fully read turn
//	turn    UserPromptSubmit: starts a fresh turn, so reads never carry over
//	gate    PreToolUse: refuses a bad read, refuses AskUserQuestion and a draft
//	        edit until this turn carries every read, and a capture until this
//	        turn carries every read or its draft is grounded
//	start   SessionStart: removes ledgers of sessions gone a week
//	end     SessionEnd: removes this session's ledgers
package grounding

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/graphconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/project"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
)

//go:embed refusals
var refusals embed.FS

var ErrUsage = errors.New("usage: agentic-grounding-gate record|turn|gate|start|end")

const (
	graphDirName = ".sdd"

	// A hook exit code Claude Code reads as a refusal, handing stderr to the
	// agent.
	refused = 2

	// Long enough that a session resumed the next morning still finds its
	// grounded drafts, and the one `agentic-scratch` keeps a scratch for.
	orphanAge = 7 * 24 * time.Hour
)

var (
	draftPath   = regexp.MustCompile(`[/\\]drafts?[/\\]`)
	toNull      = regexp.MustCompile(`(?:&>>?|(?:[12]?>>?)(?:&\s*[12])?)\s*\|?\s*/dev/null|>\s*&\s*/dev/null`)
	downDepth   = regexp.MustCompile(`--down[=\s]+(\d+)\b`)
	upDepth     = regexp.MustCompile(`--up[=\s]+(\d+)\b`)
	entryID     = regexp.MustCompile(`\b\d{8}-\d{6}-[sd]-([a-z]{3})-[a-z0-9]{3}\b`)
	subjectFlag = regexp.MustCompile(`--entry[=\s]+\S+`)
	draftName   = regexp.MustCompile(`[\w./-]+\.md\b`)
	enters      = regexp.MustCompile(`\bcd\s+(?:'([^']+)'|"([^"]+)"|([^\s;&|]+))`)
)

// Matching this shape is the whole safety of the sweep: only a ledger named for
// a session, or for an agent within one, is ever removed.
var ledgerName = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}(?:\.[^/]+)?\.json$`)

var gatedTools = map[string]bool{"AskUserQuestion": true, "Write": true, "Edit": true, "NotebookEdit": true}

var savingTools = map[string]bool{"Write": true, "Edit": true}

// Env is where a hook invocation runs. ProjectDir is empty when the client set
// none; Checkout finds the main checkout, where the seat claims live.
type Env struct {
	ProjectDir string
	WorkingDir string
	Home       string
	Checkout   project.CommonDirLookup
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
	if len(args) != 1 || !slices.Contains([]string{"record", "turn", "gate", "start", "end"}, args[0]) {
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
	case "start":
		reap(ctx, env.ledgerDir(), time.Now())

		return 0
	case "end":
		end(ctx, env.ledgerDir(), payload)

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
	if savingTools[payload.ToolName] {
		recordDraft(ctx, env, payload)

		return
	}

	command := payload.ToolInput.Command
	if command == "" || payload.Failed() {
		return
	}

	spoken := shellOnly(command)

	path, err := ledgerPath(env.ledgerDir(), payload)
	if err != nil {
		return
	}

	// A config that does not parse credits nothing: the gate refuses on it
	// anyway, and which command reads the graph is part of what it says.
	config, err := env.config()
	if err != nil {
		return
	}

	reads := readsOf(config)

	held := loadEvidence(ctx, path)
	changed := false

	if reads.search.MatchString(spoken) {
		held.Search++
		changed = true

		if reads.semantic.MatchString(command) {
			held.Query++
		}

		if reads.literal.MatchString(command) {
			held.Term++
		}
	}

	if reads.view.MatchString(spoken) {
		for _, listing := range reads.listingsIn(config.ListingPrefix, command) {
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

// recordDraft grounds a draft saved in a turn carrying every read. The hash is
// of the file as saved, since an `Edit` names only the strings it swapped.
func recordDraft(ctx context.Context, env Env, payload hookio.Payload) {
	target := payload.ToolInput.FilePath
	if !draftPath.MatchString(target) || payload.Failed() {
		return
	}

	path, err := ledgerPath(env.ledgerDir(), payload)
	if err != nil {
		return
	}

	held := loadEvidence(ctx, path)
	if !held.complete() {
		return
	}

	if !filepath.IsAbs(target) {
		target = filepath.Join(env.WorkingDir, target)
	}

	draft, err := env.grounding(target)
	if err != nil {
		return
	}

	held.addGrounded(draft)
	_ = saveEvidence(ctx, path, held)
}

// turn starts fresh, keeping only the grounded drafts. A turn that failed to
// clear would leave the last one's reads standing, so a failure removes the
// file rather than leaving it.
func turn(ctx context.Context, env Env, payload hookio.Payload) {
	path, err := ledgerPath(env.ledgerDir(), payload)
	if err != nil {
		return
	}

	fresh := freshEvidence()
	fresh.Grounded = loadEvidence(ctx, path).Grounded

	err = saveEvidence(ctx, path, fresh)
	if err != nil {
		_ = os.Remove(path)
	}
}

// gate returns the refusal this call earns, or nothing.
func gate(ctx context.Context, env Env, payload hookio.Payload) string {
	if payload.ToolName == "Bash" {
		found := readsBadly(ctx, env, payload)
		if found != "" || writing(env, payload.ToolInput.Command) == writesNothing {
			return found
		}
	} else if !gated(payload) {
		return ""
	}

	config, err := env.config()
	if err != nil {
		return configRefusal(err)
	}

	prefix := config.ListingPrefix

	path, err := ledgerPath(env.ledgerDir(), payload)

	held := freshEvidence()
	if err == nil {
		held = loadEvidence(ctx, path)
	}

	if held.complete() {
		return ""
	}

	// A plain `sdd new` names no draft, so nothing grounded stands in for it.
	if payload.ToolName == "Bash" && writing(env, payload.ToolInput.Command) == writesThroughCapture &&
		capturesGrounded(env, held, payload.ToolInput.Command) {
		return ""
	}

	haveSemantic, haveLiteral, haveListing := held.Query > 0, held.Term > 0, len(held.Listings) > 0

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

	reads := readsOf(config)

	return refusal("reads.txt", "{read}", reads.command, "{suffix}", reads.suffix, "{listing}", listingCommand(prefix),
		"{have}", carries, "{missing}", strings.Join(missing, " "))
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

func configRefusal(err error) string {
	return fmt.Sprintf("GROUNDING GATE: refused. %v\n\nFix `.sdd/grounding.yaml`, then ask again.\n", err)
}

func readsBadly(ctx context.Context, env Env, payload hookio.Payload) string {
	raw := payload.ToolInput.Command
	if elsewhere(env, raw) {
		return ""
	}

	texts := spokenTexts(raw)

	// A config that does not parse still leaves bare `sdd` recognised, so a
	// broken file never refuses a command reading nothing.
	config, configErr := env.config()
	reads := readsOf(config)

	for _, check := range []func() string{
		func() string { return discardsAStream(reads, texts, raw) },
		func() string { return hidesTheDownstream(ctx, env, payload.SessionID, texts, reads, config.ShowDepth, configErr) },
	} {
		found := check()
		if found != "" {
			return found
		}
	}

	return ""
}

type graphWrite int

const (
	writesNothing graphWrite = iota
	writesThroughCapture
	writesThroughNew
)

// writing says how the command writes to this project's graph, if it does.
// `agentic-capture` wins over `sdd new`, since only it can pass on a grounded
// draft.
func writing(env Env, raw string) graphWrite {
	if elsewhere(env, raw) {
		return writesNothing
	}

	texts := spokenTexts(raw)

	for _, text := range texts {
		if callsCapture(text) {
			return writesThroughCapture
		}
	}

	for _, text := range texts {
		if callsNew(text) {
			return writesThroughNew
		}
	}

	return writesNothing
}

// capturesGrounded reports whether a draft the capture names is grounded.
func capturesGrounded(env Env, held evidence, command string) bool {
	for _, named := range draftPaths(env, command) {
		draft, err := env.grounding(named.path)
		if err == nil && held.grounds(draft) {
			return true
		}
	}

	return false
}

// grounding is what a draft grounded now would be recorded as.
func (env Env) grounding(path string) (groundedDraft, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return groundedDraft{}, fmt.Errorf("reading the draft: %w", err)
	}

	sum := sha256.Sum256(content)

	return groundedDraft{Draft: hex.EncodeToString(sum[:]), Graph: env.graphState()}, nil
}

// graphState names what the graph holds: how many entry files, and the newest
// by name. Entries are immutable and named for the moment they were captured,
// so a capture moves both and only a hand edit moves neither.
func (env Env) graphState() string {
	count, newest := 0, ""

	_ = filepath.WalkDir(filepath.Join(env.graphDir(), "graph"), func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}

		count++
		newest = max(newest, filepath.ToSlash(path))

		return nil
	})

	return fmt.Sprintf("%d:%s", count, filepath.Base(newest))
}

type namedDraft struct {
	name string
	path string
}

// draftPaths names every file the command's draft names can mean. A relative
// name resolves against the working directory and the project root only: every
// other checkout is somebody else's.
func draftPaths(env Env, command string) []namedDraft {
	project := env.ProjectDir
	if project == "" {
		project = "."
	}

	bases := []string{env.WorkingDir}
	if project != env.WorkingDir {
		bases = append(bases, project)
	}

	var found []namedDraft

	for _, candidate := range draftName.FindAllString(command, -1) {
		if filepath.IsAbs(candidate) {
			found = append(found, namedDraft{name: candidate, path: candidate})

			continue
		}

		for _, base := range bases {
			found = append(found, namedDraft{name: candidate, path: filepath.Join(base, candidate)})
		}
	}

	return found
}

func discardsAStream(reads reads, texts []string, raw string) string {
	for _, text := range texts {
		if reads.reachedBy(text) && toNull.MatchString(text) {
			return refusal("stream.txt", "{command}", strings.TrimSpace(raw), "{read}", reads.command, "{suffix}", reads.suffix)
		}
	}

	return ""
}

// hidesTheDownstream refuses on a broken config only once a show turns up, so
// a broken config never refuses a command reading nothing.
func hidesTheDownstream(ctx context.Context, env Env, session string, texts []string, reads reads,
	owed graphconfig.ShowDepth, configErr error,
) string {
	for _, command := range texts {
		if !reads.show.MatchString(command) {
			continue
		}

		if configErr != nil {
			return configRefusal(configErr)
		}

		missing := readsTooShallow(reads, command, owed)
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

		if env.exempt(ctx, session, owed.ExemptSeats) {
			return ""
		}

		return refusal("downstream.txt", "{command}", strings.TrimSpace(command), "{missing}", missing,
			"{depth}", depthFlags(owed))
	}

	return ""
}

// readsTooShallow says what a show falls short of, in the words the refusal
// needs. A body is immutable, so a surface it names keeps that spelling after a
// later entry renamed it: the rename lives downstream. `--up` shows what the
// entry answered, which says whether it still applies.
func readsTooShallow(reads reads, command string, owed graphconfig.ShowDepth) string {
	found := reads.show.FindString(command)
	if found == "" {
		return ""
	}

	var short []string

	for _, reach := range []struct {
		flag    string
		pattern *regexp.Regexp
		wanted  int
	}{
		{flag: "down", pattern: downDepth, wanted: owed.Down},
		{flag: "up", pattern: upDepth, wanted: owed.Up},
	} {
		if reach.wanted == 0 {
			continue
		}

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

	return strings.Join(short, " and ") + " reads short of `" + depthFlags(owed) + "`."
}

// depthFlags spells the depth owed as the flags a show carries, leaving out a
// direction owing nothing.
func depthFlags(owed graphconfig.ShowDepth) string {
	var flags []string

	if owed.Down > 0 {
		flags = append(flags, fmt.Sprintf("--down %d", owed.Down))
	}

	if owed.Up > 0 {
		flags = append(flags, fmt.Sprintf("--up %d", owed.Up))
	}

	return strings.Join(flags, " ")
}

// exempt reports whether the session holds a seat owing no depth. A subagent
// carries its session's id, so it reads as the seat its session holds.
func (env Env) exempt(ctx context.Context, session string, seats []string) bool {
	if len(seats) == 0 {
		return false
	}

	root := project.Root(ctx, env.project(), env.Checkout)

	return slices.Contains(seats, seat.OfSession(seat.Dir(root), session))
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

func (env Env) config() (graphconfig.Config, error) {
	config, err := graphconfig.Load(env.graphDir())
	if err != nil {
		return graphconfig.Config{}, fmt.Errorf("the project's graph config: %w", err)
	}

	return config, nil
}

// listingsIn names every listing a view command performed: each topic carrying
// the prefix, or, with no prefix, the view of every topic.
func (reads reads) listingsIn(prefix string, command string) []string {
	if prefix == "" {
		if reads.everyTopic.MatchString(command) {
			return []string{"every topic"}
		}

		return nil
	}

	pattern := regexp.MustCompile(reads.tool + `\s+view\b[^\n]*topic\(\s*"?` + regexp.QuoteMeta(prefix))

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
