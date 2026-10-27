package memory_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func (h *harness) sessionEnd(reason string) string {
	data, _ := json.Marshal(map[string]string{"session_id": h.session, "reason": reason})

	return string(data)
}

// otherProject is a harness on another repository whose store sits beside the
// harness's own, under the same home and clock.
func otherProject(h *harness) *harness {
	other := newHarnessSharing(h)
	other.repo = filepath.Join(filepath.Dir(h.repo), "other")
	test.WriteFile(h.t, filepath.Join(other.repo, ".sdd", "config.yaml"), "repo_id: github.com/acme/other\n")
	test.WriteFile(h.t, filepath.Join(other.repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	other.git = &test.Git{Top: other.repo, Commit: "15af1a8c", Branch: "main"}

	return other
}

func TestLifetimeSetsTheShelfsLifetime(t *testing.T) {
	h := newHarness(t)

	if got := h.run("", "lifetime", "temporary"); got.code != 1 || !strings.Contains(got.err, "claim a seat first") {
		t.Errorf("lifetime without a seat should be refused, got %d %q", got.code, got.err)
	}

	h.must("", "claim", "main")

	cases := []struct {
		value, reply, stored string
	}{
		{"temporary", "shelf main is temporary: purged 1d after its last seat closes", `"lifetime": "temporary"`},
		{"2d", "shelf main is temporary: purged 2d after its last seat closes", `"lifetime": "2d"`},
		{"0", "shelf main is temporary: purged 0 after its last seat closes", `"lifetime": "0"`},
		{"durable", "shelf main is durable", ""},
	}

	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			if out := h.must("", "lifetime", tc.value); !strings.Contains(out, tc.reply) {
				t.Errorf("lifetime should say how the shelf now ends, got %q", out)
			}

			shelves, _ := os.ReadFile(filepath.Join(h.store(), "shelves.json"))
			if (tc.stored == "") == strings.Contains(string(shelves), `"lifetime"`) ||
				(tc.stored != "" && !strings.Contains(string(shelves), tc.stored)) {
				t.Errorf("the lifetime should be kept with the shelf, durable as none, got %s", shelves)
			}
		})
	}

	if got := h.run("", "lifetime", "a week"); got.code != 1 || !strings.Contains(got.err, "lifetime takes durable, temporary or a span") {
		t.Errorf("a value that is neither word nor span should be refused, got %d %q", got.code, got.err)
	}

	test.WriteFile(t, h.settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"temporary_seat_lifetime":"12h"}}}}`)

	if out := h.must("", "lifetime", "temporary"); !strings.Contains(out, "purged 12h after") {
		t.Errorf("the option should set the default retention, got %q", out)
	}

	test.WriteFile(t, h.settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"temporary_seat_lifetime":"soon"}}}}`)

	if answer := h.mcp(`{"name":"lifetime","arguments":{"value":"temporary"}}`); !strings.Contains(answer, `"temporary":true`) {
		t.Errorf("the MCP tool should say the shelf is temporary now, got %s", answer)
	}

	if out := h.must("", "lifetime", "temporary"); !strings.Contains(out, `purged 1d after its last seat closes (temporary_seat_lifetime "soon" is no span; 1d holds)`) {
		t.Errorf("an option holding no span should fall back to 1d with a note, got %q", out)
	}

	answer := h.mcp(`{"name":"lifetime","arguments":{"value":"durable"}}`)
	if !strings.Contains(answer, "shelf main is durable") || !strings.Contains(answer, `"temporary":false`) {
		t.Errorf("the MCP tool should set the lifetime from its value key, got %s", answer)
	}
}

