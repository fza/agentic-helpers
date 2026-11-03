package memory

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
)

// Every kind of value the slot file holds is a slots.Fitted: a ceiling in
// encoded JSON bytes, its caps, and the steps that shrink it to a minimal form
// that always fits. The ceilings of each kind, every one within
// slots.MaxValue:
const (
	ladderCeiling       = 11_000
	shownCeiling        = 32_768
	spawnCeiling        = 33_000
	agentsCeiling       = 110_000
	instructionsCeiling = 65_536
	graphMarkCeiling    = 2_200
	trapsCeiling        = 65_536
	doNowCeiling        = 65_536
	seenCeiling         = 65_536
	ledgerEndsCeiling   = 65_536
	landedCeiling       = 120_000
	branchTipsCeiling   = 95_000
	fixedCeiling        = 200
)

// The caps within each kind, in encoded JSON bytes unless they count items.
const (
	promptBytes      = 3_500
	wroteBytes       = 3_500
	owedBytes        = 3_500
	agentIDBytes     = 64
	agentSeatBytes   = 64
	agentRecords     = 800
	pathBytes        = 1_024
	triggerBytes     = 512
	itemTitleBytes   = 512
	itemTriggerBytes = 256
	proofTriggers    = 4
	proofBytes       = 128
	sessionBytes     = 130
	mappingTarget    = 64
	mappingText      = 200
	landedMappings   = 300
	branchBytes      = 255
	branchTipCount   = 300
)

// cutLines keeps the whole lines of text that fit limit, a last line
// counting the ones dropped; a first line too long alone is cut.
func cutLines(text string, limit int) string {
	if slots.EncodedLen(text) <= limit {
		return text
	}

	lines := strings.Split(text, "\n")

	// A line's quotes stand for the line break joining it to the next, so the
	// lines and the count line take the sum of their own JSON.
	room := limit - slots.EncodedLen(fmt.Sprintf("  +%d more", len(lines)))
	kept := 0

	for used := 0; kept < len(lines)-1; kept++ {
		used += slots.EncodedLen(lines[kept])
		if used > room {
			break
		}
	}

	for ; kept > 0; kept-- {
		cut := strings.Join(append(slices.Clone(lines[:kept]), fmt.Sprintf("  +%d more", len(lines)-kept)), "\n")
		if slots.EncodedLen(cut) <= limit {
			return cut
		}
	}

	return slots.CutJSON(lines[0], limit)
}

// keptWithin is the first count items of list, each within each bytes, that
// sum to at most total: an item past either is left out.
func keptWithin(list []string, count int, each int, total int) []string {
	var kept []string

	used := 2

	for _, item := range list {
		size := slots.EncodedLen(item)
		if len(kept) == count || size > each || used+size+1 > total {
			continue
		}

		kept = append(kept, item)
		used += size + 1
	}

	return kept
}

func (held *ladder) Ceiling() int { return ladderCeiling }

func (held *ladder) Limit() {
	held.LastPrompt = slots.CutJSON(held.LastPrompt, promptBytes)
	held.Wrote = keptWithin(held.Wrote, writtenFiles, wroteBytes, wroteBytes)
	held.Owed = cutLines(held.Owed, owedBytes)
}

// Shrink has nothing to give up: the caps alone hold the ladder within its
// ceiling.
func (held *ladder) Shrink() bool { return false }

func (shown *shownSet) Ceiling() int { return shownCeiling }

func (shown *shownSet) Limit() {}

// Shrink forgets the older half of each mark list: those entries print in
// full again.
func (shown *shownSet) Shrink() bool {
	if len(shown.IDs)+len(shown.Bodies)+len(shown.Traps)+len(shown.Notes)+len(shown.Landed) == 0 {
		return false
	}

	shown.IDs = slots.NewerHalf(shown.IDs)
	shown.Bodies = slots.NewerHalf(shown.Bodies)
	shown.Traps = slots.NewerHalf(shown.Traps)
	shown.Notes = slots.NewerHalf(shown.Notes)
	shown.Landed = slots.NewerHalf(shown.Landed)

	return true
}

