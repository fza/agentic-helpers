package memory

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

// timeLayout carries the zone: a local time alone repeats an hour each autumn
// when the clocks go back, and two stamps from that hour read out of order.
const timeLayout = "2006-01-02 15:04 MST"

func stamp(at time.Time) string {
	return at.Local().Format(timeLayout)
}

// line is how an entry shows in every list: id, title, the bracket naming seat,
// kind, a retain override that differs from the kind's decay, source, filing
// time and commit, then its flags.
func line(here tree, held *entry) string {
	return withFlags(lineHead(held), append(flagsOf(here, held, held.flags()), declared(held)...))
}

// lineHead is a full line before its flags: id, title and the bracket.
func lineHead(held *entry) string {
	filer := held.seat
	if held.recipients != "" {
		filer += " → " + held.recipients
	}

	parts := []string{filer, held.kind}
	if retained := held.retainedOverride(); retained != "" {
		parts = append(parts, "retain "+retained)
	}

	parts = append(parts, "from "+held.from, stamp(held.filed))
	if held.commit != "" {
		parts = append(parts, held.commit)
	}

	return fmt.Sprintf("%s %s  [%s]", held.id, held.title, strings.Join(parts, ", "))
}

// shortLine is how an entry shows in the brief and in recall, read every
// claim and compaction: id, title, its age, the filing seat where it is not
// the reader, a retain override that differs from the kind's decay, then its
// flags with the ended links folded, then any extra flag the caller adds.
func shortLine(here tree, held *entry, reader string, now time.Time, extra ...string) string {
	parts := []string{age(now.Sub(held.filed))}
	if held.seat != reader {
		parts = append(parts, held.seat)
	}

	if retained := held.retainedOverride(); retained != "" {
		parts = append(parts, "retain "+retained)
	}

	flags := append(flagsOf(here, held, foldedLinks(held.flags())), extra...)

	return withFlags(fmt.Sprintf("%s %s  [%s]", held.id, held.title, strings.Join(parts, ", ")), flags)
}

// foldedLinks is a short line's link flags: a settled target in full, since a
// decision in force is context, every other ended one folded into one count,
// a superseded one kept by its successor, which is what an agent follows.
func foldedLinks(flags []string) []string {
	var (
		listed     []string
		successors []string
	)

	ended := 0
	bySuccessor := map[string]int{}

	for _, flag := range flags {
		_, state, _ := strings.Cut(strings.TrimPrefix(flag, "→ "), " ")
		successor, superseded := strings.CutPrefix(state, "superseded by ")

		switch {
		case state == "settled" || strings.HasPrefix(flag, "→ tool "):
			listed = append(listed, flag)
		case superseded:
			if bySuccessor[successor] == 0 {
				successors = append(successors, successor)
			}

			bySuccessor[successor]++
		default:
			ended++
		}
	}

	var folded []string
	if ended > 0 {
		folded = append(folded, fmt.Sprintf("%d ended", ended))
	}

	for _, successor := range successors {
		folded = append(folded, fmt.Sprintf("%d superseded by %s", bySuccessor[successor], successor))
	}

	if len(folded) > 0 {
		listed = append(listed, "→ "+strings.Join(folded, ", "))
	}

	return listed
}

// flagsOf is every flag an entry's line carries: its status, its staleness
// against the checkout, the link flags given, and how many live or retired
// entries link to it.
func flagsOf(here tree, held *entry, links []string) []string {
	flags := statusFlags(held)
	flags = append(flags, staleness(here, held)...)
	flags = append(flags, links...)

	if inbound := held.linkedFrom(false); len(inbound) > 0 {
		flags = append(flags, fmt.Sprintf("← %d", len(inbound)))
	}

	return flags
}

func withFlags(text string, flags []string) string {
	if len(flags) > 0 {
		text += "  " + strings.Join(flags, "; ")
	}

	return text
}

// linkedFrom is the entries linking to held: superseded ones only where
// withSuperseded is set, since every older link of a supersede chain is one.
// A retired one stays: it ended, and nothing replaced it.
func (held *entry) linkedFrom(withSuperseded bool) []*entry {
	if withSuperseded {
		return held.inbound
	}

	var linked []*entry

	for _, other := range held.inbound {
		if other.status != statusSuperseded {
			linked = append(linked, other)
		}
	}

	return linked
}

// declared is what the filer declared about the entry's life, as a full line
// names it: the conditions that end it, a trap's triggers, the moments a rule
// is owed at and the calls proving it, and when it comes due.
func declared(held *entry) []string {
	var flags []string

	for _, part := range []struct {
		name   string
		values []string
	}{{"until", held.until}, {"when", held.when}, {"on", held.on}, {"proof", held.proof}, {"forbids", held.forbids}} {
		if len(part.values) > 0 {
			flags = append(flags, part.name+": "+strings.Join(part.values, ", "))
		}
	}

	if !held.due.IsZero() {
		flags = append(flags, "due: "+stamp(held.due))
	}

	return flags
}

func statusFlags(held *entry) []string {
	last, found := held.latest()
	if !found {
		return nil
	}

	switch held.status {
	case statusRetired:
		if last.because != "" {
			return []string{fmt.Sprintf("retired %s: %s", stamp(last.at), last.because)}
		}

		return []string{"retired " + stamp(last.at)}
	case statusSuperseded:
		return []string{"superseded " + stamp(last.at) + supersession(last.by, last.because)}
	}

	return nil
}

// unspaced are the scripts written without spaces between words, where a
// character is the unit a count can rely on.
var unspaced = []*unicode.RangeTable{
	unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Thai, unicode.Lao, unicode.Khmer, unicode.Myanmar,
}

func isUnspaced(char rune) bool {
	return unicode.In(char, unspaced...)
}

// words counts each character of an unspaced script as a word, and every other
// run between whitespace and such characters as one.
func words(text string) int {
	count := 0

	for _, field := range strings.Fields(text) {
		inRun := false

		for _, char := range field {
			switch {
			case isUnspaced(char):
				count++
				inRun = false
			case !inRun:
				count++
				inRun = true
			}
		}
	}

	return count
}

func plural(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}

	if strings.HasSuffix(noun, "ry") {
		return fmt.Sprintf("%d %sies", count, strings.TrimSuffix(noun, "y"))
	}

	return fmt.Sprintf("%d %ss", count, noun)
}
