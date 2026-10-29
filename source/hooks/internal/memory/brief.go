package memory

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/pieces"
	"github.com/fza/agentic-helpers/source/hooks/internal/space"
	"github.com/fza/agentic-helpers/source/hooks/internal/tools"
)

// briefBudget keeps the brief inside one hook output: Claude Code swaps a
// longer one for a file path, which a compaction summary then outranks.
const briefBudget = 9000

type budget struct {
	lines []string
	used  int
	limit int
	cut   int
}

func (held *budget) add(text string) bool {
	if held.used+len(text)+1 > held.limit {
		held.cut++

		return false
	}

	held.lines = append(held.lines, text)
	held.used += len(text) + 1

	return true
}

type briefMark struct {
	At time.Time `json:"at"`
}

func (mem *memory) previousBrief(seatName string) time.Time {
	var mark briefMark
	mem.readSlot(mem.briefMarkKey(seatName), &mark)

	return mark.At
}

func (mem *memory) briefCommand(ctx context.Context, req request) result {
	name, isSeated := mem.seated(ctx)
	if !isSeated {
		return refused("%s", ErrNoSeat)
	}

	defer mem.presenting(ctx, req.Again)()

	folded, err := mem.load(ctx)
	if err != nil {
		return refused("%v", err)
	}

	here := mem.checkout(ctx)

	if req.Kind != "" && !slices.Contains(entryKinds, req.Kind) {
		return refused("--kind takes %s, not %q", strings.Join(entryKinds, ", "), req.Kind)
	}

	if req.JSON {
		return mem.briefJSON(ctx, here, folded, name, briefFilter(req))
	}

	if req.Kind != "" {
		return done(mem.kindBrief(here, folded, name, req.Kind))
	}

	if len(req.Topics) > 0 {
		return done(mem.topicBrief(here, folded, name, req.Topics))
	}

	return done(mem.brief(ctx, here, folded, name))
}

// kindBrief is every live entry of one kind the seat holds, hottest first,
// whole: the place a note the brief's cap left out is read.
func (mem *memory) kindBrief(here tree, folded *state, seatName string, kind string) string {
	var lines []string

	for _, held := range mem.ownLive(folded, seatName, "kind", kind) {
		lines = append(lines, mem.lineOf(held, shortLine(here, held, seatName, mem.now())))
	}

	if len(lines) == 0 {
		return fmt.Sprintf("no live %s of %s", kind, seatName)
	}

	return strings.Join(lines, "\n")
}

func (mem *memory) topicBrief(here tree, folded *state, seatName string, topics []string) string {
	var wanted []string
	for _, topic := range topics {
		wanted = append(wanted, folded.resolve(topic))
	}

	held := &budget{limit: briefBudget}

	for _, entry := range mem.ownLive(folded, seatName, "topic", topics...) {
		mem.addLine(held, "", entry, shortLine(here, entry, seatName, mem.now()))
	}

	if len(held.lines) == 0 {
		return "no live entry of " + seatName + " carries " + strings.Join(wanted, ", ")
	}

	if held.cut > 0 {
		held.lines = append(held.lines, fmt.Sprintf("+%d more past the budget: %s search --topic %s --term .",
			held.cut, self, wanted[0]))
	}

	return strings.Join(held.lines, "\n")
}

// briefFilter is the one filter a narrowed brief names: its kind, else its
// topics, else none.
func briefFilter(req request) []string {
	if req.Kind != "" {
		return []string{"kind", req.Kind}
	}

	if len(req.Topics) > 0 {
		return append([]string{"topic"}, req.Topics...)
	}

	return []string{""}
}

// ownLive is the seat's live entries, hottest first: active:mine:rank(heat),
// narrowed by one more filter where a word is given.
func (mem *memory) ownLive(folded *state, seatName string, word string, values ...string) []*entry {
	scope := &viewScope{mem: mem, folded: folded, reader: seatName}

	var filters []filter

	for _, call := range [][]string{{"active"}, {"mine"}, append([]string{word}, values...)} {
		if call[0] == "" {
			continue
		}

		held, err := scope.filterOf(call[0], call[1:]...)
		if err != nil {
			return nil
		}

		filters = append(filters, held)
	}

	live := scope.selected(filters)

	return best(live, len(live), scope.order(live, rankHeat))
}

