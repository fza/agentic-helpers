package memory

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
)

// A seat's state is a ledger of lines: the agent adds, ticks and removes one
// line at a time through `ack state`, never retyping the rest, and hooks add
// the lines git and the graph already know. Each line is an `item` row of the
// log whose entry is the reserved id `state`, filed under the seat it belongs
// to, so the ledger is keyed as the slot it replaces was and no memory id,
// every one starting `m-`, can collide with it.
const (
	ledgerID = "state"
	// oldLine addresses the free-text slot a seat wrote before the ledger,
	// shown until the agent splits it into lines and ticks it.
	oldLine = "old"

	hookLanded   = "landed"
	hookCaptured = "captured"
	hookDirty    = "dirty"
	hookAsked    = "asked"

	// itemRetext replaces a hook line's text: the one dirty and the one
	// asked line are kept current rather than added again.
	itemRetext = "text"
	lineEnded  = "ended"

	// untilNone is the --until of a state line no condition ends.
	untilNone = "none"
	// hookLineClaimed is why a hook line ended: what git and the graph
	// recorded for one claim's session is the next claim's history.
	hookLineClaimed = "the seat was claimed since"

	// briefHookLines is how many open landed and captured lines stay open
	// past hookLineLifetime, and how many the brief and show state print.
	briefHookLines   = 12
	hookLineLifetime = 24 * time.Hour
	landedLines      = 20
	writtenFiles     = 20
)

func ledgerAsk(at channel) string {
	return "Add or tick state lines where work moved: " +
		hintChoice(at, addStateHint(), settleHint(ledgerID+"#<item>", "done"))
}

func oldStateAsk(at channel) string {
	return "state is a ledger now: split this into lines (" + addStateHint().in(at) + " ...), then " +
		settleHint(ledgerID+"#old", "done").in(at)
}

// stateRefusal answers `state`, which the MCP server does not list, so it
// only ever answers a command line.
const stateRefusal = "state is a ledger now, changed a line at a time: " + self + " ack state --add '<line>' --until <end> | " +
	self + " ack state#<item> --done | --undo | --remove; " + self + " show state prints it"

var (
	ErrStateMixed  = errors.New("state lines take an ack call of their own: give state or state#<item> ids only")
	ErrStateChange = errors.New("ack on state lines takes exactly one of --add, --done, --undo or --remove")
	ErrUntilAdd    = errors.New("--until declares the ends of the lines ack state --add adds; a todo's items and rules take none")
	ErrDueAdd      = errors.New("--due sets when the lines ack state --add adds come due; a todo's items and rules take none")
	ErrStateAdd    = errors.New("--add takes the one id state, no item address")
	ErrShowSeats   = errors.New("--seat names the one seat whose ledger show state reads")
	ErrSeatNoState = errors.New("--seat names whose ledger show state reads; give state among the ids")
	ErrUnknownSeat = errors.New("no such seat")
	// stateAddress is a ledger line's address, `state#<item>`.
	stateAddress = regexp.MustCompile(`^state#([a-z0-9]+)$`)
	// citedMemory finds a memory id or a todo item's address a line's text
	// cites.
	citedMemory = regexp.MustCompile(`\bm-[a-z0-9]{4,16}(?:#[a-z0-9]{3})?\b`)
	landingOnly = regexp.MustCompile(`\bgit\b[^;&|\n]*?\s(?:merge|commit|cherry-pick)\b`)
	// citedAddress and citedCommit find the line address and the commits an
	// agent's line names, for the hook lines it settles.
	citedAddress = regexp.MustCompile(`\bstate#([a-z0-9]+)\b`)
	citedCommit  = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
)

// ledger is one seat's state lines in the order added, removed ones
// included. Changed is the seat's newest change to it, the agent's alone:
// a hook line holds no reasoning, so it answers no nudge and moves no replay.
// OldSettled is when the agent ticked or removed state#old.
type ledger struct {
	Lines      []ledgerLine
	Changed    time.Time
	OldSettled time.Time
}

// ledgerLine is one line as the log left it. Set is whether an ack gave it
// its state, which wins over every end the line would reach on its own.
type ledgerLine struct {
	ID, Text, Link, State, Hook, Commit, Session string
	Paths                                        []string
	Until                                        []string
	UntilHashes                                  map[string]string
	UntilHold                                    *hold
	Set                                          bool
	Added, Due                                   time.Time
}

func (held *ledger) line(id string) *ledgerLine {
	for index := range held.Lines {
		if held.Lines[index].ID == id {
			return &held.Lines[index]
		}
	}

	return nil
}

func (folded *state) ledgerOf(seatName string) *ledger {
	if folded.ledgers == nil {
		folded.ledgers = map[string]*ledger{}
	}

	held, found := folded.ledgers[seatName]
	if !found {
		held = &ledger{}
		folded.ledgers[seatName] = held
	}

	return held
}

// ledgerFor is the seat's ledger as folded, empty where it has none: a
// ledger is created by its first line, never by the agent.
func (folded *state) ledgerFor(seatName string) *ledger {
	held, found := folded.ledgers[seatName]
	if !found {
		return &ledger{}
	}

	return held
}

func (held *ledger) apply(at time.Time, change itemPayload) {
	if change.Hook == "" {
		held.Changed = at
	}

	if change.ID == oldLine {
		switch change.Op {
		case itemCheck, itemRemove:
			held.OldSettled = at
		case itemUncheck:
			held.OldSettled = time.Time{}
		}

		return
	}

	if change.Op == itemAdd {
		if held.line(change.ID) != nil {
			return
		}

		held.Lines = append(held.Lines, ledgerLine{
			ID: change.ID, Text: change.Text, Link: change.Link, State: itemOpen, Hook: change.Hook, Commit: change.Commit,
			Session: change.Session, Paths: change.Paths, Until: change.Until, UntilHashes: change.UntilHashes,
			UntilHold: change.UntilHold, Added: at, Due: change.Due,
		})

		return
	}

	target := held.line(change.ID)
	if target == nil {
		return
	}

	switch change.Op {
	case itemCheck:
		target.State, target.Set = itemDone, true
	case itemUncheck:
		// A line brought back stays open until the agent ticks or removes it:
		// whatever ended it would end it again at the next read.
		target.State, target.Set = itemOpen, true
	case itemRemove:
		target.State, target.Set = itemRemoved, true
	case itemRetext:
		target.Text, target.Paths = change.Text, change.Paths
	}
}

