package memory

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
)

// The moments a rule may be owed at.
const (
	onClaim   = "claim"
	onCompact = "compact"
)

var doNowMoments = []string{onClaim, onCompact}

// stopsPerTrigger is how often Stop holds a turn for one trigger's pending
// items, so a list nobody can settle never holds a session for good.
const stopsPerTrigger = 2

// settlePayload is how a do-now item was settled: done by a proving tool call
// (Proof names the trigger), done by ack, or declined with a reason.
type settlePayload struct {
	How     string `json:"how"`
	Proof   string `json:"proof,omitempty"`
	Because string `json:"because,omitempty"`
	Session string `json:"session,omitempty"`
}

const (
	settledByProof = "proof"
	settledByAck   = "ack"
	settledDecline = "decline"
)

func (settled settlePayload) describe() string {
	switch settled.How {
	case settledByProof:
		return "done by proof " + settled.Proof
	case settledDecline:
		return "declined: " + settled.Because
	}

	return "done by ack"
}

// doNowItem is one rule owed at a moment, pending for the session from then.
type doNowItem struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Trigger string    `json:"trigger"`
	Proof   []string  `json:"proof,omitempty"`
	Since   time.Time `json:"since"`
}

// doNowState is a seat's pending do-now items: the session they are owed in,
// and how often Stop held a turn for each trigger. It sits beside the nudge
// ladder, keyed by the seat, since a claim run from a shell may not know its
// session yet.
type doNowState struct {
	Session string         `json:"session,omitempty"`
	Items   []doNowItem    `json:"items,omitempty"`
	Stops   map[string]int `json:"stops,omitempty"`
}

func (mem *memory) doNowPath(seatName string) string {
	return filepath.Join(mem.cacheDir(), "ladder", mem.seatKey(seatName)+".do-now.json")
}

func (mem *memory) skippedPath(seatName string) string {
	return filepath.Join(mem.cacheDir(), "briefs", mem.seatKey(seatName)+".skipped.json")
}

func (mem *memory) pendingDoNow(seatName string) doNowState {
	var pending doNowState
	readJSON(mem.doNowPath(seatName), &pending)

	return pending
}

func (mem *memory) savePending(seatName string, pending doNowState) {
	if len(pending.Items) == 0 {
		removeIfThere(mem.doNowPath(seatName))

		return
	}

	_ = writeJSON(mem.doNowPath(seatName), pending)
}

// skip records items left pending, for the seat's next do-now list.
func (mem *memory) skip(seatName string, items []doNowItem) {
	if len(items) == 0 {
		return
	}

	var skipped []doNowItem
	readJSON(mem.skippedPath(seatName), &skipped)

	_ = writeJSON(mem.skippedPath(seatName), append(skipped, items...))
}

func itemLine(number int, item doNowItem) string {
	if len(item.Proof) == 0 {
		return fmt.Sprintf("%d. %s %s  ack when done", number, item.ID, item.Title)
	}

	return fmt.Sprintf("%d. %s %s  proof: %s", number, item.ID, item.Title, strings.Join(item.Proof, ", "))
}

// doNow is the list owed at a moment, printed before the brief: what the
// seat's previous session left pending, then every live rule the seat's brief
// would show that is owed at this moment, numbered. Each item is pending for
// the session from now; an item a session before left pending counts as
// skipped.
func (mem *memory) doNow(folded *state, seatName string, moment string, session string) string {
	pending := mem.pendingDoNow(seatName)
	if pending.Session != "" && session != "" && pending.Session != session {
		mem.skip(seatName, pending.Items)
		pending = doNowState{}
	}

	pending.Session = cmp.Or(session, pending.Session)

	var lines []string

	var skipped []doNowItem
	if readJSON(mem.skippedPath(seatName), &skipped) && len(skipped) > 0 {
		lines = append(lines, "skipped last session:")

		for _, item := range skipped {
			lines = append(lines, fmt.Sprintf("  %s %s (%s)", item.ID, item.Title, item.Trigger))
		}

		removeIfThere(mem.skippedPath(seatName))
	}

	var owed []doNowItem

	for _, held := range mem.ownLive(folded, seatName, "") {
		if held.kind != kindRule || !slices.Contains(held.on, moment) {
			continue
		}

		owed = append(owed, doNowItem{ID: held.id, Title: held.title, Trigger: moment, Proof: held.proof, Since: mem.now()})
	}

	if len(owed) > 0 {
		lines = append(lines, fmt.Sprintf("do now (%s):", moment))

		for index, item := range owed {
			lines = append(lines, itemLine(index+1, item))
		}

		pending.Items = slices.DeleteFunc(pending.Items, func(item doNowItem) bool { return item.Trigger == moment })
		pending.Items = append(pending.Items, owed...)

		if pending.Stops != nil {
			delete(pending.Stops, moment)
		}
	}

	mem.savePending(seatName, pending)

	return strings.Join(lines, "\n")
}

