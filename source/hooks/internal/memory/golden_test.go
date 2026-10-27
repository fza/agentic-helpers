package memory_test

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite the golden reads under test/fixtures/memory/golden")

// localStamp is a printed time: the zone is the machine's, so a golden read
// holds the placeholder in its place.
var localStamp = regexp.MustCompile(`\d{4}-\d{2}-\d{2} \d{2}:\d{2} [A-Z0-9+-]+`)

// goldenStore files a store touching every section the brief prints and every
// status a search lists: a rule, a trap whose anchor drifted, a todo with a
// declared end, more notes than the brief prints, a link to a closed decision,
// an anchor gone, entries filed for the seat, for all and under a followed
// topic, a link another seat made, a retire, a supersede, heat from use and a
// state slot.
func goldenStore(t *testing.T) *harness {
	t.Helper()

	h, other := twoSeats(t)
	withGraph(t, h)

	h.must("commit when asked", "new", "--title", "Owner allows commits on main", "--topic", "grants",
		"--kind", "rule", "--from", "user")
	h.file("Gate ignores MCP reads always", "the gate reads internal/gate/gate.go only for Bash", "--kind", "trap")
	h.file("Port the waiter to Linux", "the waiter polls darwin only", "--kind", "todo",
		"--until", "at:2027-01-01T00:00:00Z")

	var notes []string

	for index, word := range unrelated[:12] {
		h.clock.Advance(7 * time.Minute)

		topic := "gate"
		if index%4 == 0 {
			topic = "infra"
		}

		notes = append(notes, h.file("Note about the "+word+" finding", word+" "+word, "--topic", topic))
	}

	h.file("Finding citing a closed decision", "see the decision", "--link", closedDecision)
	h.file("Readme names the build steps", "the README.md lists them")
	h.must("ten words of where the gate fix stands", "state")

	h.clock.Advance(time.Hour)
	other.file("Review owes the API diff", "a handover", "--for", "main")
	other.file("Staging DB moved to db2", "a changed fact", "--for", "all")
	h.must("", "follow", "deploy")
	other.file("Deploy runs at noon daily", "a schedule", "--topic", "deploy")
	other.must("", "link", notes[1], notes[2])

	h.must("", "retire", notes[3], "--because", "done with it")
	h.must("", "supersede", notes[4], "--by", notes[5])
	h.must("", "supersede", notes[9], "--because", "it was wrong")
	h.must("", "show", notes[6])
	h.must("", "show", notes[7])
	h.must("", "show", notes[7])

	test.WriteFile(t, filepath.Join(h.repo, "internal", "gate", "gate.go"), "package gate\n\nfunc Changed() {}\n")

	err := os.Remove(filepath.Join(h.repo, "README.md"))
	if err != nil {
		t.Fatal(err)
	}

	h.clock.Advance(3 * time.Hour)

	return h
}

func TestReadsMatchTheirGoldenText(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{name: "brief", args: []string{"brief"}},
		{name: "brief-json", args: []string{"brief", "--json"}},
		{name: "brief-kind-note", args: []string{"brief", "--kind", "note"}},
		{name: "brief-kind-trap", args: []string{"brief", "--kind", "trap"}},
		{name: "brief-topic-infra", args: []string{"brief", "--topic", "infra"}},
		{name: "search-term", args: []string{"search", "--term", "note", "--no-heat"}},
		{name: "search-term-json", args: []string{"search", "--term", "note", "--no-heat", "--json"}},
		{name: "search-page-json", args: []string{"search", "--term", "the", "--limit", "3", "--skip", "2", "--no-heat", "--json"}},
		{name: "search-retired-json", args: []string{"search", "--status", "retired", "--json"}},
		{name: "search-superseded-json", args: []string{"search", "--status", "superseded", "--json"}},
		{name: "search-live-all-seats-json", args: []string{"search", "--status", "live", "--all-seats", "--json"}},
		{name: "search-seat-json", args: []string{"search", "--status", "live", "--seat", "idea", "--json"}},
		{name: "search-topic-json", args: []string{"search", "--term", ".", "--topic", "infra", "--no-heat", "--json"}},
		{name: "search-with-superseded-json", args: []string{"search", "--term", "finding", "--with-superseded", "--no-heat", "--json"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := goldenStore(t)

			got := h.must("", tc.args...)
			got = localStamp.ReplaceAllString(got, "<stamp>")
			got = strings.ReplaceAll(got, h.repo, "<repo>")

			path := filepath.Join("..", "..", "test", "fixtures", "memory", "golden", tc.name+".txt")

			if *updateGolden {
				test.WriteFile(t, path, got)

				return
			}

			if got != test.Fixture(t, "memory/golden/"+tc.name+".txt") {
				t.Errorf("the read should stay byte-identical to its golden text, got:\n%s", got)
			}
		})
	}
}