// oldState is the free-text slot the seat wrote itself, while it still waits
// to be split into lines: a hook's draft is dropped unasked.
func (folded *state) oldState(seatName string) (stateSlot, bool) {
	slot, found := folded.states[seatName]
	if !found || slot.Draft || strings.TrimSpace(slot.Body) == "" {
		return stateSlot{}, false
	}

	if folded.ledgerFor(seatName).OldSettled.After(slot.at) {
		return stateSlot{}, false
	}

	return slot, true
}

// lineView is a line as a read shows it: its state worked out, the text it
// shows, the flag of a pinned line, and why it ended on its own.
type lineView struct {
	line   ledgerLine
	state  string
	text   string
	flag   string
	reason string
}

func (view lineView) open() bool {
	return view.state == itemOpen
}

// ledgerViews is every line of the seat's ledger, removed ones aside, its
// state worked out now.
func (mem *memory) ledgerViews(ctx context.Context, folded *state, seatName string) []lineView {
	held := folded.ledgerFor(seatName)
	probe := mem.probe(ctx)

	var views []lineView

	for _, line := range held.Lines {
		if line.State == itemRemoved {
			continue
		}

		views = append(views, mem.viewLine(ctx, probe, folded, seatName, held, line))
	}

	return views
}

// viewLine works a line's state out: an ack's tick, untick or remove wins;
// a hook line ends at the seat's next claim, a landed or captured line also
// once it is out of the newest open ones and a day old, any other hook line
// stays open until its hook ticks it; else the
// line ends once a declared end is reached, once its pinned target ends as a
// linked todo item's does, or once every id its text cites ended.
func (mem *memory) viewLine(ctx context.Context, probe *endProbe, folded *state, seatName string, held *ledger,
	line ledgerLine,
) lineView {
	view := lineView{line: line, state: line.State, text: line.Text}

	if line.Link != "" && line.Hook == "" {
		view.text, view.flag = folded.pinned(line.Link)
	}

	if line.Set || line.State != itemOpen {
		return view
	}

	if line.Hook != "" {
		if line.Added.Before(probe.claimedAt(mem.seatKey(seatName))) {
			view.state, view.reason = lineEnded, hookLineClaimed

			return view
		}

		if historyLine(line) && mem.now().Sub(line.Added) > hookLineLifetime && held.newerHookLines(line.ID) >= briefHookLines {
			view.state, view.reason = lineEnded, fmt.Sprintf("out of the newest %d", briefHookLines)
		}

		return view
	}

	for _, end := range lineConditions(line) {
		if probe.met(ctx, untilHolder(mem.seatKey(seatName), held, line), end) {
			view.state, view.reason = lineEnded, endReached+end.String()

			return view
		}
	}

	if line.Link != "" {
		ended, flag := folded.linkEnd(line.Link)
		view.flag = flag

		if ended {
			view.state, view.reason = lineEnded, strings.TrimPrefix(flag, "→ ")
		}

		return view
	}

	cited := folded.cited(line.Text)
	if len(cited) > 0 && !slices.ContainsFunc(cited, func(id string) bool { return !folded.citedEnded(probe, ctx, id) }) {
		view.state, view.reason = lineEnded, "every id it cites ended"
	}

	return view
}

// historyLine is whether a hook line records what shipped, a landed commit
// or a captured graph entry, rather than where the session stands.
func historyLine(line ledgerLine) bool {
	return line.Hook == hookLanded || line.Hook == hookCaptured
}

// newerHookLines is how many open landed and captured lines the ledger
// gained after the line id, by the state the log records.
func (held *ledger) newerHookLines(id string) int {
	newer, after := 0, false

	for _, other := range held.Lines {
		switch {
		case other.ID == id:
			after = true
		case after && historyLine(other) && other.State == itemOpen:
			newer++
		}
	}

	return newer
}

func lineConditions(line ledgerLine) []condition {
	return conditionsOf(&entry{until: line.Until})
}

// untilHolder is the entry a line's ends are checked against, as an entry's
// are: the line's seat, session, time added, path hashes and claim, and for
// `captured` every graph entry a hook line recorded since the line was added.
func untilHolder(seatKey string, held *ledger, line ledgerLine) *entry {
	holder := &entry{
		seatKey: seatKey, session: line.Session, filed: line.Added, until: line.Until,
		untilHashes: line.UntilHashes, untilHold: line.UntilHold,
	}

	for _, other := range held.Lines {
		if other.Hook == hookCaptured && other.Added.After(line.Added) && other.Link != "" {
			holder.textLinks = append(holder.textLinks, other.Link)
		}
	}

	return holder
}

// pinned is how a pinned line shows its target: the title of an entry, the
// text of an item, the id of a graph entry, and the target's state as a
// linked item's flag reads it.
func (folded *state) pinned(link string) (string, string) {
	_, flag := folded.linkEnd(link)

	todo, itemID, isItem := strings.Cut(link, "#")
	if isItem {
		held, found := folded.entries[todo]
		if found {
			if one := held.item(itemID); one != nil {
				return cmp.Or(one.Text, one.Link), flag
			}
		}

		return link, flag
	}

	if held, found := folded.entries[link]; found {
		return held.title, flag
	}

	return link, flag
}

