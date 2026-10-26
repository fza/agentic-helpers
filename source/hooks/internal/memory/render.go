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

	text := fmt.Sprintf("%s %s  [%s]", held.id, held.title, strings.Join(parts, ", "))

	flags := statusFlags(held)
	flags = append(flags, staleness(here, held)...)
	flags = append(flags, held.linkFlags...)

	if len(held.inbound) > 0 {
		flags = append(flags, fmt.Sprintf("← %d", len(held.inbound)))
	}

	flags = append(flags, declared(held)...)

	if len(flags) > 0 {
		text += "  " + strings.Join(flags, "; ")
	}

	return text
}

// declared is what the filer declared about the entry's life, as a full line
// names it: the conditions that end it.
func declared(held *entry) []string {
	var flags []string

	if len(held.until) > 0 {
		flags = append(flags, "until: "+strings.Join(held.until, ", "))
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
		if last.by != "" {
			return []string{fmt.Sprintf("superseded %s by %s", stamp(last.at), last.by)}
		}

		return []string{fmt.Sprintf("superseded %s: %s", stamp(last.at), last.because)}
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

	return fmt.Sprintf("%d %ss", count, noun)
}
