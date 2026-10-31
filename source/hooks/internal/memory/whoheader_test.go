package memory_test

import (
	"regexp"
	"strings"
	"testing"
)

func TestWhoNamesItsWindowOnceAndAShelfOnlyWhereItIsNotTheSeat(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	alone := h.must("", "who")
	if !strings.HasPrefix(alone, "seats, filed / used in the last 14d:\nmain  ") || strings.Count(alone, "14d") != 1 {
		t.Errorf("who should name the window once, above the rows:\n%s", alone)
	}

	if strings.Contains(alone, "shelf") || !regexp.MustCompile(`(?m)^main  [0-9a-f]{8} pid `).MatchString(alone) {
		t.Errorf("a seat on the shelf of its own name should print no shelf column:\n%s", alone)
	}

	other := newHarnessSharing(h)
	other.must("", "claim", "main-2", "--shelf", "main")

	shared := h.must("", "who")

	var mate, own []string

	for _, row := range strings.Split(shared, "\n") {
		switch {
		case strings.HasPrefix(row, "main-2 "):
			mate = append(mate, row)
		case strings.HasPrefix(row, "main "):
			own = append(own, row)
		}
	}

	if len(mate) != 1 || !strings.Contains(mate[0], "  shelf main  ") || len(own) != 1 || strings.Contains(own[0], "shelf") {
		t.Errorf("only a seat on a shelf of another name should print its shelf:\n%s", shared)
	}
}