// linkEnd is whether a pinned line's target ended, as a linked todo item's
// does, and its flag: a todo item ends when ticked or dropped.
func (folded *state) linkEnd(link string) (bool, string) {
	todo, itemID, isItem := strings.Cut(link, "#")
	if !isItem {
		state, flag := folded.itemState(todoItem{Link: link})

		return state == itemDone, flag
	}

	held, found := folded.entries[todo]
	if !found {
		return false, "→ " + link + " gone"
	}

	one := held.item(itemID)
	if one == nil {
		return false, "→ " + link + " gone"
	}

	state, _ := folded.itemState(*one)

	return state == itemDone || state == itemDropped, "→ " + link + " " + state
}

// cited is every id a line's text names that can end: a memory entry or a
// todo item the store holds, a graph id.
func (folded *state) cited(text string) []string {
	var ids []string

	rest := qualifiedIDPattern.ReplaceAllString(text, " ")
	found := qualifiedIDPattern.FindAllString(text, -1)
	found = append(found, graphIDPattern.FindAllString(rest, -1)...)

	for _, id := range citedMemory.FindAllString(rest, -1) {
		todo, _, _ := strings.Cut(id, "#")
		if _, known := folded.entries[todo]; known {
			found = append(found, id)
		}
	}

	for _, id := range found {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}

	return ids
}

// citedEnded is whether an id a line cites ended: a graph entry closed or
// superseded, a memory entry retired or superseded, a todo item ticked or
// dropped.
func (folded *state) citedEnded(probe *endProbe, ctx context.Context, id string) bool {
	if strings.HasPrefix(id, "m-") {
		todo, itemID, isItem := strings.Cut(id, "#")

		held, found := folded.entries[todo]
		if !found {
			return false
		}

		if !isItem {
			return held.status == statusRetired || held.status == statusSuperseded
		}

		one := held.item(itemID)
		if one == nil {
			return false
		}

		state, _ := folded.itemState(*one)

		return state == itemDone || state == itemDropped
	}

	if !probe.asked {
		probe.statuses, _ = probe.mem.graphStatuses(ctx)
		probe.asked = true
	}

	status := probe.statuses[id].Status

	return status == "closed-by" || status == "superseded-by"
}

// ackState changes the calling seat's ledger: adds lines, or ticks, unticks
// or removes the lines addressed `state#<item>`. Every address is checked
// before anything lands. No line is refused for its length, nor the ledger
// for its count: a refusal costs a rewrite.
func (mem *memory) ackState(ctx context.Context, req request) result {
	name, isSeated := mem.seated(ctx)
	if !isSeated {
		return refused("%s", noSeatText(mem.hintChannel(ctx)))
	}

	changes := 0
	for _, given := range []bool{req.Done, req.Undo, req.Remove, len(req.Add) > 0} {
		if given {
			changes++
		}
	}

	if changes != 1 || strings.TrimSpace(req.Decline) != "" || req.Order != "" {
		return refused("%v", ErrStateChange)
	}

	if len(req.Until) > 0 && len(req.Add) == 0 {
		return refused("%v", ErrUntilAdd)
	}

	if strings.TrimSpace(req.Due.text) != "" && len(req.Add) == 0 {
		return refused("%v", ErrDueAdd)
	}

	folded, err := mem.load(ctx)
	if err != nil {
		return refused("%v", err)
	}

	held := folded.ledgerFor(name)

	var (
		events []memorylog.Event
		reply  string
	)

	if len(req.Add) > 0 {
		events, reply, err = mem.addLines(ctx, held, name, req)
	} else {
		events, reply, err = mem.changeLines(folded, held, name, req)
	}

	if err != nil {
		return refused("%v", err)
	}

	err = mem.appendEvents(ctx, events...)
	if err != nil {
		return refused("%v", err)
	}

	lines := []string{reply}

	// An undo reopens a line it once settled: neither an add nor a settle.
	after, err := mem.load(ctx)
	if err == nil && !req.Undo {
		open := openToSettle(mem.ledgerViews(ctx, after, name))

		marks, fires := mem.recordLedger(ctx, name, len(open), len(req.Add) == 0)
		if fires {
			lines = append(lines, mem.pressureLines(mem.hintChannel(ctx), open, marks)...)
		}
	}

	return done(strings.Join(lines, "\n"))
}

// untilNeeded is the refusal of a state line added without an end, its keys
// spelled for the caller's channel: an agent rewrites the call it made by the
// keys the refusal names.
func untilNeeded(at channel) string {
	if at == channelCLI {
		return "ack state --add needs --until: " + untilForms + "; or none for a line only a tick or a remove ends"
	}

	return `ack on state with "add" needs "until": ` + untilForms + `; or "none" for a line only a tick or a remove ends`
}

func (mem *memory) addLines(ctx context.Context, held *ledger, name string, req request) ([]memorylog.Event, string, error) {
	if len(req.IDs) != 1 || req.IDs[0] != ledgerID {
		return nil, "", ErrStateAdd
	}

	if len(req.Until) == 0 {
		return nil, "", errors.New(untilNeeded(mem.hintChannel(ctx)))
	}

	// none keeps no condition: beside another one it adds nothing.
	declared := slices.DeleteFunc(slices.Clone(req.Until), func(text string) bool { return strings.TrimSpace(text) == untilNone })
	here := mem.checkout(ctx)

	until, hashes, err := mem.untilFor(ctx, here, declared)
	if err != nil {
		return nil, "", err
	}

	var untilHold *hold

	if slices.Contains(until, "session") {
		_, claim := mem.ownSeat(ctx)
		untilHold = &hold{Seat: mem.seatKey(name), Claimed: claim.Claimed}
	}

	var due time.Time

	if strings.TrimSpace(req.Due.text) != "" {
		parsed, err := parseDue(req.Due.text)
		if err != nil {
			return nil, "", err
		}

		due = parsed.UTC()
	}

	var (
		changes []itemPayload
		ids     []string
	)

	taken := func(id string) bool {
		return id == oldLine || held.line(id) != nil || slices.ContainsFunc(changes, func(one itemPayload) bool { return one.ID == id })
	}

	for _, text := range req.Add {
		if strings.TrimSpace(text) == "" {
			continue
		}

		id, err := newItemID(taken)
		if err != nil {
			return nil, "", err
		}

		one := lineOf(text)
		one.ID, one.Until, one.UntilHashes, one.UntilHold, one.Session = id, until, hashes, untilHold, mem.env.SessionID
		one.Due = due
		changes = append(changes, one)
		ids = append(ids, ledgerID+"#"+id)
	}

	if len(changes) == 0 {
		return nil, "", errors.New("--add needs a line: text, or a memory id, <todo>#<item> or graph id to pin")
	}

	events, err := itemEvents(ledgerID, name, mem.now(), changes)
	if err != nil {
		return nil, "", err
	}

	return events, withFlags("added "+strings.Join(ids, ", "), dueDeclared(due)), nil
}

