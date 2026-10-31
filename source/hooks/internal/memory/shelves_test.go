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

// rowShelf is impl held by the harness session and rev by the other one, both
// on the shelf row-7ah.
func rowShelf(t *testing.T) (*harness, *harness) {
	t.Helper()

	h := newHarness(t)
	h.must("", "claim", "impl", "--shelf", "row-7ah")

	other := newHarnessSharing(h)
	other.must("", "claim", "rev", "--shelf", "row-7ah")

	return h, other
}

func (h *harness) promptHook(prompt string) string {
	data, _ := json.Marshal(map[string]string{"session_id": h.session, "prompt": prompt})

	return string(data)
}

func TestSeatsOnOneShelfShareItsMemory(t *testing.T) {
	h, other := rowShelf(t)

	if shelfID(t, h.store(), "impl") != shelfID(t, h.store(), "rev") {
		t.Fatal("two seats claimed onto one shelf should file onto the same shelf")
	}

	other.must(other.promptHook(""), "prompt")
	other.clock.Advance(time.Minute)

	id := h.file("Gate finding on the row", "the row's finding")

	if out := other.must("", "search", "--term", "row's finding"); !strings.Contains(out, id) || !strings.Contains(out, "[impl, ") {
		t.Errorf("a shelf mate's entry should be found by a seat's default search, its filer named, got:\n%s", out)
	}

	if out := other.must("", "search", "--term", "row's finding", "--seat", "rev"); strings.Contains(out, id) {
		t.Errorf("--seat should narrow a search to one filer, got:\n%s", out)
	}

	if out := other.must("", "brief"); !strings.Contains(out, "seat rev on shelf row-7ah") || !strings.Contains(out, id) {
		t.Errorf("a seat's brief should name its shared shelf and hold its shelf mates' entries, got:\n%s", out)
	}

	if out := other.must(other.promptHook("next"), "prompt"); !strings.Contains(out, "agentic-memory: new for rev (1):") ||
		!strings.Contains(out, id) {
		t.Errorf("a shelf mate's new entry should reach the other seats on the shelf as an arrival, got:\n%s", out)
	}

	h.must(h.promptHook(""), "prompt")
	h.clock.Advance(time.Minute)
	other.must("", "retire", id, "--because", "fixed on the row")

	out := h.must(h.promptHook("again"), "prompt")
	if !strings.Contains(out, "agentic-memory: changed on shelf row-7ah (1):") ||
		!strings.Contains(out, id+" Gate finding on the row: retired: fixed on the row by rev") {
		t.Errorf("a shelf mate's change should reach the other seats on the shelf, naming the verb and the seat, got:\n%s", out)
	}

	if out := h.must(h.promptHook("once more"), "prompt"); strings.Contains(out, "changed on shelf") {
		t.Errorf("a change should be told once, got:\n%s", out)
	}

	h.clock.Advance(time.Minute)
	own := h.file("Second gate finding on the row", "another finding")
	h.must("", "retire", own)
	other.must("", "confirm", own)
	other.must("", "supersede", own, "--because", "covered elsewhere")

	if out := h.must(h.promptHook("last"), "prompt"); strings.Contains(out, "changed on shelf") {
		t.Errorf("a seat should hear neither of its own changes nor of a supersede or confirm, got:\n%s", out)
	}
}

func TestASeatOnAnotherShelfMayNotEditTheShelfsEntries(t *testing.T) {
	h, other := rowShelf(t)
	id := h.file("Gate finding on the row", "the row's finding")

	other.must("", "release")
	other.must("", "claim", "main")

	for _, args := range [][]string{
		{"retire", id}, {"retain", id, "long"}, {"retitle", id, "A title from elsewhere"}, {"tag", id, "x"},
	} {
		got := other.run("", args...)
		if got.code != 1 || !strings.Contains(got.err, id+" is on shelf row-7ah") {
			t.Errorf("%s from a seat on another shelf should be refused naming the shelf, got %d %q", args[0], got.code, got.err)
		}
	}

	if out := other.must("", "search", "--term", "row's finding"); strings.Contains(out, id) {
		t.Errorf("another shelf's entry should stay out of a seat's default search, got:\n%s", out)
	}
}

func TestClaimingOntoAShelf(t *testing.T) {
	h, other := rowShelf(t)

	h.must("", "release")

	if got := h.run("", "claim", "impl", "--shelf", "row-8bq"); got.code != 1 ||
		!strings.Contains(got.err, "impl is on shelf row-7ah") {
		t.Errorf("a seat should stay on its shelf for good, got %d %q", got.code, got.err)
	}

	h.must("", "claim", "impl", "--shelf", "row-7ah")
	h.must("", "release")

	if got := h.run("", "claim", "row-7ah"); got.code != 1 ||
		!strings.Contains(got.err, "row-7ah names a shelf; claim it with --shelf row-7ah") {
		t.Errorf("a new seat named like a shelf should be pointed at --shelf, got %d %q", got.code, got.err)
	}

	minted := h.must("", "claim", "--shelf", "row-7ah")
	if !strings.HasPrefix(minted, "agentic-memory: seat row-7ah-2 ") || shelfID(t, h.store(), "row-7ah-2") != shelfID(t, h.store(), "rev") {
		t.Errorf("a claim naming only a shelf should mint the lowest free <shelf>-<n> onto it, got:\n%s", minted)
	}

	h.must("", "release")
	other.must("", "release")

	if out := other.must("", "claim", "--shelf", "row-7ah"); !strings.HasPrefix(out, "agentic-memory: seat row-7ah-3 ") {
		t.Errorf("a minted name should pass over a seat already minted, got:\n%s", out)
	}

	answer := h.mcp(`{"name":"claim","arguments":{"shelf":"row-9zz"}}`)
	if !strings.Contains(answer, "agentic-memory: seat row-9zz-2 ") {
		t.Errorf("the MCP claim should mint a seat from a shelf alone, got %s", answer)
	}
}