func TestATemporaryShelfIsPurgedOnceItsRetentionRunsOut(t *testing.T) {
	h, other := rowShelf(t)
	other.must("", "release")
	other.must("", "claim", "idle")
	other.must("", "release")
	other.must("", "claim", "main")
	toRow := other.file("Handover for the row from main", "main's note", "--for", "row-7ah")
	other.must("", "release")
	other.must("", "claim", "rev")
	h.must("", "lifetime", "1d")

	implID, revID := seatID(t, h.store(), "impl"), seatID(t, h.store(), "rev")
	kept := h.file("Row finding kept for main", "a handover", "--for", "main")
	gone := h.file("Row finding for the row only", "the row's own")
	h.must("", "retire", gone)

	h.must(h.sessionEnd("clear"), "session-end")

	if got := h.run("", "holder", "impl"); got.code != 0 {
		t.Error("a /clear should leave the seat held")
	}

	h.must(h.sessionEnd("prompt_input_exit"), "session-end")
	h.clock.Advance(48 * time.Hour)
	h.must(hookPayload("startup", "", ""), "session-start")

	if out := other.must("", "show", gone); !strings.Contains(out, gone) {
		t.Fatal("a shelf with a seat still held should never be purged")
	}

	other.must("", "release")
	h.clock.Advance(12 * time.Hour)
	h.must(hookPayload("startup", "", ""), "session-start")

	if out := other.must("", "search", "--all-seats", "--status", "retired", "--no-heat"); !strings.Contains(out, gone) {
		t.Fatal("a shelf should keep everything until its retention has run out since its last seat closed")
	}

	other.must("", "claim", "rev")
	other.must("", "release")
	h.clock.Advance(25 * time.Hour)

	otherProject(h).must(hookPayload("startup", "", ""), "session-start")

	h.must("", "claim", "main")

	if out := h.must("", "search", "--all-seats", "--status", "live", "--status", "retired", "--with-superseded", "--no-heat"); strings.Contains(out, gone) || !strings.Contains(out, kept) {
		t.Errorf("a purge should remove the shelf's entries and keep those filed for another shelf, got:\n%s", out)
	}

	if got := h.run("", "revive", gone); got.code != 1 || !strings.Contains(got.err, "no such entry") {
		t.Errorf("a purged entry should be unknown, got %d %q", got.code, got.err)
	}

	if out := h.must("", "show", kept); !strings.Contains(out, "[impl (expired) → main, ") {
		t.Errorf("a surviving entry should name its filer as expired, got:\n%s", out)
	}

	if out := h.must("", "show", toRow); !strings.Contains(out, toRow) {
		t.Errorf("an entry another shelf filed for the purged shelf should stay its filer's, got:\n%s", out)
	}

	h.must("", "release")
	h.must("", "claim", "idle")

	if got := h.run("", "retire", kept); got.code != 1 || !strings.Contains(got.err, kept+" was filed by impl (expired) for other shelves") {
		t.Errorf("a seat the surviving entry was not filed for should be refused it, got %d %q", got.code, got.err)
	}

	h.must("", "release")
	h.must("", "claim", "main")
	h.must("", "retire", kept, "--because", "read")

	shelves, _, _ := seat.LoadShelves(h.store())
	if _, left := shelves.IDOf("row-7ah"); left {
		t.Error("a purge should remove the shelf")
	}

	if _, found := seat.Read(filepath.Join(h.store(), "seats"), seatID(t, h.store(), "main")); !found {
		t.Error("a purge should leave other seats' claims alone")
	}

	for _, id := range []string{implID, revID} {
		if test.Exists(t, filepath.Join(h.store(), "seats", id+".last.json")) {
			t.Error("a purge should remove the purged seats' files")
		}
	}

	h.must("", "release")
	h.must("", "claim", "impl")

	if out := h.must("", "brief"); strings.Contains(out, "Row finding") {
		t.Errorf("a new seat under a purged seat's name should carry nothing of it, got:\n%s", out)
	}
}