// dueDeclared is the flag naming a line's due time in its normal form, the
// one a filing names: none where the line carries no due time.
func dueDeclared(due time.Time) []string {
	if due.IsZero() {
		return nil
	}

	return []string{"due: " + stampIn(due, time.Local)}
}

// lineDueFlag is how a read says when a line comes due.
func (mem *memory) lineDueFlag(line ledgerLine) string {
	if line.Due.IsZero() {
		return ""
	}

	now := mem.now()
	if line.Due.After(now) {
		return "due in " + age(line.Due.Sub(now))
	}

	return "due " + age(now.Sub(line.Due)) + " ago"
}

// dueLater is whether an open agent line comes due more than a day from
// now: the brief holds it back until it nears, as it does a due entry.
func (mem *memory) dueLater(line ledgerLine) bool {
	return !line.Due.IsZero() && line.Due.Sub(mem.now()) > dueNear
}

// lineOf is a line as --add gives it: a memory id, a todo item's address or
// a graph id pins its target, any other text is the line.
func lineOf(text string) itemPayload {
	text = strings.TrimSpace(text)
	if itemAddress.MatchString(text) {
		return itemPayload{Op: itemAdd, Link: text}
	}

	return itemOf(text)
}

func (mem *memory) changeLines(folded *state, held *ledger, name string, req request) ([]memorylog.Event, string, error) {
	op := itemCheck

	switch {
	case req.Undo:
		op = itemUncheck
	case req.Remove:
		op = itemRemove
	}

	var (
		changes  []itemPayload
		failures []string
	)

	for _, address := range slices.Compact(slices.Clone(req.IDs)) {
		parts := stateAddress.FindStringSubmatch(address)

		switch {
		case parts == nil:
			failures = append(failures, address+" is no line address; give state#<item>")
		case parts[1] == oldLine:
			_, pending := folded.oldState(name)
			if pending == req.Undo {
				failures = append(failures, address+": no such line")

				continue
			}

			changes = append(changes, itemPayload{Op: op, ID: oldLine})
		default:
			line := held.line(parts[1])
			if line == nil || (line.State == itemRemoved && !req.Undo) {
				failures = append(failures, address+": no such line")

				continue
			}

			changes = append(changes, itemPayload{Op: op, ID: parts[1]})
		}
	}

	if len(failures) > 0 {
		return nil, "", fmt.Errorf("nothing acked:\n  %s", strings.Join(failures, "\n  "))
	}

	events, err := itemEvents(ledgerID, name, mem.now(), changes)
	if err != nil {
		return nil, "", err
	}

	return events, map[string]string{itemCheck: "done", itemUncheck: "undone", itemRemove: "removed"}[op], nil
}

// lineMark is a line as every read opens it: its id, marked where a hook
// added it, and its text.
func lineMark(view lineView) string {
	if view.line.Hook != "" {
		return fmt.Sprintf("#%s hook: %s", view.line.ID, hookio.NeutralizeInline(view.text))
	}

	return fmt.Sprintf("#%s: %s", view.line.ID, hookio.NeutralizeInline(view.text))
}

// stateSection is how the brief opens on the seat's ledger: the old slot
// waiting to be split, every open agent line whole with the commits a landed
// round matched to it, the one dirty and the one asked line, and the newest
// landed and captured lines, the rest folded into a count, then the size
// nudge where the seat's open lines stand above its high mark.
func (mem *memory) stateSection(ctx context.Context, folded *state, seatName string) []string {
	views := mem.ledgerViews(ctx, folded, seatName)
	old, pending := folded.oldState(seatName)

	var open []lineView

	for _, view := range views {
		if view.open() {
			open = append(open, view)
		}
	}

	if len(open) == 0 && !pending {
		return []string{"state: empty"}
	}

	at := mem.hintChannel(ctx)
	lines := []string{"state (" + hintChoice(at, settleHint(ledgerID+"#<item>", "done"),
		settleHint(ledgerID+"#<item>", "remove")) + "):"}

	if pending {
		lines = append(lines, mem.oldLines(at, old)...)
	}

	nested := mem.ledgerLanded(folded, seatName)

	var commits []string
	for _, mapping := range nested {
		commits = append(commits, mapping.Commit)
	}

	var current, history, later []lineView

	for _, view := range open {
		switch {
		case historyLine(view.line):
			if view.line.Hook == hookLanded && slices.ContainsFunc(commits, func(sha string) bool { return sameCommit(sha, view.line.Commit) }) {
				continue
			}

			history = append(history, view)
		case view.line.Hook != "":
			current = append(current, view)
		case mem.dueLater(view.line):
			later = append(later, view)
		default:
			lines = append(lines, "  "+withFlag(withFlag(lineMark(view), view.flag), mem.lineDueFlag(view.line)))

			for _, mapping := range nested {
				if strings.HasSuffix(mapping.Target, "#"+view.line.ID) {
					lines = append(lines, fmt.Sprintf("    landed %s %s", shortCommit(mapping.Commit), hookio.NeutralizeInline(mapping.Subject)))
				}
			}
		}
	}

	if len(later) > 0 {
		next := slices.MinFunc(later, func(one, other lineView) int { return one.line.Due.Compare(other.line.Due) })
		lines = append(lines, fmt.Sprintf("  (%d due later, next %s: %s)", len(later), stamp(next.line.Due), showHint(ledgerID).in(at)))
	}

	for _, view := range current {
		lines = append(lines, fmt.Sprintf("  %s (%s)", lineMark(view), age(mem.now().Sub(view.line.Added))))
	}

	slices.Reverse(history)

	for index, view := range history {
		if index == briefHookLines {
			lines = append(lines, fmt.Sprintf("  (%d more: %s)", len(history)-briefHookLines, showHint(ledgerID, "with_history").in(at)))

			break
		}

		lines = append(lines, fmt.Sprintf("  %s (%s)", lineMark(view), age(mem.now().Sub(view.line.Added))))
	}

	return append(lines, mem.briefPressure(ctx, views, seatName)...)
}