const rulesHeading = "rules (from the user, verify before relying)"

// briefLayout is the brief's kind sections as a fixed layout, which
// `view --explain brief` prints and nothing configures: each the seat's live
// entries of one kind, hottest first, in the short line, notes cut at the
// brief_note_limit.
func briefLayout(noteLimit int) string {
	section := func(kind string, heading string, cut string) string {
		return fmt.Sprintf(`active:mine:kind(%s):rank(heat):%sname(%q):brief:as-list`, kind, cut, heading)
	}

	return strings.Join([]string{
		section(kindRule, rulesHeading, ""),
		section(kindTrap, "traps", ""),
		section(kindTodo, "todos", ""),
		section(kindNote, "notes", fmt.Sprintf("n(%d):", noteLimit)),
	}, ",")
}

// briefParts is what `view --explain brief` says stands around the layout:
// the parts of the brief that are not lists of entries.
const briefParts = "brief, fixed: its first line, the state slot, changed by other seats and ended since last " +
	"brief; then these sections; then retire?, new from other seats and the topics. Nothing configures it."

// lowSpace is the brief's space line: only a store with under a tenth of its
// limit left is named, since with room to spare the figure changes nothing the
// agent does, and every write reply carries it anyway.
func (mem *memory) lowSpace() string {
	figures := []space.Figure{mem.attachmentsSpace()}
	if pieces.Tools.On(mem.options()) {
		figures = append(figures, tools.Space(mem.env.Roots, mem.options().ToolsTotalMB))
	}

	return space.LowLine(figures...)
}

