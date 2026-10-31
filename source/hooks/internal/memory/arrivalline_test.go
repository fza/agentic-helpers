package memory_test

import (
	"strings"
	"testing"
	"time"
)

func TestAnArrivalPrintsTheShortLineNamingOnlyAKindAndSourceNotTheDefaults(t *testing.T) {
	cases := []struct {
		name   string
		title  string
		extra  []string
		suffix string
	}{
		{"a note from an agent names the filer alone", "Review owes the API diff", nil, "  [0m, main]"},
		{"a trap names its kind", "Gate trap handed over", []string{"--kind", "trap"}, "  [0m, main, trap]"},
		{"a note from the user names its source", "Owner wants the diff today", []string{"--from", "user"}, "  [0m, main, from user]"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, other := twoSeats(t)
			other.must(hookPayload("", "", ""), "prompt")
			h.clock.Advance(time.Minute)
			id := h.file(tc.title, "a handover", append([]string{"--for", "idea"}, tc.extra...)...)

			out := other.must(hookPayload("", "", "next"), "prompt")

			arrived := linesHolding(out, id)
			if len(arrived) != 1 || arrived[0] != "  "+id+" "+tc.title+tc.suffix {
				t.Errorf("the arrival should print the short line with only what differs from a note by an agent:\n%s", out)
			}

			if strings.Contains(out, "→ idea") {
				t.Errorf("the arrival should not name the reader as its recipient:\n%s", out)
			}
		})
	}
}
