// Package grounding holds a question, a draft edit or a capture to reads of the
// decision graph: a literal search, a semantic search, and a listing. Credit for
// them lasts until the graph changes or the session's context is compacted.
//
// One search mode alone misses what the other finds, and an empty search reads
// as absence. A listing cannot miss that way, because it enumerates rather
// than matching words. A project naming a listing prefix in
// `.sdd/grounding.yaml` is held to a listing of one topic carrying it; every
// other project to a view naming every topic. A project naming a
// `gather_command` may run all three in one call through it, credited for the
// modes and areas the call names. `sdd show` alone never satisfies the gate:
// following references reaches only entries something already cited.
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
// seat a session holds from agentic-memory's claims, through the claude process
// owning the hook.
//
// The file can also lift the gate: `enabled: false` switches it off for the
// project, and a seat named in top-level `exempt_seats` is never refused.
//
// Modes:
//
//	record  PostToolUse on Bash and sdd's MCP reads: notes which graph reads a
//	        call performed, and keeps them across the session's own capture
//	        when its entry is the graph's only change; on Write and Edit: notes
//	        a draft saved while every read stands
//	gate    PreToolUse: refuses a bad read; lets AskUserQuestion and a draft
//	        edit through with a note until every read stands, and refuses a
//	        capture until every read stands or its draft is grounded
//	start   SessionStart: removes ledgers of sessions gone a week, and after a
//	        compaction forgets this session's reads
//	end     SessionEnd: removes this session's ledgers
package grounding

import (
	"cmp"
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
	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/pieces"
	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/project"
	"github.com/fza/agentic-helpers/source/hooks/internal/sddcaps"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
)

//go:embed refusals
var refusals embed.FS

var ErrUsage = errors.New("usage: agentic-grounding-gate record|gate|start|end")

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

// forgetReads drops the session's reads after a compaction: the reads' output
// is gone from the context, so their credit goes too. Grounded drafts stay.
func forgetReads(ctx context.Context, env Env, payload hookio.Payload) {
	path, err := ledgerPath(env.graphRoot(), env.ledgerDir(), payload)
	if err != nil {
		return
	}

	held := loadEvidence(ctx, path)
	held.forget()

	err = saveEvidence(ctx, env.graphRoot(), path, held)
	if err != nil {
		_ = os.Remove(path)
	}
}