func withFlag(text string, flag string) string {
	if flag == "" {
		return text
	}

	return text + "  " + flag
}

func sameCommit(one string, other string) bool {
	if one == "" || other == "" {
		return false
	}

	return strings.HasPrefix(one, other) || strings.HasPrefix(other, one)
}

// oldLines is the slot the seat wrote before the ledger, as item state#old,
// with what to do with it.
func (mem *memory) oldLines(at channel, old stateSlot) []string {
	where := age(mem.now().Sub(old.at)) + " old"
	if old.Branch != "" {
		where += ", branch " + old.Branch
	}

	return []string{fmt.Sprintf("  #old (%s): %s", where, old.Body), "  " + oldStateAsk(at)}
}

// oldStateNotice is the old slot's request alone, for a resumed session,
// whose transcript holds its last brief; empty once it was split.
func (mem *memory) oldStateNotice(ctx context.Context, seatName string) string {
	folded, err := mem.load(ctx)
	if err != nil {
		return ""
	}

	old, pending := folded.oldState(seatName)
	if !pending {
		return ""
	}

	return strings.Join(append([]string{self + ": state#old waits to be split:"}, mem.oldLines(mem.hintChannel(ctx), old)...), "\n")
}

type ledgerEnds struct {
	Shown []string `json:"shown"`
}

func (mem *memory) ledgerEndsKey(seatName string) string {
	return "ledger-ended/" + mem.seatKey(seatName)
}

// endedLines is every agent line that ended on its own since the seat's
// last brief, each named once; a line that comes back open is named again
// should it end again.
func (mem *memory) endedLines(ctx context.Context, folded *state, seatName string) []string {
	var (
		marked ledgerEnds
		kept   []string
		lines  []string
	)

	mem.readSlot(mem.ledgerEndsKey(seatName), &marked)

	views := mem.ledgerViews(ctx, folded, seatName)

	for _, view := range views {
		if view.state != lineEnded || view.line.Hook != "" {
			continue
		}

		kept = append(kept, view.line.ID)

		if !slices.Contains(marked.Shown, view.line.ID) {
			lines = append(lines, fmt.Sprintf("  state#%s %s: %s (%s)", view.line.ID, hookio.NeutralizeInline(view.text), hookio.NeutralizeInline(view.reason), settleHint(ledgerID+"#"+view.line.ID, "undo").in(mem.hintChannel(ctx))))
		}
	}

	if !slices.Equal(kept, marked.Shown) {
		if len(kept) == 0 {
			mem.clearSlot(mem.ledgerEndsKey(seatName))
		} else {
			mem.writeSlot(mem.ledgerEndsKey(seatName), &ledgerEnds{Shown: kept})
		}
	}

	// Lines that ended on their own settle work as a tick does, counted
	// once, when the brief first names them.
	if len(lines) > 0 {
		mem.recordLedger(ctx, seatName, len(openToSettle(views)), true)
	}

	return lines
}

// stateShown is which of show state's folded groups it prints: every landed
// and captured line, every other ticked or ended line, and removed lines; and
// the seat --seat named, which every fold hint names again, empty for the
// caller's own.
type stateShown struct {
	history, settled, removed bool
	seat                      string
}

func stateShownOf(req request) stateShown {
	shown := stateShown{history: req.WithHistory, settled: req.AllItems, removed: req.WithRemoved}
	if len(req.Seats) > 0 {
		shown.seat = req.Seats[0]
	}

	return shown
}

// foldHint is the show state call printing one fold, naming the seat read.
func (shown stateShown) foldHint(flag string) hintCall {
	call := showHint(ledgerID, flag)
	if shown.seat != "" {
		call.args = append(call.args, hintFlag("seat", shown.seat))
	}

	return call
}

// ledgerSeat is the seat whose ledger show state reads: the one --seat names,
// read-only and whether or not the caller holds a seat, else the caller's
// own; empty with why where there is none.
func (mem *memory) ledgerSeat(ctx context.Context, req request) (string, string) {
	if len(req.Seats) == 0 {
		name, isSeated := mem.seated(ctx)
		if !isSeated {
			return "", noSeatText(mem.hintChannel(ctx))
		}

		return name, ""
	}

	name := req.Seats[0]

	var names []string
	for _, key := range seat.Known(mem.seatsDir()) {
		names = append(names, mem.seatName(key))
	}

	if slices.Contains(names, name) {
		return name, ""
	}

	if len(names) == 0 {
		return "", fmt.Sprintf("%v: %s; no seat in this project", ErrUnknownSeat, name)
	}

	slices.Sort(names)

	return "", fmt.Sprintf("%v: %s; this project's seats: %s", ErrUnknownSeat, name, strings.Join(names, ", "))
}

// showSeatRefusal is why show's --seat cannot be read as given: more than one
// seat, or no state id to read the seat's ledger by.
func showSeatRefusal(req request) error {
	switch {
	case len(req.Seats) > 1:
		return ErrShowSeats
	case len(req.Seats) == 1 && !slices.Contains(req.IDs, ledgerID):
		return ErrSeatNoState
	}

	return nil
}