func (spawned *forkSpawn) Ceiling() int { return spawnCeiling }

func (spawned *forkSpawn) Limit() {
	if slots.EncodedLen(spawned.Seat) > agentSeatBytes {
		spawned.Seat = ""
	}

	if slots.EncodedLen(spawned.Level) > agentIDBytes {
		spawned.Level = levelNone.String()
	}
}

func (spawned *forkSpawn) Shrink() bool { return spawned.Shown.Shrink() }

func (table *subagentTable) Ceiling() int { return agentsCeiling }

// Limit leaves out a record whose id, level or seat key passes its cap, and
// every record past agentRecords, the ids in order.
func (table *subagentTable) Limit() {
	for _, id := range slices.Sorted(maps.Keys(table.Agents)) {
		record := table.Agents[id]
		if slots.EncodedLen(id) > agentIDBytes || slots.EncodedLen(record.Seat) > agentSeatBytes ||
			slots.EncodedLen(record.Level) > agentIDBytes {
			delete(table.Agents, id)
		}
	}

	for _, id := range slices.Sorted(maps.Keys(table.Agents))[min(len(table.Agents), agentRecords):] {
		delete(table.Agents, id)
	}
}

// Shrink forgets half the records: a forgotten subagent's level is learnt
// again from its transcript.
func (table *subagentTable) Shrink() bool {
	ids := slices.Sorted(maps.Keys(table.Agents))
	if len(ids) == 0 {
		return false
	}

	for _, id := range ids[:len(ids)/2+len(ids)%2] {
		delete(table.Agents, id)
	}

	return true
}

func (loaded *loadedInstructions) Ceiling() int { return instructionsCeiling }

// Limit leaves a file whose path passes its cap unwatched.
func (loaded *loadedInstructions) Limit() {
	for path := range loaded.Files {
		if slots.EncodedLen(path) > pathBytes {
			delete(loaded.Files, path)
		}
	}
}

// Shrink stops watching the later half of the files, by path.
func (loaded *loadedInstructions) Shrink() bool {
	paths := slices.Sorted(maps.Keys(loaded.Files))
	if len(paths) == 0 {
		return false
	}

	for _, path := range paths[len(paths)/2:] {
		delete(loaded.Files, path)
	}

	return true
}

func (mark *graphMark) Ceiling() int { return graphMarkCeiling }

func (mark *graphMark) Limit() {}

// Shrink marks the month as holding no graph: the session hears of no capture
// until the month turns. A directory within pathBytes and a part's names
// stay within the ceiling (TestAMarkOfAFullPartKeepsItsGraph).
func (mark *graphMark) Shrink() bool {
	if mark.Dir == noGraph && mark.Stamp == "" && mark.Seen == nil {
		return false
	}

	mark.Dir, mark.Stamp, mark.Seen = noGraph, "", nil

	return true
}

func (index *trapIndex) Ceiling() int { return trapsCeiling }

// Limit drops an anchor or a trigger past its cap.
func (index *trapIndex) Limit() {
	for position, trap := range index.Traps {
		index.Traps[position].Anchors = keptWithin(trap.Anchors, len(trap.Anchors), pathBytes, trapsCeiling)
		index.Traps[position].When = keptWithin(trap.When, len(trap.When), triggerBytes, trapsCeiling)
	}

	for position, note := range index.Notes {
		index.Notes[position].About = keptWithin(note.About, len(note.About), pathBytes, trapsCeiling)
	}
}

// Shrink replaces the index by its marker: the tool hook then matches the
// seat's traps against the log, slower and as correct.
func (index *trapIndex) Shrink() bool {
	if index.FromLog {
		return false
	}

	*index = trapIndex{FromLog: true}

	return true
}

func limitItems(items []doNowItem) []doNowItem {
	kept := make([]doNowItem, 0, len(items))

	for _, item := range items {
		if slots.EncodedLen(item.ID) > agentIDBytes || slots.EncodedLen(item.Trigger) > itemTriggerBytes {
			continue
		}

		item.Title = slots.CutJSON(item.Title, itemTitleBytes)
		item.Proof = keptWithin(item.Proof, proofTriggers, proofBytes, proofTriggers*(proofBytes+1)+2)
		kept = append(kept, item)
	}

	return kept
}

