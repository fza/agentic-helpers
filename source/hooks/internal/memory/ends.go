package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
)

// endReached opens the retire reason a reached end gives, before its
// condition.
const endReached = "end reached: "

const untilForms = "closed:<graph id>, merged:<branch>[@<into>], changed:<path>, gone:<path>, claim, session, at:<time> or captured"

var (
	ErrBadUntil  = errors.New("--until takes " + untilForms)
	ErrNoExport  = errors.New("closed: needs sdd export, which this sdd lacks")
	ErrRuleUntil = errors.New("--until is for an entry that ends; a rule ends only when someone retires it")
)

// condition is one declared end: its form and what the form names.
type condition struct {
	form  string
	value string
}

func (held condition) String() string {
	if held.value == "" {
		return held.form
	}

	return held.form + ":" + held.value
}

// parseCondition reads one condition as --until spells it.
func parseCondition(text string) (condition, error) {
	text = strings.TrimSpace(text)
	form, value, named := strings.Cut(text, ":")

	switch {
	case !named && (form == "claim" || form == "session" || form == "captured"):
		return condition{form: form}, nil
	case !named || value == "":
	case form == "closed" && (isGraphID(value) || wholeQualifiedID.MatchString(value)):
		return condition{form: form, value: value}, nil
	case form == "merged" && !strings.ContainsAny(value, " \t\n"):
		branch, into, targeted := strings.Cut(value, "@")
		if branch != "" && (!targeted || into != "") {
			return condition{form: form, value: value}, nil
		}
	case form == "changed" || form == "gone":
		return condition{form: form, value: value}, nil
	case form == "at":
		_, err := parseEndTime(value)
		if err == nil {
			return condition{form: form, value: value}, nil
		}
	}

	return condition{}, fmt.Errorf("%w, not %q", ErrBadUntil, text)
}

// parseEndTime reads an `at:` time: RFC 3339, or a date meaning its start in
// local time.
func parseEndTime(value string) (time.Time, error) {
	at, err := time.Parse(time.RFC3339, value)
	if err == nil {
		return at, nil
	}

	at, err = time.ParseInLocation(time.DateOnly, value, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("reading the time %q: %w", value, err)
	}

	return at, nil
}

// untilFor checks a filing's --until conditions and spells each the way the
// entry keeps it: a path relative to the work tree, its content hash at filing
// kept for `changed:`.
func (mem *memory) untilFor(ctx context.Context, here tree, given []string) ([]string, map[string]string, error) {
	var conditions []string

	hashes := map[string]string{}

	for _, text := range given {
		held, err := parseCondition(text)
		if err != nil {
			return nil, nil, err
		}

		if held.form == "closed" && mem.lacksExport(ctx) {
			return nil, nil, ErrNoExport
		}

		// A graph entry is watched in the form a link stores it: qualified where
		// a connected repo holds it.
		if held.form == "closed" {
			resolved, err := mem.resolveGraphTarget(ctx, held.value)
			if err == nil {
				held.value = resolved
			}
		}

		if held.form == "changed" || held.form == "gone" {
			path := relative(here.root, held.value)
			if path == "" {
				return nil, nil, fmt.Errorf("--until %s names no path inside the work tree", text)
			}

			held.value = path

			if held.form == "changed" {
				hashes[path], _ = fileHash(filepath.Join(here.root, filepath.FromSlash(path)))
			}
		}

		if !slices.Contains(conditions, held.String()) {
			conditions = append(conditions, held.String())
		}
	}

	if len(hashes) == 0 {
		hashes = nil
	}

	return conditions, hashes, nil
}

// lacksExport is whether the installed sdd is known to carry no `export`,
// which every graph status is read from.
func (mem *memory) lacksExport(ctx context.Context) bool {
	if mem.env.Sdd == nil {
		return false
	}

	return !mem.env.Sdd.Of(ctx, mem.mainRoot(ctx)).Export
}

// endProbe answers the end check's questions once per check: the graph's
// statuses and each branch's state.
type endProbe struct {
	mem      *memory
	here     tree
	statuses map[string]graphState
	asked    bool
	branches map[string]branchState
}

type branchState struct {
	exists bool
	merged bool
}

func (mem *memory) probe(ctx context.Context) *endProbe {
	return &endProbe{mem: mem, here: mem.checkout(ctx), branches: map[string]branchState{}}
}

