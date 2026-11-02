package memory_test

import (
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestASessionEndClearsTheStateNothingReadsAfterIt(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.must(h.promptOf("start"), "prompt")
	h.putSlot("waiter/"+test.Session, "1 2")
	h.putSlot("graph-seen/"+test.Session, "{}")
	h.putSlot("fork/"+test.Session, "{}")
	h.putSlot("ladder/another-session", "{}")

	if h.slot("ladder/"+test.Session) == "" {
		t.Fatal("a seated prompt should keep the session's ladder")
	}

	h.must(h.promptOf(""), "session-end")

	for _, purpose := range []string{"ladder/", "waiter/", "graph-seen/", "fork/"} {
		if h.slot(purpose+test.Session) != "" {
			t.Errorf("a session's end should clear its %s state", purpose)
		}
	}

	if h.slot("ladder/another-session") == "" {
		t.Error("a session's end should leave every other session's state")
	}
}

func TestTheStateOfASeatGoneFromTheRegistryIsReclaimed(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	main := seatID(t, h.store(), "main")

	registry, _, err := seat.LoadRegistry(h.store())
	if err != nil {
		t.Fatalf("reading the registry: %v", err)
	}

	registry.Seats["s-expird"] = seat.Entry{Name: "job", Expired: true}

	err = registry.Save(h.roots().Data)
	if err != nil {
		t.Fatalf("writing the registry: %v", err)
	}

	for _, purpose := range []string{"traps/", "do-now/", "skipped/", "brief/", "seen/", "ledger-ended/"} {
		h.putSlot(purpose+main, "{}")
		h.putSlot(purpose+"s-purged", "{}")
		h.putSlot(purpose+"s-expird", "{}")
	}

	h.putSlot("seen/agent/a-subagent", "{}")
	h.putSlot("brief/not-an-id", "{}")

	h.must(h.sessionStart("startup"), "session-start")

	for _, purpose := range []string{"traps/", "do-now/", "skipped/", "brief/", "seen/", "ledger-ended/"} {
		if h.slot(purpose+main) == "" {
			t.Errorf("a live seat's %s state should stay", purpose)
		}

		if h.slot(purpose+"s-purged") != "" || h.slot(purpose+"s-expird") != "" {
			t.Errorf("the %s state of a seat purged or expired should be reclaimed", purpose)
		}
	}

	if h.slot("seen/agent/a-subagent") == "" || h.slot("brief/not-an-id") == "" {
		t.Error("a key past a seat purpose that names no seat id should stay, a subagent's young set too")
	}
}

func TestAnUnreadableRegistryReclaimsNoSeatsState(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	main := seatID(t, h.store(), "main")
	h.putSlot("brief/"+main, "{}")

	test.WriteFile(t, seat.RegistryPath(h.store()), "not json")

	h.run(h.sessionStart("startup"), "session-start")

	if h.slot("brief/"+main) == "" {
		t.Error("a registry that cannot be read should leave every seat's state, since no seat can be told gone")
	}
}
