package memory

import (
	"fmt"
	"strings"
	"time"
)

// timeLayout carries the zone: a local time alone repeats an hour each autumn
// when the clocks go back, and two stamps from that hour read out of order.
const timeLayout = "2006-01-02 15:04 MST"

func stamp(at time.Time) string {
	return at.Local().Format(timeLayout)
}

// line is how an entry shows in every list: id, title, the bracket naming seat,
// retention, source, filing time and commit, then its flags.
func line(here tree, held *entry) string {
	parts := []string{held.seat, held.retention, "from " + held.from, stamp(held.filed)}
	if held.commit != "" {
		parts = append(parts, held.commit)
	}

	text := fmt.Sprintf("%s %s  [%s]", held.id, held.title, strings.Join(parts, ", "))

	flags := statusFlags(held)
	flags = append(flags, staleness(here, held)...)

	if len(flags) > 0 {
		text += "  " + strings.Join(flags, "; ")
	}

	return text
}

func statusFlags(held *entry) []string {
	last, found := held.latest()
	if !found {
		return nil
	}

	switch held.status {
	case statusRetired:
		return []string{"retired " + stamp(last.at)}
	case statusSuperseded:
		if last.by != "" {
			return []string{fmt.Sprintf("superseded %s by %s", stamp(last.at), last.by)}
		}

		return []string{fmt.Sprintf("superseded %s: %s", stamp(last.at), last.because)}
	}

	return nil
}

func words(text string) int {
	return len(strings.Fields(text))
}

func plural(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}

	return fmt.Sprintf("%d %ss", count, noun)
}