// met is whether held reached the end the condition declares.
func (probe *endProbe) met(ctx context.Context, held *entry, end condition) bool {
	switch end.form {
	case "closed":
		if !probe.asked {
			probe.statuses, _ = probe.mem.graphStatuses(ctx)
			probe.asked = true
		}

		status := probe.statuses[end.value].Status

		return status == "closed-by" || status == "superseded-by"
	case "merged":
		state := probe.branch(ctx, end.value)

		return state.exists && state.merged
	case "changed":
		hash, _ := probe.here.fileHash(filepath.Join(probe.here.root, filepath.FromSlash(end.value)))

		return hash != held.untilHashes[end.value]
	case "gone":
		_, err := os.Lstat(filepath.Join(probe.here.root, filepath.FromSlash(end.value)))

		return errors.Is(err, os.ErrNotExist)
	case "claim":
		return probe.mem.claimedSince(held)
	case "session":
		return probe.mem.holdEnded(held)
	case "at":
		at, err := parseEndTime(end.value)

		return err == nil && !probe.mem.now().Before(at)
	case "captured":
		return capturedSince(held)
	}

	return false
}

// branch is whether a `merged:` branch exists and has its tip on its target:
// the default branch, the target of refs/remotes/origin/HEAD, else main.
func (probe *endProbe) branch(ctx context.Context, value string) branchState {
	known, asked := probe.branches[value]
	if asked {
		return known
	}

	branch, into, named := strings.Cut(value, "@")
	if !named {
		into = probe.defaultBranch(ctx)
	}

	git := probe.mem.env.Git
	state := branchState{}

	_, err := git.Output(ctx, probe.here.root, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if err == nil {
		state.exists = true
		_, err = git.Output(ctx, probe.here.root, "merge-base", "--is-ancestor", "refs/heads/"+branch, into)
		state.merged = err == nil
	}

	probe.branches[value] = state

	return state
}

func (probe *endProbe) defaultBranch(ctx context.Context) string {
	target, err := probe.mem.env.Git.Output(ctx, probe.here.root, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD")
	if err == nil && strings.TrimSpace(target) != "" {
		return strings.TrimSpace(target)
	}

	return "main"
}

// claimedSince is whether the seat that filed held was claimed by another
// session after the filing.
func (mem *memory) claimedSince(held *entry) bool {
	for _, read := range []func(string, string) (seat.Claim, bool){seat.Read, seat.ReadLast} {
		claim, found := read(mem.seatsDir(), held.seatKey)
		if !found || claim.SessionID == "" || claim.SessionID == held.session {
			continue
		}

		at, err := time.Parse(time.RFC3339Nano, claim.Claimed)
		if err == nil && at.After(held.filed) {
			return true
		}
	}

	return false
}

// hold is the claim a `session` end watches: the filing seat's key and the
// claim's `claimed` stamp. A session id would not do: `/clear` starts a new
// one while the process keeps its seat and its claim.
type hold struct {
	Seat    string `json:"seat"`
	Claimed string `json:"claimed"`
}

// holdEnded is whether the claim held was filed under is over: closed or
// freed, either of which moves it off the seat's claim file, or replaced by
// another claim. A claim an earlier release wrote carries no `claimed` stamp,
// so only its absence ends it.
func (mem *memory) holdEnded(held *entry) bool {
	if held.untilHold == nil {
		return false
	}

	current, found := seat.Read(mem.seatsDir(), held.untilHold.Seat)

	return !found || current.Claimed != held.untilHold.Claimed
}

// reachedEnd is the condition a reached end retired an entry with, where
// last is such a retire.
func reachedEnd(last transition) (condition, bool) {
	text, ended := strings.CutPrefix(last.because, endReached)
	if !ended {
		return condition{}, false
	}

	end, err := parseCondition(text)
	if err != nil {
		return condition{}, false
	}

	return end, true
}

// endSession retires the entries on seatName's shelf whose `session` end a
// release or a session's end just reached.
func (mem *memory) endSession(ctx context.Context, seatName string) {
	folded, err := mem.load(ctx)
	if err != nil {
		return
	}

	mem.reachEnds(ctx, folded, seatName, "", "session")
}

// capturedSince is whether held links a graph entry filed after it, which
// its id's stamp says.
func capturedSince(held *entry) bool {
	return slices.ContainsFunc(held.links(), func(target string) bool {
		_, bare, qualified := splitQualified(target)
		if qualified {
			target = bare
		}

		if !isGraphID(target) {
			return false
		}

		at, err := time.ParseInLocation("20060102-150405", target[:15], time.Local)

		return err == nil && at.After(held.filed)
	})
}

func conditionsOf(held *entry) []condition {
	var parsed []condition

	for _, text := range held.until {
		end, err := parseCondition(text)
		if err == nil {
			parsed = append(parsed, end)
		}
	}

	return parsed
}

// reachEnds retires every live entry on seatName's shelf, other than a rule,
// one of whose declared ends was reached, the reason naming the condition;
// where only is given, that entry alone, and where form is, conditions of that
// form alone. The retires land under the store lock, checked against the log
// as it stands then, and are applied to folded.
func (mem *memory) reachEnds(ctx context.Context, folded *state, seatName string, only string, form string) []string {
	probe := mem.probe(ctx)

	reached := map[string]string{}

	var order []string

	for _, held := range folded.entryList() {
		if held.status != statusLive || held.kind == kindRule || len(held.until) == 0 || !mem.onShelf(held, seatName) ||
			(only != "" && held.id != only) {
			continue
		}

		for _, end := range conditionsOf(held) {
			if (form == "" || end.form == form) && probe.met(ctx, held, end) {
				reached[held.id] = end.String()
				order = append(order, held.id)

				break
			}
		}
	}

	if len(order) == 0 {
		return nil
	}

	unlock, err := mem.lockStore(ctx)
	if err != nil {
		return nil
	}
	defer unlock()

	current, err := mem.load(ctx)
	if err != nil {
		return nil
	}

	var events []memorylog.Event

	for _, id := range order {
		held, found := current.entries[id]
		if !found || held.status != statusLive {
			continue
		}

		raw, err := json.Marshal(retirePayload{Because: endReached + reached[id]})
		if err != nil {
			return nil
		}

		events = append(events, memorylog.Event{Kind: memorylog.KindRetire, Entry: id, Time: mem.now(), Seat: seatName, Payload: raw})
	}

	if len(events) == 0 || mem.appendEvents(ctx, events...) != nil {
		return nil
	}

	lines := make([]string, 0, len(events))

	for _, event := range events {
		_ = folded.apply(memorylog.Row{Kind: event.Kind, Entry: event.Entry, Time: event.Time, Seat: event.Seat}, event.Payload)
		lines = append(lines, fmt.Sprintf("retired %s: %s%s", event.Entry, endReached, reached[event.Entry]))
	}

	return lines
}

// endedSince is every entry on seatName's shelf a reached end retired after
// since, each with its condition.
func (mem *memory) endedSince(folded *state, seatName string, since time.Time) []string {
	var lines []string

	for _, held := range folded.entryList() {
		last, found := held.latest()
		if !found || held.status != statusRetired || !mem.onShelf(held, seatName) || !last.at.After(since) {
			continue
		}

		condition, ended := strings.CutPrefix(last.because, endReached)
		if ended {
			lines = append(lines, fmt.Sprintf("  %s %s: %s", held.id, held.title, condition))
		}
	}

	return lines
}

// inferredEnd is why held looks ended though no declared end says so: every
// link it holds ended, every anchor is gone, or a `merged:` branch is gone.
// Empty where nothing says so.
func (probe *endProbe) inferredEnd(ctx context.Context, held *entry) string {
	if links := held.inferringLinks(); len(links) > 0 {
		states := map[string]string{}
		for _, link := range held.linkStates() {
			states[link.Target] = link.State
		}

		if !slices.ContainsFunc(links, func(target string) bool { return !held.linkEnded(states[target]) }) {
			return "every link ended"
		}
	}

	if len(held.anchors) > 0 && !slices.ContainsFunc(held.anchors, func(anchored anchor) bool {
		_, found := probe.here.fileHash(filepath.Join(probe.here.root, filepath.FromSlash(anchored.Path)))

		return found
	}) {
		return "every anchor gone"
	}

	for _, end := range conditionsOf(held) {
		if end.form == "merged" && !probe.branch(ctx, end.value).exists {
			return "branch " + strings.Split(end.value, "@")[0] + " is gone"
		}
	}

	return ""
}

// inferringLinks is the links whose end can infer held's: a todo's graph links
// alone, since a todo ends by its own work, never by the predecessor or the
// finding it links back to; every link of any other kind, so a trap linked to
// its fix todo is flagged once that todo ends.
func (held *entry) inferringLinks() []string {
	if held.kind != kindTodo {
		return held.links()
	}

	var graph []string

	for _, target := range held.links() {
		_, _, qualified := splitQualified(target)
		if isGraphID(target) || qualified {
			graph = append(graph, target)
		}
	}

	return graph
}

// linkEnded is whether a link target in state counts as ended for held: a
// settled decision is in force, and a memory target held itself superseded is
// replaced by held, not ended.
func (held *entry) linkEnded(state string) bool {
	switch state {
	case "", "ok", "settled", "superseded by " + held.id:
		return false
	}

	return true
}

// retireCandidate is a live entry whose end the tool infers, with why.
type retireCandidate struct {
	held *entry
	why  string
}

func (candidate retireCandidate) flag() string {
	return "retire? " + candidate.why
}

// retireCandidates is every live entry on seatName's shelf, other than a
// rule, whose end the tool infers, each with why.
func (mem *memory) retireCandidates(ctx context.Context, folded *state, seatName string) []retireCandidate {
	probe := mem.probe(ctx)

	var candidates []retireCandidate

	for _, held := range folded.entryList() {
		if held.status != statusLive || held.kind == kindRule || !mem.onShelf(held, seatName) {
			continue
		}

		why := probe.inferredEnd(ctx, held)
		if why != "" {
			candidates = append(candidates, retireCandidate{held: held, why: why})
		}
	}

	return candidates
}
