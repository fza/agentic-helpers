package memory

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
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

func (mem *memory) briefMarkPath(seatName string) string {
	return filepath.Join(mem.cacheDir(), "briefs", mem.seatKey(seatName)+".json")
}

func (mem *memory) previousBrief(seatName string) time.Time {
	var mark briefMark
	readJSON(mem.briefMarkPath(seatName), &mark)

	return mark.At
}

func (mem *memory) briefCommand(ctx context.Context, req request) result {
	name, isSeated := mem.seated(ctx)
	if !isSeated {
		return refused("%s", ErrNoSeat)
	}

	folded, err := mem.load(ctx)
	if err != nil {
		return refused("%v", err)
	}

	here := mem.checkout(ctx)

	if len(req.Topics) > 0 {
		return done(mem.topicBrief(here, folded, name, req.Topics))
	}

	return done(mem.brief(ctx, here, folded, name))
}

func (mem *memory) topicBrief(here tree, folded *state, seatName string, topics []string) string {
	var wanted []string
	for _, topic := range topics {
		wanted = append(wanted, folded.resolve(topic))
	}

	held := &budget{limit: briefBudget}

	for _, entry := range mem.ownLive(folded, seatName) {
		if slices.ContainsFunc(folded.topicsOf(entry), func(topic string) bool { return slices.Contains(wanted, topic) }) {
			held.add(line(here, entry))
		}
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

// ownLive is the seat's live entries, hottest first.
func (mem *memory) ownLive(folded *state, seatName string) []*entry {
	heats := mem.heats()

	var live []*entry

	for _, held := range folded.entryList() {
		if held.status == statusLive && mem.ownOrReceived(folded, held, seatName) {
			live = append(live, held)
		}
	}

	slices.SortStableFunc(live, func(a, b *entry) int {
		return cmp.Or(cmp.Compare(mem.heat(heats, b), mem.heat(heats, a)), b.filed.Compare(a.filed))
	})

	return live
}

// brief is what a fresh context gets: standing entries, then the seat's live
// entries by heat, then what other seats did since this seat's last brief, then
// the topics. Claim and compaction share it.
func (mem *memory) brief(ctx context.Context, here tree, folded *state, seatName string) string {
	since := mem.previousBrief(seatName)
	live := mem.ownLive(folded, seatName)

	retired := 0

	for _, held := range folded.entryList() {
		if held.seat == seatName && held.status == statusRetired {
			retired++
		}
	}

	out := &budget{limit: briefBudget}
	out.add(fmt.Sprintf("%s: seat %s, %d live, %d retired, entries in %s", self, seatName, len(live), retired, mem.language(ctx)))

	var changed []string

	for _, held := range folded.entryList() {
		if held.seat != seatName {
			continue
		}

		for _, other := range held.changes {
			if other.at.After(since) {
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

	var standing, working []*entry

	for _, held := range live {
		if held.retention == "standing" {
			standing = append(standing, held)
		} else {
			working = append(working, held)
		}
	}

	if len(standing) > 0 {
		out.add("standing (user-given grants and instructions, verify before relying):")

		third := &budget{limit: briefBudget / 3}
		for _, held := range standing {
			third.add("  " + line(here, held))
		}

		for _, text := range third.lines {
			out.add(text)
		}

		if third.cut > 0 {
			out.add(fmt.Sprintf("standing entries exceed a third of the brief: %d; demote some", len(standing)))
		}
	}

	shown := 0

	if len(working) > 0 {
		out.add("working set:")

		for _, held := range working {
			if out.add("  " + line(here, held)) {
				shown++
			}
		}
	}

	var news []*entry

	for _, held := range folded.entryList() {
		if held.seat != seatName && held.status == statusLive && held.filed.After(since) {
			news = append(news, held)
		}
	}

	slices.SortStableFunc(news, func(a, b *entry) int { return b.filed.Compare(a.filed) })

	if len(news) > 0 {
		out.add("new from other seats:")

		for _, held := range news {
			out.add("  " + line(here, held))
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

	if missing := len(working) - shown; missing > 0 {
		out.limit += 200
		out.add(fmt.Sprintf("+%d more: %s brief --topic <name>", missing, self))
	}

	_ = writeJSON(mem.briefMarkPath(seatName), briefMark{At: mem.now()})

	return strings.Join(out.lines, "\n")
}