func (pending *doNowState) Ceiling() int { return doNowCeiling }

func (pending *doNowState) Limit() {
	if slots.EncodedLen(pending.Session) > sessionBytes {
		pending.Session = ""
	}

	pending.Items = limitItems(pending.Items)

	for trigger := range pending.Stops {
		if !slices.ContainsFunc(pending.Items, func(item doNowItem) bool { return item.Trigger == trigger }) {
			delete(pending.Stops, trigger)
		}
	}
}

// Shrink lets the later half of the items go: the brief still names every
// rule owed.
func (pending *doNowState) Shrink() bool {
	if len(pending.Items) == 0 {
		return false
	}

	pending.Items = pending.Items[:len(pending.Items)/2]

	return true
}

// skippedItems are the do-now items sessions left pending, for the seat's
// next list, each rule once.
type skippedItems []doNowItem

func (skipped *skippedItems) Ceiling() int { return doNowCeiling }

func (skipped *skippedItems) Limit() {
	seen := map[string]bool{}

	var unique []doNowItem

	for _, item := range limitItems(*skipped) {
		if !seen[item.ID] {
			seen[item.ID] = true
			unique = append(unique, item)
		}
	}

	*skipped = unique
}

func (skipped *skippedItems) Shrink() bool {
	if len(*skipped) == 0 {
		return false
	}

	*skipped = slots.NewerHalf(*skipped)

	return true
}

func (mark *seenMark) Ceiling() int { return seenCeiling }

func (mark *seenMark) Limit() {}

// Shrink forgets the older half of the entries told due: they are told again.
func (mark *seenMark) Shrink() bool {
	if len(mark.Due) == 0 {
		return false
	}

	mark.Due = slots.NewerHalf(mark.Due)

	return true
}

func (marked *ledgerEnds) Ceiling() int { return ledgerEndsCeiling }

func (marked *ledgerEnds) Limit() {}

func (marked *ledgerEnds) Shrink() bool {
	if len(marked.Shown) == 0 {
		return false
	}

	marked.Shown = slots.NewerHalf(marked.Shown)

	return true
}

func limitMapping(mapping landedMapping) (landedMapping, bool) {
	if slots.EncodedLen(mapping.Target) > mappingTarget {
		return mapping, false
	}

	mapping.Subject = slots.CutJSON(mapping.Subject, mappingText)
	mapping.Test = slots.CutJSON(mapping.Test, mappingText)

	return mapping, true
}

func (held *landedSet) Ceiling() int { return landedCeiling }

// Limit leaves out a mapping whose target passes its cap and every one past
// landedMappings; a round stops short of that cap (storeMappings).
func (held *landedSet) Limit() {
	kept := make([]landedMapping, 0, len(held.Mappings))

	for _, mapping := range held.Mappings {
		limited, keep := limitMapping(mapping)
		if keep && len(kept) < landedMappings {
			kept = append(kept, limited)
		}
	}

	held.Mappings = kept
}

func (held *landedSet) Shrink() bool {
	if len(held.Mappings) == 0 {
		return false
	}

	held.Mappings = held.Mappings[:len(held.Mappings)/2]

	return true
}

// branchTips is the tip a probe last saw of each watched branch.
type branchTips map[string]string

func (tips *branchTips) Ceiling() int { return branchTipsCeiling }

// Limit leaves out a branch whose name passes its cap, and every branch past
// branchTipCount, by name.
func (tips *branchTips) Limit() {
	for _, branch := range slices.Sorted(maps.Keys(*tips)) {
		if slots.EncodedLen(branch) > branchBytes {
			delete(*tips, branch)
		}
	}

	for _, branch := range slices.Sorted(maps.Keys(*tips))[min(len(*tips), branchTipCount):] {
		delete(*tips, branch)
	}
}

// Shrink has nothing to give up: the caps alone hold the tips within their
// ceiling.
func (tips *branchTips) Shrink() bool { return false }