func TestForAndFollowAttachToShelves(t *testing.T) {
	h, other := rowShelf(t)
	outside := newHarnessSharing(h)
	other.must("", "release")
	outside.must("", "claim", "main")

	toSeat := outside.file("Handover for the implementer", "for impl", "--for", "impl")
	toShelf := outside.file("Handover for the row", "for the row", "--for", "row-7ah")

	outside.must("", "release")
	other.must("", "claim", "rev")

	for _, id := range []string{toSeat, toShelf} {
		if out := other.must("", "search", "--term", "Handover"); !strings.Contains(out, id) {
			t.Errorf("an entry --for a seat or its shelf should reach every seat on that shelf, got:\n%s", out)
		}
	}

	if out := other.must("", "show", toSeat); !strings.Contains(out, "[main → row-7ah, ") {
		t.Errorf("an entry's line should name the shelf it went to, got:\n%s", out)
	}

	if out := other.must("", "follow", "infra"); !strings.Contains(out, "row-7ah follows infra") {
		t.Errorf("follow should say what the shelf follows, got %q", out)
	}

	other.must("", "release")
	outside.must("", "claim", "main")
	followed := outside.file("Staging DB moved to db2", "infra news", "--topic", "infra")

	if out := h.must("", "search", "--term", "Staging"); !strings.Contains(out, followed) {
		t.Errorf("a topic one seat on a shelf follows should reach every seat on it, got:\n%s", out)
	}
}

func TestARenameTakesTheSeatsOwnShelf(t *testing.T) {
	h, alone := rowShelf(t)
	h.must("", "release")
	alone.must("", "release")
	alone.must("", "claim", "main")
	alone.must("", "rename", "haupt")

	shelves, _, err := seat.LoadShelves(h.store())
	if err != nil {
		t.Fatal(err)
	}

	if _, renamed := shelves.IDOf("haupt"); !renamed {
		t.Error("a rename should rename the seat's own shelf with it")
	}

	if got := alone.run("", "rename", "row-7ah"); got.code != 1 || !strings.Contains(got.err, "row-7ah already names a shelf") {
		t.Errorf("a rename to another shelf's name should be refused, got %d %q", got.code, got.err)
	}

	alone.must("", "release")
	h.must("", "claim", "impl")
	h.must("", "rename", "implementer")

	shelves, _, _ = seat.LoadShelves(h.store())
	if _, kept := shelves.IDOf("row-7ah"); !kept {
		t.Error("a rename should leave a shared shelf its own name")
	}
}

func TestAFormat2StoreGetsAShelfPerSeat(t *testing.T) {
	h := newHarness(t)
	test.ClaimSeatByID(t, h.store(), "s-aaaaaa", "main", test.Session)
	test.WriteFile(t, filepath.Join(h.store(), "seats.json"), `{"s-aaaaaa": {"name": "main", "follows": ["infra"]}}`)

	test.WriteFile(t, filepath.Join(h.store(), "log", "tail.jnl"), "")
	before := h.logBytes()

	h.must("", "topics")

	format, _ := os.ReadFile(filepath.Join(h.store(), "format.json"))
	if !strings.Contains(string(format), `"format":7`) {
		t.Fatalf("a format-2 store should migrate to the current format, got %s", format)
	}

	shelves, found, err := seat.LoadShelves(h.store())
	if err != nil || !found {
		t.Fatalf("the migration should write shelves.json: %v", err)
	}

	shelf, named := shelves.IDOf("main")
	if !named || !seat.IsShelfID(shelf) || strings.Join(shelves.Shelves[shelf].Follows, ",") != "infra" {
		t.Errorf("every seat should get a shelf of its own under its name, its follows moved there, got %+v", shelves.Shelves)
	}

	registry, _, _ := seat.LoadRegistry(h.store())
	if registry.Seats["s-aaaaaa"].Shelf != shelf || len(registry.Seats["s-aaaaaa"].Follows) != 0 {
		t.Errorf("the seat should name its shelf and keep no follows of its own, got %+v", registry.Seats["s-aaaaaa"])
	}

	backups, _ := filepath.Glob(filepath.Join(h.store(), "backup", "format-2-*", "seats.json"))
	if len(backups) != 1 {
		t.Errorf("the migration should back up seats.json first, got %v", backups)
	}

	if !h.logUnchanged(before) {
		t.Error("the migration should leave the log untouched")
	}
}
