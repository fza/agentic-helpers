package memory_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestAnEndedEntryPrintsNoEndDueOrStaleness(t *testing.T) {
	cases := []struct {
		name string
		end  func(h *harness, id string)
	}{
		{"retired", func(h *harness, id string) { h.must("", "retire", id, "--because", "done") }},
		{"superseded", func(h *harness, id string) {
			h.must("", "supersede", id, "--by", h.file("A later take on the README", "a newer body"))
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")

			id := h.file("Port the waiter per README", "see README.md", "--kind", "todo", "--until", "claim",
				"--due", "2027-01-01")
			test.WriteFile(t, filepath.Join(h.repo, "README.md"), "# app, changed\n")

			live := h.must("", "search", "--term", "per README", "--again")
			if !strings.Contains(live, "until: claim") || !strings.Contains(live, "due: 2027-01-01") || !strings.Contains(live, "drifted: README.md") {
				t.Fatalf("a live entry should print its end, due time and staleness:\n%s", live)
			}

			tc.end(h, id)

			reads := []string{
				h.must("", "search", "--term", "per README", "--status", "retired", "--status", "superseded", "--again"),
				h.must("", "show", id, "--again"),
			}

			for _, out := range reads {
				for _, flag := range []string{"until:", "due:", "drifted"} {
					if strings.Contains(out, flag) {
						t.Errorf("an ended entry should print no %q:\n%s", flag, out)
					}
				}
			}
		})
	}
}