// fixedValue is a kind whose value has a fixed shape well within its
// ceiling: nothing to cap, nothing to shrink. Decoded, it keeps the value as
// written.
type fixedValue struct{ value any }

func fixedMark(value any) slots.Fitted { return &fixedValue{value: value} }

func (fixed *fixedValue) Ceiling() int { return fixedCeiling }

func (fixed *fixedValue) Limit() {}

func (fixed *fixedValue) Shrink() bool { return false }

func (fixed *fixedValue) MarshalJSON() ([]byte, error) { return json.Marshal(fixed.value) }

func (fixed *fixedValue) UnmarshalJSON(data []byte) error {
	fixed.value = json.RawMessage(slices.Clone(data))

	return nil
}

// slotKinds is every kind of value the slot file holds, by its key's prefix,
// each with a fresh value to decode a held one into: the format step fits a
// value an earlier release wrote past its kind's ceiling.
var slotKinds = []struct {
	prefix string
	exact  bool
	fresh  func() slots.Fitted
}{
	{prefix: "ladder/", fresh: func() slots.Fitted { return &ladder{} }},
	{prefix: "shown/", fresh: func() slots.Fitted { return &shownSet{} }},
	{prefix: "spawn/", fresh: func() slots.Fitted { return &forkSpawn{} }},
	{prefix: "agents/", fresh: func() slots.Fitted { return &subagentTable{} }},
	{prefix: "instructions/", fresh: func() slots.Fitted { return &loadedInstructions{} }},
	{prefix: "graph-seen/", fresh: func() slots.Fitted { return &graphMark{} }},
	{prefix: "traps/", fresh: func() slots.Fitted { return &trapIndex{} }},
	{prefix: "do-now/", fresh: func() slots.Fitted { return &doNowState{} }},
	{prefix: "skipped/", fresh: func() slots.Fitted { return &skippedItems{} }},
	{prefix: "seen/", fresh: func() slots.Fitted { return &seenMark{} }},
	{prefix: "ledger-ended/", fresh: func() slots.Fitted { return &ledgerEnds{} }},
	{prefix: landedKey, exact: true, fresh: func() slots.Fitted { return &landedSet{} }},
	{prefix: branchTipsKey, exact: true, fresh: func() slots.Fitted { return &branchTips{} }},
	{prefix: "fork/", fresh: func() slots.Fitted { return &fixedValue{} }},
	{prefix: "waiter/", fresh: func() slots.Fitted { return &fixedValue{} }},
	{prefix: "migration-failed/", fresh: func() slots.Fitted { return &fixedValue{} }},
	{prefix: "brief/", fresh: func() slots.Fitted { return &fixedValue{} }},
	{prefix: "landed-scan/", fresh: func() slots.Fitted { return &fixedValue{} }},
}

// refit is the value a format step keeps of slot: as held where it stays
// within its kind's ceiling, else fitted to it; a graph-names part keeps the
// names a part holds. Nothing is kept of a value too large that does not
// decode as its kind.
func refit(slot slots.Slot) ([]byte, bool) {
	if strings.HasPrefix(slot.Key, graphNamesPrefix) {
		return firstNames(slot.Value), true
	}

	for _, kind := range slotKinds {
		if kind.exact && slot.Key != kind.prefix || !kind.exact && !strings.HasPrefix(slot.Key, kind.prefix) {
			continue
		}

		value := kind.fresh()
		if len(slot.Value) <= value.Ceiling() {
			return slot.Value, true
		}

		err := json.Unmarshal(slot.Value, value)
		if err != nil {
			return nil, false
		}

		data, err := slots.Encode(value)

		return data, err == nil
	}

	return slot.Value, len(slot.Value) <= slots.MaxValue
}

// reportSlot reports a slot write that failed on stderr, which the agent
// never reads: a lost write costs session state, never the call.
func (mem *memory) reportSlot(action string, key string, err error) {
	if err != nil {
		_, _ = fmt.Fprintf(mem.streams.Err, "%s: %s %s: %v\n", self, action, key, err)
	}
}