// proveDoNow settles every pending item a proof of which the tool call is:
// one matching call after the moment the item became owed, in its session.
func (mem *memory) proveDoNow(ctx context.Context, seatName string, session string, call func() toolCall) {
	pending := mem.pendingDoNow(seatName)
	if len(pending.Items) == 0 || (pending.Session != "" && session != "" && pending.Session != session) {
		return
	}

	var events []memorylog.Event

	kept := pending.Items[:0:0]

	for _, item := range pending.Items {
		proof := ""

		for _, text := range item.Proof {
			if firstMatch([]string{text}, call()) != "" {
				proof = text

				break
			}
		}

		if proof == "" {
			kept = append(kept, item)

			continue
		}

		raw, err := json.Marshal(settlePayload{How: settledByProof, Proof: proof, Session: session})
		if err != nil {
			kept = append(kept, item)

			continue
		}

		events = append(events, memorylog.Event{Kind: memorylog.KindSettle, Entry: item.ID, Time: mem.now(), Seat: seatName, Payload: raw})
	}

	if len(events) == 0 || mem.appendEvents(ctx, events...) != nil {
		return
	}

	pending.Items = kept
	mem.savePending(seatName, pending)
}

func (mem *memory) ack(ctx context.Context, req request) result {
	name, held := mem.ownSeat(ctx)
	if name == "" {
		return refused("%s", ErrNoSeat)
	}

	because := strings.TrimSpace(req.Decline)
	if req.Done == (because != "") {
		return refused("ack takes exactly one of --done or --decline '<why>'")
	}

	session := cmp.Or(mem.env.SessionID, held.SessionID)
	pending := mem.pendingDoNow(name)

	if pending.Session != "" && session != "" && pending.Session != session {
		pending = doNowState{}
	}

	var unique, failures []string

	for _, id := range req.IDs {
		if slices.Contains(unique, id) {
			continue
		}

		unique = append(unique, id)

		if !slices.ContainsFunc(pending.Items, func(item doNowItem) bool { return item.ID == id }) {
			failures = append(failures, id+" is not pending in this session")
		}
	}

	if len(failures) > 0 {
		return refused("nothing acked:\n  %s", strings.Join(failures, "\n  "))
	}

	settled := settlePayload{How: settledByAck, Session: session}
	if because != "" {
		settled = settlePayload{How: settledDecline, Because: because, Session: session}
	}

	raw, err := json.Marshal(settled)
	if err != nil {
		return refused("encoding the ack: %v", err)
	}

	events := make([]memorylog.Event, 0, len(unique))
	for _, id := range unique {
		events = append(events, memorylog.Event{Kind: memorylog.KindSettle, Entry: id, Time: mem.now(), Seat: name, Payload: raw})
	}

	err = mem.appendEvents(ctx, events...)
	if err != nil {
		return refused("%v", err)
	}

	pending.Items = slices.DeleteFunc(pending.Items, func(item doNowItem) bool { return slices.Contains(unique, item.ID) })
	mem.savePending(name, pending)

	// A decline answers the rule without acting on it, so only a done ack is a
	// use that warms.
	if !req.NoHeat && because == "" {
		folded, err := mem.load(ctx)
		if err == nil {
			mem.warm(folded, heatLink, unique...)
		}
	}

	if because != "" {
		return done(fmt.Sprintf("declined %s: %s", strings.Join(unique, ", "), because)).
			with(ackFields{IDs: unique, Declined: because})
	}

	return done("done " + strings.Join(unique, ", ")).with(ackFields{IDs: unique})
}

// holdForDoNow is what Stop says while do-now items are pending, at most
// stopsPerTrigger times per trigger; past that, the items left are skipped.
func (mem *memory) holdForDoNow(seatName string, session string) string {
	pending := mem.pendingDoNow(seatName)
	if len(pending.Items) == 0 || (pending.Session != "" && session != "" && pending.Session != session) {
		return ""
	}

	if pending.Stops == nil {
		pending.Stops = map[string]int{}
	}

	var held, skipped []doNowItem

	for _, item := range pending.Items {
		if pending.Stops[item.Trigger] < stopsPerTrigger {
			held = append(held, item)
		} else {
			skipped = append(skipped, item)
		}
	}

	mem.skip(seatName, skipped)

	pending.Items = held
	counted := map[string]bool{}

	for _, item := range held {
		if !counted[item.Trigger] {
			counted[item.Trigger] = true
			pending.Stops[item.Trigger]++
		}
	}

	mem.savePending(seatName, pending)

	if len(held) == 0 {
		return ""
	}

	lines := []string{self + ": do-now items pending:"}
	for index, item := range held {
		lines = append(lines, itemLine(index+1, item))
	}

	return strings.Join(lines, "\n")
}

// skipAtEnd records every item still pending as the session ends.
func (mem *memory) skipAtEnd(seatName string) {
	pending := mem.pendingDoNow(seatName)
	mem.skip(seatName, pending.Items)
	removeIfThere(mem.doNowPath(seatName))
}