// stateRead is what show state prints of the ledger, agent lines first, then
// the other hook lines, then the landed and captured ones, each run in the
// order added, and how many lines each fold holds back.
type stateRead struct {
	views            []lineView
	history, settled int
}

// stateLines is the ledger as show state reads it: every live line and the
// newest open landed and captured ones, the rest counted into the fold whose
// flag prints them.
func (mem *memory) stateLines(ctx context.Context, folded *state, seatName string, shown stateShown) stateRead {
	held := folded.ledgerFor(seatName)
	probe := mem.probe(ctx)

	var (
		read    stateRead
		agent   []lineView
		hooks   []lineView
		shipped []lineView
	)

	for _, line := range held.Lines {
		if line.State == itemRemoved && !shown.removed {
			continue
		}

		view := mem.viewLine(ctx, probe, folded, seatName, held, line)

		switch {
		case line.State == itemRemoved:
		case historyLine(line):
			if !shown.history && (!view.open() || held.newerHookLines(line.ID) >= briefHookLines) {
				read.history++

				continue
			}
		case !view.open() && !shown.settled:
			read.settled++

			continue
		}

		switch {
		case historyLine(line):
			shipped = append(shipped, view)
		case line.Hook != "":
			hooks = append(hooks, view)
		default:
			agent = append(agent, view)
		}
	}

	read.views = slices.Concat(agent, hooks, shipped)

	return read
}

// showState is the ledger's live lines, each with its state and every hook
// line's age, and a fold line per group held back naming the flag that
// prints it.
func (mem *memory) showState(ctx context.Context, folded *state, seatName string, shown stateShown) string {
	var lines []string

	if old, pending := folded.oldState(seatName); pending {
		oldLines := mem.oldLines(mem.hintChannel(ctx), old)
		// Splitting the old slot is its own seat's work: another seat reads
		// the slot without the request.
		if shown.seat != "" {
			oldLines = oldLines[:1]
		}

		lines = append(lines, oldLines...)
	}

	read := mem.stateLines(ctx, folded, seatName, shown)
	nested := mem.ledgerLanded(folded, seatName)

	for _, view := range read.views {
		box := map[string]string{itemOpen: "[ ]", itemDone: "[x]", lineEnded: "[e]", itemRemoved: "[r]"}[view.state]

		text := fmt.Sprintf("  %s %s", box, withFlag(lineMark(view), view.flag))
		if view.line.Hook != "" {
			text += fmt.Sprintf(" (%s)", age(mem.now().Sub(view.line.Added)))
		}

		if len(view.line.Until) > 0 {
			text += "  until " + strings.Join(view.line.Until, ", ")
		}

		if view.open() {
			text = withFlag(text, mem.lineDueFlag(view.line))
		}

		lines = append(lines, text)

		if view.reason != "" {
			lines = append(lines, "    ended: "+view.reason)
		}

		for _, mapping := range nested {
			if strings.HasSuffix(mapping.Target, "#"+view.line.ID) {
				lines = append(lines, fmt.Sprintf("    landed %s %s", shortCommit(mapping.Commit), mapping.Subject))
			}
		}
	}

	if read.history > 0 {
		lines = append(lines, fmt.Sprintf("  (%d older landed and captured: %s)", read.history, shown.foldHint("with_history").in(mem.hintChannel(ctx))))
	}

	if read.settled > 0 {
		lines = append(lines, fmt.Sprintf("  (%d settled: %s)", read.settled, shown.foldHint("all_items").in(mem.hintChannel(ctx))))
	}

	if len(lines) == 0 {
		return "state: empty"
	}

	return strings.Join(append([]string{"state of seat " + seatName + ":"}, lines...), "\n")
}

// ledgerTarget is the address a landed round matches an agent line by.
func ledgerTarget(seatName string, id string) string {
	return ledgerID + ":" + seatName + "#" + id
}

func splitLedgerTarget(target string) (string, string, bool) {
	rest, isLedger := strings.CutPrefix(target, ledgerID+":")
	if !isLedger {
		return "", "", false
	}

	seatName, id, found := strings.Cut(rest, "#")

	return seatName, id, found
}

// ledgerLanded is the landed-commit matches standing under the seat's open
// agent lines: evidence, never a tick, and gone once the line is.
func (mem *memory) ledgerLanded(folded *state, seatName string) []landedMapping {
	var held landedSet
	if !mem.readSlot(landedKey, &held) {
		return nil
	}

	var own []landedMapping

	for _, mapping := range pendingMappings(folded, held.Mappings) {
		owner, _, isLedger := splitLedgerTarget(mapping.Target)
		if isLedger && owner == seatName {
			own = append(own, mapping)
		}
	}

	return own
}

// ledgerTargets is every open agent line of every seat, each with what a
// commit is matched by.
func ledgerTargets(folded *state) []landedTarget {
	var targets []landedTarget

	for seatName, held := range folded.ledgers {
		for _, line := range held.Lines {
			if line.Hook != "" || line.State != itemOpen {
				continue
			}

			target := landedTarget{address: ledgerTarget(seatName, line.ID), floor: line.Added, text: line.Text}
			if graphLink(line.Link) {
				target.links = []string{line.Link}
			}

			targets = append(targets, target)
		}
	}

	slices.SortFunc(targets, func(one, other landedTarget) int { return strings.Compare(one.address, other.address) })

	return targets
}

// ledgerLineOpen is whether a landed match's line still stands open.
func ledgerLineOpen(folded *state, target string) bool {
	seatName, id, _ := splitLedgerTarget(target)
	line := folded.ledgerFor(seatName).line(id)

	return line != nil && line.State == itemOpen && line.Hook == ""
}

