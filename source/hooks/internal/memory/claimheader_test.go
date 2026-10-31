package memory_test

import (
	"regexp"
	"strings"
	"testing"
)

var claimHeader = regexp.MustCompile(`^agentic-memory: seat main, 0 live, 0 retired, entries in \S+, \S+, session [0-9a-f]{8} \(pid \d+\)$`)

// claimedAs is whether out opens as a claim of seat does: the brief's first
// line, naming the seat and any shelf of another name.
func claimedAs(out string, seat string) bool {
	return strings.HasPrefix(out, "agentic-memory: seat "+seat+",") ||
		strings.HasPrefix(out, "agentic-memory: seat "+seat+" on shelf ")
}

func TestAClaimOpensWithOneHeaderLineNamingTheSeatAndItsHolder(t *testing.T) {
	h := newHarness(t)

	out := h.must("", "claim", "main")

	first, _, _ := strings.Cut(out, "\n")
	if !claimHeader.MatchString(first) {
		t.Errorf("a claim should open with the brief's first line naming the holder:\n%s", out)
	}

	if strings.Count(out, "seat main") != 1 || strings.Contains(out, "\nmain: ") {
		t.Errorf("a claim should name its seat in one header line:\n%s", out)
	}
}