// brief is what a fresh context gets: the seat's state slot, what other seats
// changed since its last brief, its live rules, traps, todos and notes, each by
// heat, what other seats filed, then the topics. Claim and compaction share it.
func (mem *memory) brief(ctx context.Context, here tree, folded *state, seatName string) string {
	defer mem.presenting(ctx, false)()

	since := mem.previousBrief(seatName)
	mem.reachEnds(ctx, folded, seatName, "", "")
	now := mem.now()
	live := mem.ownLive(folded, seatName, "")

	retired := 0

	for _, held := range folded.entryList() {
		if mem.onShelf(held, seatName) && held.status == statusRetired {
			retired++
		}
	}

	out := &budget{limit: briefBudget}
	seated := "seat " + seatName

	shelf := mem.shelfName(mem.shelfOf(seatName))
	if shelf != seatName {
		seated += " on shelf " + shelf
	}

	out.add(fmt.Sprintf("%s: %s, %d live, %d retired, entries in %s, %s", self, seated, len(live), retired,
		mem.language(ctx), mem.options().WritingStyle))

	if low := mem.lowSpace(); low != "" {
		out.add(low)
	}

	if slot := mem.stateLine(folded, seatName); slot != "" {
		out.add(slot)
	}

	for _, text := range mem.credentialLines(ctx, seatName) {
		out.add(text)
	}

	var changed []string

	for _, held := range folded.entryList() {
		if !mem.onShelf(held, seatName) {
			continue
		}

		for _, other := range held.events {
			if other.seat != seatName && other.at.After(since) {
				changed = append(changed, fmt.Sprintf("  %s %s by %s (%s)", held.id, other.text, other.seat, stamp(other.at)))
			}
		}
	}

	for _, merge := range folded.merges {
		if merge.seat != seatName && merge.at.After(since) {
			changed = append(changed, fmt.Sprintf("  topic %s merged into %s by %s", merge.from, merge.into, merge.seat))
		}
	}

	if len(changed) > 0 {
		out.add("changed by other seats:")

		for _, text := range changed {
			out.add(text)
		}
	}

	if ended := mem.endedSince(folded, seatName, since); len(ended) > 0 {
		out.add("ended since last brief (revive to undo):")

		for _, text := range ended {
			out.add(text)
		}
	}

	listed := map[string]bool{}
	for _, held := range live {
		listed[held.id] = true
	}

	scope := &viewScope{mem: mem, ctx: ctx, folded: folded, here: here, reader: seatName}

	sections, err := scope.compileLayout(briefLayout(mem.options().BriefNoteLimit))
	if err != nil {
		out.add(fmt.Sprintf("%s: the brief's layout: %v", self, err))
	}

	// A candidate printed in a section carries its retire? flag there; the
	// retire? section names only the ones no section printed.
	candidates := mem.retireCandidates(ctx, folded, seatName)
	flagged := map[string][]string{}

	for _, candidate := range candidates {
		flagged[candidate.held.id] = []string{candidate.flag()}
	}

	printed := map[string]bool{}
	missing := 0

	for _, section := range sections {
		page, kept := scope.run(section)
		if len(kept) == 0 {
			continue
		}

		out.add(section.name + ":")

		// Rules take at most a third of the brief. Notes are capped by n,
		// hottest first; traps are rare and todos end through their declared
		// conditions, so both print whole.
		if section.name == rulesHeading {
			third := &budget{limit: briefBudget / 3}
			for _, held := range page {
				mem.addLine(third, "  ", held, shortLine(here, held, seatName, now))
			}

			for _, text := range third.lines {
				out.add(text)
			}

			if third.cut > 0 {
				out.add(fmt.Sprintf("rules exceed a third of the brief: %d; retire or rekind some", len(kept)))
			}

			continue
		}

		for _, one := range page {
			extra := flagged[one.id]

			count, items := folded.briefItems(one, "    ")
			if count != "" {
				extra = append(slices.Clone(extra), count)
			}

			// A pointer stands for the line and its items, both printed before.
			fresh := !mem.seen.hasLine(one)

			if !mem.addLine(out, "  ", one, shortLine(here, one, seatName, now, extra...)) {
				missing++

				continue
			}

			printed[one.id] = true

			if fresh {
				for _, text := range items {
					out.add(text)
				}
			}

			for _, cited := range citingLines(one, listed, "    ") {
				out.add(cited)
			}
		}

		if capped := len(kept) - len(page); capped > 0 {
			out.add(fmt.Sprintf("+%d more notes: %s brief --kind note", capped, self))
		}
	}

	var unprinted []string

	for _, candidate := range candidates {
		if !printed[candidate.held.id] {
			unprinted = append(unprinted, fmt.Sprintf("  %s %s  %s", candidate.held.id, candidate.held.title, candidate.flag()))
		}
	}

	if len(unprinted) > 0 {
		out.add("retire?")

		for _, text := range unprinted {
			out.add(text)
		}
	}

	var news []*entry

	for _, held := range folded.entryList() {
		if !mem.onShelf(held, seatName) && held.status == statusLive && held.filed.After(since) {
			news = append(news, held)
		}
	}

	slices.SortStableFunc(news, func(a, b *entry) int { return b.filed.Compare(a.filed) })

	if len(news) > 0 {
		out.add("new from other seats:")

		for _, held := range news {
			mem.addLine(out, "  ", held, shortLine(here, held, seatName, now))
		}
	}

	counts := mem.topicCounts(folded)
	if len(counts) > 0 {
		names := make([]string, 0, len(counts))
		for name := range counts {
			names = append(names, name)
		}

		slices.SortFunc(names, func(a, b string) int {
			return cmp.Or(cmp.Compare(counts[b], counts[a]), strings.Compare(a, b))
		})

		var parts []string
		for _, name := range names {
			parts = append(parts, fmt.Sprintf("%s (%d)", name, counts[name]))
		}

		out.add("topics: " + strings.Join(parts, " "))
	}

	if missing > 0 {
		out.limit += 200
		out.add(fmt.Sprintf("+%d more: %s brief --topic <name>", missing, self))
	}

	_ = mem.writeSlot(mem.briefMarkKey(seatName), briefMark{At: mem.now()})

	return strings.Join(out.lines, "\n")
}