func TestAHolderGoneWithoutSessionEndClosedAtItsTranscript(t *testing.T) {
	h, other := rowShelf(t)
	h.must("", "lifetime", "1d")
	h.file("Row finding", "the row's own")

	transcript := filepath.Join(t.TempDir(), "rev.jsonl")
	test.WriteFile(t, transcript, "{}\n")

	payload, _ := json.Marshal(map[string]string{"session_id": other.session, "transcript_path": transcript})
	other.must(string(payload), "prompt")

	h.must("", "release")
	h.clock.Advance(48 * time.Hour)
	lastWrite := h.clock.Now().Add(-12 * time.Hour)

	err := os.Chtimes(transcript, lastWrite, lastWrite)
	if err != nil {
		t.Fatal(err)
	}

	h.processes.Kill(test.OtherOwnerPID)
	h.must(hookPayload("startup", "", ""), "session-start")

	if _, found := mustShelf(h, "row-7ah"); !found {
		t.Fatal("a holder gone without SessionEnd should count as closed at its transcript's last write, not at its claim")
	}

	h.clock.Advance(13 * time.Hour)
	h.must(hookPayload("startup", "", ""), "session-start")

	if _, found := mustShelf(h, "row-7ah"); found {
		t.Error("the retention should run from the transcript's last write")
	}
}

func TestRetentionZeroPurgesAtTheLastClose(t *testing.T) {
	h, other := rowShelf(t)
	h.must("", "lifetime", "0")
	h.file("Row finding", "the row's own")
	h.must("", "release")

	if _, found := mustShelf(h, "row-7ah"); !found {
		t.Fatal("a shelf with a seat still held should stay")
	}

	other.must(other.sessionEnd("other"), "session-end")

	if _, found := mustShelf(h, "row-7ah"); found {
		t.Error("retention 0 should purge the shelf as its last seat closes")
	}

	h.must("", "claim", "scratch", "--lifetime", "0")
	scratch := seatID(t, h.store(), "scratch")
	h.must("where the scratch work stands", "state")
	h.must("", "release")

	if _, found := mustShelf(h, "scratch"); found {
		t.Error("retention 0 should purge the shelf as its last seat is released")
	}

	for _, row := range storeEvents(t, h) {
		if row.Seat == scratch {
			t.Error("a purge should drop the state rows of the seats it purges")
		}
	}
}

func mustShelf(h *harness, name string) (string, bool) {
	shelves, _, _ := seat.LoadShelves(h.store())

	return shelves.IDOf(name)
}

func TestDropPurgesATemporaryShelfAtOnce(t *testing.T) {
	h, other := rowShelf(t)
	h.file("Row finding", "the row's own")

	if got := h.run("", "drop", "row-7ah"); got.code != 1 || !strings.Contains(got.err, "row-7ah is durable; make it temporary first") {
		t.Errorf("drop should refuse a durable shelf, got %d %q", got.code, got.err)
	}

	h.must("", "lifetime", "30d")

	if got := h.run("", "drop", "row-7ah"); got.code != 1 || !strings.Contains(got.err, "holds a seat on row-7ah") {
		t.Errorf("drop should refuse a shelf a seat is held on, got %d %q", got.code, got.err)
	}

	h.must("", "release")
	other.must("", "release")

	if out := h.must("", "drop", "row-7ah"); !strings.Contains(out, "dropped row-7ah (2 seats, 1 entry)") {
		t.Errorf("drop should purge the shelf at once and count what went, got %q", out)
	}

	if got := h.run("", "drop", "row-7ah"); got.code != 1 || !strings.Contains(got.err, "no shelf called row-7ah") {
		t.Errorf("drop should refuse a shelf that does not exist, got %d %q", got.code, got.err)
	}
}

func TestClaimSetsTheLifetimeOfTheSeatsShelf(t *testing.T) {
	h := newHarness(t)

	if out := h.must("", "claim", "impl", "--shelf", "row-7ah", "--lifetime", "3d"); !strings.Contains(out, "shelf row-7ah is temporary: purged 3d after its last seat closes") {
		t.Errorf("claim --lifetime should set and say the shelf's lifetime, got:\n%s", out)
	}

	h.must("", "release")

	if out := h.must("", "claim", "impl"); !strings.Contains(out, "shelf row-7ah is temporary") {
		t.Errorf("a claim onto a temporary shelf should say how it ends, got:\n%s", out)
	}

	h.must("", "release")

	if got := h.run("", "claim", "impl", "--lifetime", "forever"); got.code != 1 || !strings.Contains(got.err, "lifetime takes durable, temporary or a span") {
		t.Errorf("claim should refuse a lifetime that is neither word nor span, got %d %q", got.code, got.err)
	}
}
