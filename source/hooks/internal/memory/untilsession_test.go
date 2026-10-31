package memory_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

const clearedSession = "c1ea4ed0-0000-4000-8000-000000000000"

// sessionStart is the SessionStart payload of the harness's session from source.
func (h *harness) sessionStart(source string) string {
	data, _ := json.Marshal(map[string]string{"session_id": h.session, "source": source})

	return string(data)
}

// cleared is the harness as its process runs on after a `/clear`: the same
// process under a new session id.
func cleared(h *harness) *harness {
	h.must(h.sessionEnd("clear"), "session-end")

	after := *h
	after.session = clearedSession
	after.must(after.sessionStart("clear"), "session-start")

	return &after
}

func TestASessionEndIsReachedWhenTheFilingClaimEnds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		reach   func(h *harness) *harness
		retired bool
	}{
		{name: "a release", retired: true, reach: func(h *harness) *harness {
			h.must("", "release")

			return h
		}},
		{name: "the session's end", retired: true, reach: func(h *harness) *harness {
			h.must(h.sessionEnd("prompt_input_exit"), "session-end")

			return h
		}},
		{name: "a claim taking the seat of a gone holder", retired: true, reach: func(h *harness) *harness {
			h.processes.Kill(test.OwnerPID)

			other := newHarnessSharing(h)
			other.clock.Advance(time.Minute)
			other.must("", "claim", "main")

			return other
		}},
		{name: "a /clear", reach: func(h *harness) *harness {
			after := cleared(h)
			if holder := after.must("", "holder", "--session", clearedSession, "--json"); !strings.Contains(holder, `"seat":"main"`) {
				h.t.Fatalf("a /clear should move the claim to the new session id, got %s", holder)
			}

			after.must("", "brief")

			return after
		}},
		{name: "a /resume", reach: func(h *harness) *harness {
			h.must(h.sessionEnd("resume"), "session-end")
			h.must("", "brief")

			return h
		}},
		{name: "the holder claiming its own seat again", reach: func(h *harness) *harness {
			h.clock.Advance(time.Minute)
			h.must("", "claim", "main")

			return h
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")
			id := h.file("Gate scratch for this session", "body", "--kind", "todo", "--until", "session")

			checker := tc.reach(h)

			shown := checker.must("", "show", id)
			if strings.Contains(shown, "end reached: session") != tc.retired {
				t.Errorf("a session end should retire the entry exactly when the claim it was filed under ended:\n%s", shown)
			}
		})
	}
}

func TestASessionEndUnderAClaimWithoutAStampEndsAtRelease(t *testing.T) {
	h := newHarness(t)
	test.ClaimSeatByID(t, h.store(), "s-aaaaaa", "main", test.Session)
	id := h.file("Gate scratch under an earlier claim", "body", "--until", "session")
	h.must("", "brief")

	if shown := h.must("", "show", id); strings.Contains(shown, "end reached") {
		t.Fatalf("a claim an earlier release wrote should hold the entry while it stands:\n%s", shown)
	}

	h.must("", "release")

	if shown := h.must("", "show", id); !strings.Contains(shown, "end reached: session") {
		t.Errorf("releasing a claim an earlier release wrote should end the entry:\n%s", shown)
	}
}

func TestASessionEndLeavesAnotherClaimsEntries(t *testing.T) {
	h, other := rowShelf(t)
	mine := h.file("Gate scratch of impl's session", "body", "--until", "session")
	theirs := other.file("Gate scratch of rev's session", "body", "--until", "session")

	h.must("", "release")

	if live := other.must("", "search", "--status", "live"); !strings.Contains(live, theirs) || strings.Contains(live, mine) {
		t.Errorf("a release should end its own claim's session entries only, on a shared shelf too:\n%s", live)
	}
}

func TestASessionEndIsListedAndRevives(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	id := h.file("Gate scratch for this session", "body", "--until", "session")
	h.must("", "release")

	h.clock.Advance(time.Hour)

	claimed := h.must("", "claim", "main")
	if !strings.Contains(claimed, "ended since last brief (revive to undo):\n  "+id+" Gate scratch for this session: session") {
		t.Errorf("the next brief should list the entry the session end retired:\n%s", claimed)
	}

	h.must("", "revive", id)

	if live := h.must("", "search", "--status", "live"); !strings.Contains(live, id) {
		t.Errorf("revive should restore an entry a session end retired:\n%s", live)
	}

	h.clock.Advance(time.Minute)
	h.must("", "brief")

	if live := h.must("", "search", "--status", "live"); !strings.Contains(live, id) {
		t.Errorf("a revive should bind the entry to the reviving claim, so the next brief keeps it:\n%s", live)
	}

	h.must("", "release")

	if retired := h.must("", "search", "--status", "retired", "--seat", "main"); !strings.Contains(retired, id) {
		t.Errorf("the revived entry should end with the claim that revived it:\n%s", retired)
	}
}

func TestARuleTakesNoSessionEnd(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	got := h.run("body", "new", "--title", "Owner rule for this session", "--kind", "rule", "--from", "user", "--until", "session")
	if got.code != 1 || !strings.Contains(got.err, "a rule ends only when someone retires it") {
		t.Errorf("--until session on a rule should be refused, got %d %q", got.code, got.err)
	}

	answer := h.mcp(`{"name":"new","arguments":{"title":"Gate scratch over MCP","body":"b","until":["session"]}}`)
	if strings.Contains(answer, `"isError":true`) || !strings.Contains(h.must("", "show", idPattern.FindString(answer), "--again"),
		"until: session") {
		t.Errorf("the MCP key until should take session: %s", answer)
	}
}