// Matching this shape is the whole safety of the sweep: only a ledger named for
// a session, or for an agent within one, is ever removed.
var ledgerName = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}(?:\.[^/]+)?\.json$`)

// Claude Code draws a refusal of a gated tool as a red error in its default
// view, whether the hook exits 2 or answers a JSON deny, while a refusal of
// `Bash` or an MCP tool folds into a grey line. So a gated tool is let through
// with the refusal's text as a note the agent reads, and the capture that would
// write its work into the graph stays refused.
var gatedTools = map[string]bool{"AskUserQuestion": true, "Write": true, "Edit": true, "NotebookEdit": true}

const (
	refusedLine      = "GROUNDING GATE: refused."
	readsRefusedLine = "GROUNDING GATE: refused. Run the reads owed, then run this call again."
	letThroughLine   = "GROUNDING GATE: let through, reads owed before the next question or draft."
	letThroughAnyway = "GROUNDING GATE: let through."
	ungroundedDraft  = "This draft is not grounded: its capture owes the reads."
	searchHint       = "Empty search ≠ absence; a listing settles membership."
	configRetry      = ", then run this call again."
)

var savingTools = map[string]bool{"Write": true, "Edit": true}

// Env is where a hook invocation runs. ProjectDir is empty when the client set
// none; Checkout finds the main checkout, where the seat claims live.
type Env struct {
	ProjectDir string
	WorkingDir string
	Home       string
	// Roots locate the memory store whose seats exempt_seats names.
	Roots     home.Roots
	Checkout  project.CommonDirLookup
	Runner    sddcaps.Runner
	Processes seat.Processes
	OwnerName string
	// Settings and Getenv carry the plugin's options: the gate's own switch, and
	// memory's, which exempt_seats rests on.
	Settings []string
	Getenv   func(string) string
	// SddCache and LookPath let the probe of what sdd offers keep its answer
	// per binary; either left empty, sdd's help is read on every probe.
	SddCache string
	LookPath func(string) (string, error)
}

func (env Env) options() pluginconfig.Options {
	options, err := pluginconfig.ReadWith(env.Settings, env.Getenv)
	if err != nil {
		return pluginconfig.Defaults()
	}

	return options
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

// graphRoot is the folder holding the graph's directory: every write below
// the graph walks from it, so a graph directory committed as a link is never
// written through.
func (env Env) graphRoot() string {
	return filepath.Dir(env.graphDir())
}

func (env Env) graphDir() string {
	return env.graphProject().Dir
}

func (env Env) graphProject() graphconfig.Project {
	return graphconfig.Resolve(env.project())
}

// Main runs one hook invocation. A project carrying no graph, or switching the
// gate off, gets no answer rather than a refusal it cannot act on. A config
// that does not parse keeps the gate on, since whether the project switched it
// off is part of what the file says.
func Main(ctx context.Context, args []string, env Env, streams hookio.Streams) int {
	if len(args) != 1 || !slices.Contains([]string{"record", "gate", "start", "end"}, args[0]) {
		_, _ = fmt.Fprintln(streams.Err, ErrUsage)

		return 1
	}

	if !pieces.GroundingGate.On(env.options()) || env.graphDir() == "" {
		return 0
	}

	config, err := env.config()
	if err == nil && config.Disabled {
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
	case "start":
		reap(ctx, env.ledgerDir(), time.Now())

		if payload.Source == "compact" {
			forgetReads(ctx, env, payload)
		}

		return 0
	case "end":
		end(ctx, env.ledgerDir(), payload)

		return 0
	default:
		refusal := gate(ctx, env, payload)
		if refusal == "" {
			return 0
		}

		if gatedTools[payload.ToolName] {
			if strings.HasPrefix(refusal, readsRefusedLine) && !firstNote(ctx, env, payload) {
				return 0
			}

			_ = hookio.AddContext(streams.Out, "PreToolUse", letThrough(payload, refusal))

			return 0
		}

		_, _ = fmt.Fprint(streams.Err, refusal)

		return refused
	}
}

// record credits the reads a command performed: only calls the shell itself
// runs, since crediting a quoted mention lets an `echo` satisfy the gate, and
// no search or listing inside code a shell inside the command runs.
func record(ctx context.Context, env Env, payload hookio.Payload) {
	if savingTools[payload.ToolName] {
		recordDraft(ctx, env, payload)

		return
	}

	keepAcrossOwnCapture(ctx, env, payload)
	recordEngineCapture(ctx, env, payload)

	if strings.HasPrefix(payload.ToolName, "mcp__sdd__") {
		recordMCP(ctx, env, payload)

		return
	}

	command := payload.ToolInput.Command
	if command == "" || payload.Failed() {
		return
	}

	calls := spokenIn(command, false)[0].calls

	path, err := ledgerPath(env.graphRoot(), env.ledgerDir(), payload)
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
	held.against(env.graphState())

	changed := credit(&held, reads, config.ListingPrefix, calls)

	for _, gathered := range reads.gatheringsIn(calls) {
		held.Search++
		changed = true

		if gathered.query {
			held.Query++
		}

		if gathered.term {
			held.Term++
		}

		for _, listing := range gathered.listings(config.ListingPrefix) {
			held.addListing(listing)
		}
	}

	if changed {
		// A ledger that cannot be written holds the reads nowhere, and
		// the gate then refuses: the safe side.
		_ = saveEvidence(ctx, env.graphRoot(), path, held)
	}
}

// credit notes the searches and listings the calls ran. A command credits a
// search mode once, however many of its calls ran it.
func credit(held *evidence, reads reads, prefix string, calls []call) bool {
	searched, semantic, literal, changed := false, false, false, false

	for _, called := range calls {
		if !reads.reachedBy(called) {
			continue
		}

		text := called.text()

		if reads.search.MatchString(text) {
			searched = true
			semantic = semantic || reads.semantic.MatchString(text)
			literal = literal || reads.literal.MatchString(text)
		}

		if reads.view.MatchString(text) {
			for _, listing := range reads.listingsIn(prefix, text) {
				held.addListing(listing)
				changed = true
			}
		}
	}

	if searched {
		held.Search++
		changed = true
	}

	if semantic {
		held.Query++
	}

	if literal {
		held.Term++
	}

	return changed
}

// recordDraft grounds a draft saved in a turn carrying every read. The hash is
// of the file as saved, since an `Edit` names only the strings it swapped.
func recordDraft(ctx context.Context, env Env, payload hookio.Payload) {
	target := payload.ToolInput.FilePath
	if !draftPath.MatchString(target) || payload.Failed() {
		return
	}

	path, err := ledgerPath(env.graphRoot(), env.ledgerDir(), payload)
	if err != nil {
		return
	}

	held := loadEvidence(ctx, path)
	held.against(env.graphState())
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
	_ = saveEvidence(ctx, env.graphRoot(), path, held)
}

// gate returns the refusal this call earns, or nothing. A graph write holding
// a credential is refused first, an exempt seat included, and so is one whose
// scan an ended context cut short: the reads the gate would weigh next go
// unread then too, and would name reads owed that are not. One whose files
// cannot be scanned is refused last, once nothing else refuses it: the reads
// or the engine say more about what to do next.
func gate(ctx context.Context, env Env, payload hookio.Payload) string {
	first, unscanned := leaksCredential(ctx, env, payload)
	if first != "" {
		return first
	}

	return cmp.Or(judge(ctx, env, payload), unscanned)
}

// judge returns the refusal the reads, depths and engine rules give, or
// nothing. A seat the project exempts earns none.
func judge(ctx context.Context, env Env, payload hookio.Payload) string {
	config, configErr := env.config()
	if configErr == nil && env.exempt(ctx, config.ExemptSeats) {
		return ""
	}

	if payload.ToolName == mcpShow {
		if configErr != nil {
			return configRefusal(configErr)
		}

		return mcpShowTooShallow(ctx, env, payload, config.ShowDepth)
	}

	// A config that does not parse already refuses every capture start; a
	// report goes on, since the gate cannot tell a capture's from another
	// procedure's without it.
	if payload.ToolName == mcpNext {
		if configErr != nil {
			return ""
		}

		return engineCaptureRefusal(ctx, env, payload, config)
	}

	if payload.ToolName == "Bash" {
		found := readsBadly(ctx, env, payload)
		if found != "" || writing(env, payload.ToolInput.Command) == writesNothing {
			return found
		}
	} else if !gated(payload) {
		return ""
	}

	if configErr != nil {
		return configRefusal(configErr)
	}

	if payload.ToolName == "Bash" && writing(env, payload.ToolInput.Command) == writesThroughCapture &&
		!config.CLICapture && env.servesEngine(ctx) {
		return refusal("engine.txt")
	}

	prefix := config.ListingPrefix

	path, err := ledgerPath(env.graphRoot(), env.ledgerDir(), payload)

	held := freshEvidence()
	if err == nil {
		held = loadEvidence(ctx, path)
	}

	held.against(env.graphState())

	if held.complete() {
		if gatesCapture(payload) {
			return engineCaptureRefusal(ctx, env, payload, config)
		}

		return ""
	}

	// A plain `sdd new` names no draft, so nothing grounded stands in for it.
	if payload.ToolName == "Bash" && writing(env, payload.ToolInput.Command) == writesThroughCapture &&
		capturesGrounded(env, held, payload.ToolInput.Command) {
		return ""
	}

	reads := readsOf(config)

	return refusal("reads.txt", "{gather}", gatherLine(reads), "{owed}", owedReads(held, reads, prefix))
}

// owedReads names each read still missing, the reason it is owed above the
// command that answers it. A read already credited is left out: naming it
// again sends an agent to repeat a read the gate holds.
func owedReads(held evidence, reads reads, prefix string) string {
	var owed []string

	if held.Query == 0 {
		owed = append(owed, "No `--query` search since the graph last changed.\n  "+
			reads.command+" search --query '<subject>'"+reads.searchFlags+reads.suffix)
	}

	if held.Term == 0 {
		owed = append(owed, "No `--term` search since the graph last changed.\n  "+
			reads.command+" search --term '<literal>'"+reads.searchFlags+reads.suffix)
	}

	if len(held.Listings) == 0 {
		reason := "No listing of every topic since the graph last changed."
		if prefix != "" {
			reason = "No `" + prefix + "` listing since the graph last changed."
		}

		owed = append(owed, reason+"\n  "+reads.command+" "+listingCommand(prefix)+reads.suffix)
	}

	if held.Query == 0 || held.Term == 0 {
		owed = append(owed, "\n"+searchHint)
	}

	return strings.Join(owed, "\n")
}

// letThrough is the note a let-through call carries: the refusal's text under a
// first line saying the call went ahead. The reads stay owed for what comes
// next, since asking the call again would repeat one already made.
func letThrough(payload hookio.Payload, refusal string) string {
	if !strings.HasPrefix(refusal, readsRefusedLine) {
		note := strings.Replace(refusal, refusedLine, letThroughAnyway, 1)

		return strings.Replace(note, configRetry, ".", 1)
	}

	note := strings.Replace(refusal, readsRefusedLine, letThroughLine, 1)

	if payload.ToolName != "AskUserQuestion" {
		note = strings.Replace(note, letThroughLine, letThroughLine+" "+ungroundedDraft, 1)
	}

	return note
}

// firstNote says whether the reads owed are a cause no note has named yet, and
// records them as named. A hidden message repeated without a new cause is a
// defect: the cause is the set of missing reads under the graph as it stands,
// so a graph change, a compaction or a changed set names them again. A ledger
// that cannot be written leaves the note firing: the safe side.
func firstNote(ctx context.Context, env Env, payload hookio.Payload) bool {
	path, err := ledgerPath(env.graphRoot(), env.ledgerDir(), payload)
	if err != nil {
		return true
	}

	held := loadEvidence(ctx, path)
	held.against(env.graphState())

	cause := held.missing()
	if held.Noted == cause {
		return false
	}

	held.Noted = cause
	_ = saveEvidence(ctx, env.graphRoot(), path, held)

	return true
}

func gated(payload hookio.Payload) bool {
	if payload.ToolName == "AskUserQuestion" || gatesCapture(payload) {
		return true
	}

	if !gatedTools[payload.ToolName] {
		return false
	}

	target := cmp.Or(payload.ToolInput.FilePath, payload.ToolInput.NotebookPath)

	return draftPath.MatchString(target)
}

func configRefusal(err error) string {
	return fmt.Sprintf("GROUNDING GATE: refused. %v\n\nFix `.sdd/grounding.yaml`%s\n", err, configRetry)
}

func readsBadly(ctx context.Context, env Env, payload hookio.Payload) string {
	raw := payload.ToolInput.Command
	if elsewhere(env, raw) {
		return ""
	}

	heard := spokenIn(raw, false)

	// A config that does not parse still leaves bare `sdd` recognised, so a
	// broken file never refuses a command reading nothing.
	config, configErr := env.config()
	reads := readsOf(config)

	for _, check := range []func() string{
		func() string { return discardsAStream(reads, heard, raw) },
		func() string {
			return hidesTheDownstream(ctx, env, payload.SessionID, readTexts(reads, heard), reads, config.ShowDepth, configErr)
		},
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
// draft. Code that does not parse writes through `sdd new` wherever it names
// `sdd` or `agentic-capture`, so its reads stay owed with shell-guard off.
func writing(env Env, raw string) graphWrite {
	if elsewhere(env, raw) {
		return writesNothing
	}

	return writeIn(raw)
}

// writeIn says how the command writes to a graph, this project's or another's.
func writeIn(raw string) graphWrite {
	heard := spokenIn(raw, false)

	for _, stretch := range heard {
		if slices.ContainsFunc(stretch.calls, callsCapture) {
			return writesThroughCapture
		}
	}

	for _, stretch := range heard {
		if slices.ContainsFunc(stretch.calls, callsNew) || namesTool.MatchString(stretch.unparsed) {
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

	_ = filepath.WalkDir(env.graphProject().Entries, func(path string, entry os.DirEntry, err error) error {
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

// discardsAStream refuses a stretch of code calling the graph's tool and
// discarding a stream anywhere in it; one that does not parse, where it names
// the tool and its text discards one.
func discardsAStream(reads reads, heard []spoken, raw string) string {
	for _, stretch := range heard {
		reached := stretch.discards && slices.ContainsFunc(stretch.calls, reads.reachedBy)
		guessed := namesTool.MatchString(stretch.unparsed) && toNull.MatchString(stretch.unparsed)

		if reached || guessed {
			return refusal("stream.txt", "{command}", strings.TrimSpace(raw), "{read}", reads.command, "{suffix}", reads.suffix,
				"{flags}", reads.streamFlags)
		}
	}

	return ""
}

// readTexts lists every call of the graph's tool as words, and the text of
// every stretch that does not parse but names the tool.
func readTexts(reads reads, heard []spoken) []string {
	var texts []string

	for _, stretch := range heard {
		if namesTool.MatchString(stretch.unparsed) {
			texts = append(texts, stretch.unparsed)
		}

		for _, called := range stretch.calls {
			if reads.reachedBy(called) {
				texts = append(texts, called.text())
			}
		}
	}

	return texts
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

		if env.exempt(ctx, owed.ExemptSeats) {
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

// exempt reports whether the calling session holds a seat owing no depth: the
// agentic-memory seat its owning claude process claimed. A subagent runs in
// its session's process, so it reads as the seat its session holds.
func (env Env) exempt(ctx context.Context, seats []string) bool {
	if len(seats) == 0 || env.Processes == nil || !pieces.ExemptSeats.Holds(env.options()) {
		return false
	}

	checkout, err := home.Inspect(ctx, env.project(), env.Checkout, nil)
	if err != nil {
		return false
	}

	resolution, err := home.Resolver{Roots: env.Roots}.Find(ctx, checkout)
	if err != nil || resolution.Store == "" || resolution.New {
		return false
	}

	store := env.Roots.Store(resolution.Store, home.Memory)

	held, _ := seat.Of(ctx, seat.Dir(store), env.Processes, env.OwnerName)
	if held == "" {
		return false
	}

	// A store of the current format files a claim under the seat's id; its
	// registry says the name an exemption is written in.
	name := held

	registry, found, err := seat.LoadRegistry(store)
	if err == nil && found {
		name = registry.NameOf(held)
	}

	return name != "" && slices.ContainsFunc(seats, func(exempt string) bool { return seat.Normal(exempt) == name })
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

// listings names every listing a gather call performed: each area it names
// carrying the prefix, or, naming none, every area its subject carries.
func (gathered gathering) listings(prefix string) []string {
	if len(gathered.areas) == 0 {
		if gathered.subject == "" {
			return nil
		}

		return []string{"areas of " + gathered.subject}
	}

	var found []string

	for _, area := range gathered.areas {
		if strings.HasPrefix(area, prefix) {
			found = append(found, area)
		}
	}

	return found
}

// gatherLine offers the gather call ahead of the reads owed, where the project
// names one.
func gatherLine(reads reads) string {
	if reads.gather == "" {
		return ""
	}

	return "\nOne call runs all three:\n\n  " + reads.gather +
		" <entry> --query '<subject>' --term '<literal>'" + reads.suffix + "\n\nOr each read owed apart:\n"
}

func listingCommand(prefix string) string {
	if prefix == "" {
		return `view --layout "active:as-counts"`
	}

	return `view --layout "topic(` + prefix + `<name>):as-list"`
}
