package memory_test

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAStatusListingHonoursItsLimit(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	for index := range 5 {
		h.file(fmt.Sprintf("Listed finding number %02d recorded", index), "a listed finding")
		h.clock.Advance(time.Minute)
	}

	pages := []struct {
		name   string
		skip   string
		count  int
		footer string
	}{
		{"the first page", "0", 2, "3 more: --skip 2  (run before concluding none fits)"},
		{"a middle page", "2", 2, "1 more: --skip 4  (run before concluding none fits)"},
		{"the last page", "4", 1, ""},
	}

	for _, page := range pages {
		t.Run(page.name, func(t *testing.T) {
			out := h.must("", "search", "--status", "live", "--limit", "2", "--skip", page.skip)

			if got := listed(out); len(got) != page.count {
				t.Errorf("a status listing should print at most --limit entries from --skip, got:\n%s", out)
			}

			if page.footer == "" && strings.Contains(out, "more: --skip") {
				t.Errorf("the last page of a listing should end without a paging line, got:\n%s", out)
			}

			if page.footer != "" && !strings.HasSuffix(strings.TrimSpace(out), page.footer) {
				t.Errorf("a listing with entries past its page should end naming how many and the next flag, got:\n%s", out)
			}
		})
	}

	if out := h.must("", "search", "--status", "live", "--limit", "2", "--json"); !strings.Contains(out, `"more":{"skip":2}`) {
		t.Errorf("a JSON listing with entries past its page should name the next skip, got:\n%s", out)
	}
}