// hookLines adds the lines git and the graph record for the seat: a landed
// line per commit since the claim the ledger lacks, and at Stop the files
// the session wrote that no commit holds and its last prompt. It prints
// nothing.
func (mem *memory) hookLines(ctx context.Context, seatName string, since string, session string, atStop bool) {
	folded, err := mem.load(ctx)
	if err != nil {
		return
	}

	held := folded.ledgerFor(seatName)
	here := mem.checkout(ctx)

	changes := mem.landedChanges(ctx, here, held, since)

	if atStop {
		steps := mem.loadLadder(session)
		claimed := mem.claimedAt(mem.seatKey(seatName))
		changes = append(changes, mem.dirtyChange(ctx, here, held, steps.Wrote, claimed)...)
		changes = append(changes, mem.askedChange(held, steps.LastPrompt, claimed)...)
	}

	mem.addHookChanges(ctx, held, seatName, changes)
}

func (mem *memory) addHookChanges(ctx context.Context, held *ledger, seatName string, changes []itemPayload) {
	if len(changes) == 0 {
		return
	}

	var drawn []string

	for index := range changes {
		if changes[index].Op != itemAdd {
			continue
		}

		id, err := newItemID(func(id string) bool { return id == oldLine || held.line(id) != nil || slices.Contains(drawn, id) })
		if err != nil {
			return
		}

		drawn = append(drawn, id)
		changes[index].ID = id
	}

	events, err := itemEvents(ledgerID, seatName, mem.now(), changes)
	if err != nil {
		return
	}

	_ = mem.appendEvents(ctx, events...)
}

// landedChanges is a landed line for every commit since the claim's that no
// line of the ledger records, oldest first.
func (mem *memory) landedChanges(ctx context.Context, here tree, held *ledger, since string) []itemPayload {
	if since == "" || here.commit == "" || sameCommit(since, here.commit) {
		return nil
	}

	out, err := mem.env.Git.Output(ctx, here.root, "log", "--oneline", "-n", fmt.Sprint(landedLines), since+"..HEAD")
	if err != nil {
		return nil
	}

	var changes []itemPayload

	lines := strings.Split(strings.TrimSpace(out), "\n")
	slices.Reverse(lines)

	for _, line := range lines {
		sha, subject, _ := strings.Cut(strings.TrimSpace(line), " ")
		if sha == "" || slices.ContainsFunc(held.Lines, func(kept ledgerLine) bool { return sameCommit(kept.Commit, sha) }) {
			continue
		}

		changes = append(changes, itemPayload{
			Op: itemAdd, Hook: hookLanded, Commit: sha, Text: strings.TrimSpace("landed " + sha + " " + subject),
		})
	}

	return changes
}

// dirtyChange keeps the ledger's one dirty line of the claim current: the
// files the session's writes named, and those the line named before, that
// git still reports changed; ticked once every one is committed or reverted.
func (mem *memory) dirtyChange(ctx context.Context, here tree, held *ledger, wrote []string, claimed time.Time) []itemPayload {
	current := held.openHook(hookDirty, claimed)

	var candidates []string

	if current != nil {
		candidates = slices.Clone(current.Paths)
	}

	for _, path := range wrote {
		relative, err := filepath.Rel(here.root, path)
		if err != nil || strings.HasPrefix(relative, "..") || filepath.IsAbs(relative) {
			continue
		}

		relative = filepath.ToSlash(relative)
		if !slices.Contains(candidates, relative) {
			candidates = append(candidates, relative)
		}
	}

	if len(candidates) == 0 || here.root == "" {
		return nil
	}

	args := append([]string{"status", "--porcelain", "-z", "--untracked-files=all", "--"}, candidates...)

	out, err := mem.env.Git.Output(ctx, here.root, args...)
	if err != nil {
		return nil
	}

	var dirty []string

	for _, record := range strings.Split(out, "\x00") {
		if len(record) < 4 {
			continue
		}

		path := record[3:]
		if slices.Contains(candidates, path) && !slices.Contains(dirty, path) {
			dirty = append(dirty, path)
		}
	}

	slices.SortStableFunc(dirty, func(one, other string) int {
		return slices.Index(candidates, one) - slices.Index(candidates, other)
	})

	switch {
	case len(dirty) == 0 && current != nil:
		return []itemPayload{{Op: itemCheck, ID: current.ID, Hook: hookDirty}}
	case len(dirty) == 0:
		return nil
	}

	text := "dirty " + strings.Join(dirty, ", ")

	switch {
	case current == nil:
		return []itemPayload{{Op: itemAdd, Hook: hookDirty, Text: text, Paths: dirty}}
	case current.Text != text:
		return []itemPayload{{Op: itemRetext, ID: current.ID, Hook: hookDirty, Text: text, Paths: dirty}}
	}

	return nil
}

// askedChange keeps the ledger's one asked line of the claim on the
// session's last prompt, which the prompt hook never takes where it holds a
// credential.
func (mem *memory) askedChange(held *ledger, prompt string, claimed time.Time) []itemPayload {
	prompt = strings.Join(strings.Fields(prompt), " ")
	if prompt == "" {
		return nil
	}

	text := cutWords("asked "+prompt, mem.options().EntryTargetWords)
	current := held.openHook(hookAsked, claimed)

	switch {
	case current == nil:
		return []itemPayload{{Op: itemAdd, Hook: hookAsked, Text: text}}
	case current.Text != text:
		return []itemPayload{{Op: itemRetext, ID: current.ID, Hook: hookAsked, Text: text}}
	}

	return nil
}

// wrotePaths adds path to the files a session wrote, each once, the first
// writtenFiles of them.
func wrotePaths(wrote []string, path string) []string {
	if path == "" || slices.Contains(wrote, path) || len(wrote) >= writtenFiles {
		return wrote
	}

	return append(wrote, path)
}

// cutWords keeps text within limit words, as words counts them, dropping
// whole words from where it overflows on.
func cutWords(text string, limit int) string {
	if limit <= 0 || words(text) <= limit {
		return text
	}

	var kept []string

	for _, line := range strings.Split(text, "\n") {
		if words(strings.Join(append(slices.Clone(kept), line), "\n")) <= limit {
			kept = append(kept, line)

			continue
		}

		fields := strings.Fields(line)
		for len(fields) > 0 && words(strings.Join(append(slices.Clone(kept), strings.Join(fields, " ")), "\n")) > limit {
			fields = fields[:len(fields)-1]
		}

		if len(fields) > 0 {
			kept = append(kept, strings.Join(fields, " "))
		}

		break
	}

	return strings.Join(kept, "\n")
}

// unsettled is every landed or captured line a hook added after since that
// the agent has not settled: ticked, dropped, or cited by a line of its own.
// The hook saw that work happen, so settling each line proves the agent
// looked at each piece of it, with no text matched against what it did.
func (held *ledger) unsettled(since time.Time) []ledgerLine {
	var open []ledgerLine

	for _, line := range held.Lines {
		if (line.Hook != hookLanded && line.Hook != hookCaptured) || !line.Added.After(since) || line.State != itemOpen {
			continue
		}

		if !slices.ContainsFunc(held.Lines, func(own ledgerLine) bool {
			return own.Hook == "" && own.State != itemRemoved && cites(own, line)
		}) {
			open = append(open, line)
		}
	}

	return open
}

// cites is whether an agent's line names a hook line: its address, the
// commit a landed line records, or the graph entry a captured one does.
func cites(own ledgerLine, hook ledgerLine) bool {
	for _, address := range citedAddress.FindAllStringSubmatch(own.Text, -1) {
		if address[1] == hook.ID {
			return true
		}
	}

	switch hook.Hook {
	case hookLanded:
		return slices.ContainsFunc(citedCommit.FindAllString(own.Text, -1), func(sha string) bool {
			return sameCommit(sha, hook.Commit)
		})
	case hookCaptured:
		return own.Link == hook.Link || strings.Contains(own.Text, hook.Link)
	}

	return false
}

// openHook is the open line of the hook added since the claim: one added
// before it ended with it.
func (held *ledger) openHook(hook string, claimed time.Time) *ledgerLine {
	for index := range held.Lines {
		if held.Lines[index].Hook == hook && held.Lines[index].State == itemOpen && !held.Lines[index].Added.Before(claimed) {
			return &held.Lines[index]
		}
	}

	return nil
}

// capturedLines adds a captured line for every graph entry gained the
// ledger lacks, with its title.
func (mem *memory) capturedLines(ctx context.Context, seatName string, gained []string) {
	if len(gained) == 0 {
		return
	}

	folded, err := mem.load(ctx)
	if err != nil {
		return
	}

	held := folded.ledgerFor(seatName)

	var changes []itemPayload

	for _, id := range gained {
		text := strings.TrimSpace("captured " + id + " " + graphTitle(mem.graphPath(ctx, id)))
		changes = append(changes, itemPayload{Op: itemAdd, Hook: hookCaptured, Link: id, Text: text})
	}

	mem.addHookChanges(ctx, held, seatName, changes)
}

// graphTitle is the title a graph entry's front matter names, empty where
// the file holds none.
func graphTitle(path string) string {
	if path == "" {
		return ""
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	lines := strings.Split(string(raw), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}

	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			break
		}

		title, found := strings.CutPrefix(line, "title:")
		if found {
			return strings.Trim(strings.TrimSpace(title), `"'`)
		}
	}

	return ""
}

// jsonLedger is the ledger as brief --json carries it, and as show --json
// carries the lines show state prints, with what each fold holds back.
type jsonLedger struct {
	Lines  []jsonLine  `json:"lines"`
	Old    *jsonState  `json:"old,omitempty"`
	Folded *jsonFolded `json:"folded,omitempty"`
}

// jsonFolded is how many lines show state holds back: History the landed and
// captured lines with_history adds, Settled the other ticked and ended lines
// all_items adds.
type jsonFolded struct {
	History int `json:"history,omitempty"`
	Settled int `json:"settled,omitempty"`
}

type jsonLine struct {
	ID     string    `json:"id"`
	Text   string    `json:"text"`
	Link   string    `json:"link,omitempty"`
	State  string    `json:"state"`
	Reason string    `json:"reason,omitempty"`
	Hook   string    `json:"hook,omitempty"`
	At     time.Time `json:"at"`
	Until  []string  `json:"until,omitempty"`
	Due    time.Time `json:"due,omitzero"`
}

func (mem *memory) ledgerJSON(ctx context.Context, folded *state, seatName string) *jsonLedger {
	return mem.linesJSON(folded, seatName, mem.ledgerViews(ctx, folded, seatName))
}

// stateJSON is show state as show --json carries it: the lines the text
// prints and the folds' counts.
func (mem *memory) stateJSON(ctx context.Context, folded *state, seatName string, shown stateShown) *jsonLedger {
	read := mem.stateLines(ctx, folded, seatName, shown)
	answer := mem.linesJSON(folded, seatName, read.views)

	if read.history > 0 || read.settled > 0 {
		answer.Folded = &jsonFolded{History: read.history, Settled: read.settled}
	}

	return answer
}

func (mem *memory) linesJSON(folded *state, seatName string, views []lineView) *jsonLedger {
	answer := &jsonLedger{Lines: []jsonLine{}}

	for _, view := range views {
		answer.Lines = append(answer.Lines, jsonLine{
			ID: view.line.ID, Text: view.text, Link: view.line.Link, State: view.state, Reason: view.reason,
			Hook: view.line.Hook, At: view.line.Added.UTC(), Until: view.line.Until, Due: view.line.Due.UTC(),
		})
	}

	if old, pending := folded.oldState(seatName); pending {
		answer.Old = &jsonState{Body: old.Body, At: old.at.UTC(), Branch: old.Branch, Commit: old.Commit}
	}

	return answer
}

// refuseState answers the slot's old command: the ledger is changed a line
// at a time.
func (mem *memory) refuseState(_ context.Context, _ request) result {
	return refused("%s", stateRefusal)
}
