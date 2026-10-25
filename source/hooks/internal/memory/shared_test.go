package memory_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

// twoSeats is main held by the harness session and idea by the other one.
// stop is the payload a Stop hook gets for the harness's own session.
func (h *harness) stop() string {
	data, _ := json.Marshal(map[string]string{"session_id": h.session})

	return string(data)
}

func twoSeats(t *testing.T) (*harness, *harness) {
	t.Helper()

	h := newHarness(t)
	h.must("", "claim", "main")

	other := newHarnessSharing(h)
	other.must("", "claim", "idea")

	return h, other
}

func TestAnEntryNamesWhoItIsFor(t *testing.T) {
	h, other := twoSeats(t)
	other.must("", "release")
	newHarnessSharing(h).must("", "claim", "review")
	newHarnessSharing(h).must("", "release")
	other.must("", "claim", "idea")

	named := h.file("Review owes the API diff", "a handover", "--for", "idea", "--for", "review")
	everyone := h.file("Staging DB moved to db2", "a changed fact", "--for", "all")

	tail, _ := os.ReadFile(filepath.Join(h.store(), "log", "tail.jsonl"))
	if !strings.Contains(string(tail), `"for":["`+seatID(t, h.store(), "idea")+`","`+seatID(t, h.store(), "review")+`"]`) ||
		!strings.Contains(string(tail), `"for":"all"`) {
		t.Errorf("--for should store the named seats by id, and all as all, got %s", tail)
	}

	if out := h.must("", "show", named); !strings.Contains(out, "[main → idea, review, ") {
		t.Errorf("an entry's line should name who it is for after its filer, got:\n%s", out)
	}

	if out := h.must("", "show", everyone); !strings.Contains(out, "[main → all, ") {
		t.Errorf("an entry for every seat should say so, got:\n%s", out)
	}

	if got := h.run("note", "new", "--title", "For a seat that does not exist", "--topic", "gate", "--for", "nobody"); got.code != 1 ||
		!strings.Contains(got.err, "--for names no seat called nobody") {
		t.Errorf("--for should refuse a seat name no seat has, got %d %q", got.code, got.err)
	}

	if got := h.run("", "rename", "all"); got.code != 1 || !strings.Contains(got.err, `"all" is the word --for takes for every seat`) {
		t.Errorf("a rename to all should be refused, all being reserved, got %d %q", got.code, got.err)
	}

	h.must("", "release")

	if got := h.run("", "claim", "all"); got.code != 1 || !strings.Contains(got.err, `"all" is the word --for takes for every seat`) {
		t.Errorf("a claim of all should be refused, all being reserved, got %d %q", got.code, got.err)
	}
}

func TestAReceivedEntryStandsWithTheSeatsOwn(t *testing.T) {
	h, other := twoSeats(t)
	id := h.file("Review owes the API diff", "a handover for idea", "--for", "idea")

	if out := other.must("", "search", "--term", "handover"); !strings.Contains(out, id) {
		t.Errorf("a received entry should be found by the receiver's default search, got:\n%s", out)
	}

	if out := other.must("", "brief"); !strings.Contains(out, id) || !strings.Contains(out, "1 live") {
		t.Errorf("a received entry should stand in the receiver's brief, got:\n%s", out)
	}

	if out := h.must("", "brief"); !strings.Contains(out, id) {
		t.Errorf("the filer should keep the entry as its own, got:\n%s", out)
	}

	if got := other.run("", "retire", id); got.code != 1 || !strings.Contains(got.err, id+" belongs to main") {
		t.Errorf("a receiver should not retire an entry it did not file, got %d %q", got.code, got.err)
	}

	other.must("", "supersede", id, "--because", "done elsewhere")

	everyone := h.file("Staging DB moved to db2", "a changed fact for every seat", "--for", "all")

	if out := other.must("", "search", "--term", "changed fact"); !strings.Contains(out, everyone) {
		t.Errorf("an entry for every seat should reach every other seat, got:\n%s", out)
	}
}

func TestTheFilerHearsNothingOfItsOwnEntry(t *testing.T) {
	h, _ := twoSeats(t)
	h.must(hookPayload("", "", ""), "prompt")
	h.clock.Advance(time.Minute)
	h.file("Staging DB moved to db2", "a changed fact for every seat", "--for", "all")

	if out := h.must(hookPayload("", "", "next"), "prompt"); strings.Contains(out, "new for main") {
		t.Errorf("the filing seat should never be told its own entry arrived, got:\n%s", out)
	}
}

func TestAFollowedTopicBringsItsEntries(t *testing.T) {
	h, other := twoSeats(t)

	if out := other.must("", "follow", "infra"); !strings.Contains(out, "idea follows infra") {
		t.Errorf("follow should say what the seat follows, got %q", out)
	}

	registry, _ := os.ReadFile(filepath.Join(h.store(), "seats.json"))
	if !strings.Contains(string(registry), `"follows": [`) || !strings.Contains(string(registry), `"infra"`) {
		t.Errorf("a follow should be kept in the seat's registry entry, got %s", registry)
	}

	id := h.file("Staging DB moved to db2", "infra news", "--topic", "infra")

	if out := other.must("", "search", "--term", "Staging"); !strings.Contains(out, id) {
		t.Errorf("an entry under a followed topic should reach the follower, got:\n%s", out)
	}

	if out := h.must("", "show", id); !strings.Contains(out, "→ infra followers") {
		t.Errorf("an entry reaching seats by a follow alone should say so, got:\n%s", out)
	}

	h.must("", "merge", "infra", "infrastructure")

	if out := other.must("", "search", "--term", "Staging"); !strings.Contains(out, id) {
		t.Errorf("a follow should read through topic merges, got:\n%s", out)
	}

	other.must("", "unfollow", "infra")

	if out := other.must("", "search", "--term", "Staging"); strings.Contains(out, id) {
		t.Errorf("an unfollowed topic should stop bringing its entries, got:\n%s", out)
	}
}

func TestArrivalsAreToldOnceAtThePromptAndTheToolCall(t *testing.T) {
	for _, event := range []string{"prompt", "tool"} {
		t.Run(event, func(t *testing.T) {
			h, other := twoSeats(t)
			other.must(hookPayload("", "", ""), event)
			h.clock.Advance(time.Minute)
			id := h.file("Review owes the API diff", "a handover", "--for", "idea")

			first := other.must(hookPayload("", "", "next"), event)
			if !strings.Contains(first, "agentic-memory: new for idea (1):") || !strings.Contains(first, id) {
				t.Errorf("the next %s should carry the arrival, got:\n%s", event, first)
			}

			if event == "tool" && !strings.Contains(first, `"hookEventName":"PostToolUse","additionalContext":`) {
				t.Errorf("the tool hook should hand the arrival over as hidden context, got:\n%s", first)
			}

			if again := other.must(hookPayload("", "", "next"), event); strings.Contains(again, "new for idea") {
				t.Errorf("an arrival should be told once, got it again:\n%s", again)
			}
		})
	}
}

func TestAToolCallWithNoLogChangeReadsNoLog(t *testing.T) {
	h, other := twoSeats(t)
	other.must("", "tool")

	seen := filepath.Join(h.cache, "agentic-memory", filepath.FromSlash(h.store()[len(filepath.Join(h.home, ".claude", "agentic-memory"))+1:]),
		"briefs", seatID(t, h.store(), "idea")+".seen.json")
	past := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	err := os.Chtimes(seen, past, past)
	if err != nil {
		t.Fatalf("aging the seen mark: %v", err)
	}

	other.must("", "tool")

	info, err := os.Stat(seen)
	if err != nil || !info.ModTime().Equal(past) {
		t.Error("a tool call finding the log as it stood should read nothing and write nothing")
	}
}

func (h *harness) waiting(poll time.Duration, deadline time.Duration) *harness {
	paced := *h
	paced.waitPoll, paced.waitDeadline = poll, deadline

	return &paced
}

func TestTheWaiterWakesOnAnArrival(t *testing.T) {
	h, other := twoSeats(t)
	other.must(hookPayload("", "", ""), "tool")
	h.clock.Advance(time.Minute)

	var group sync.WaitGroup

	var got ran

	started := time.Now()

	group.Go(func() { got = other.waiting(20*time.Millisecond, 10*time.Second).run(other.stop(), "wait") })

	time.Sleep(100 * time.Millisecond)

	id := h.file("Review owes the API diff", "a handover", "--for", "idea")

	group.Wait()

	if got.code != 2 || !strings.HasPrefix(got.err, "agentic-memory: new for idea (1):") || !strings.Contains(got.err, id) || got.out != "" {
		t.Errorf("an arrival should end the wait with 2 and the arrival lines alone, got %+v", got)
	}

	if time.Since(started) > 5*time.Second {
		t.Error("the waiter should wake within a poll or two of the arrival")
	}
}

func TestTheWaiterEndsWithoutWaking(t *testing.T) {
	cases := []struct {
		name string
		end  func(h *harness, other *harness)
	}{
		{name: "the deadline passes"},
		{name: "the seat is released", end: func(_ *harness, other *harness) { other.must("", "release") }},
		{name: "the session's process ends", end: func(h *harness, _ *harness) { h.processes.Kill(test.OtherOwnerPID) }},
		{name: "the claim names another session", end: func(h *harness, _ *harness) {
			claim := filepath.Join(h.store(), "seats", seatID(t, h.store(), "idea")+".json")

			raw, _ := os.ReadFile(claim)
			test.WriteFile(t, claim, strings.Replace(string(raw), test.OtherSession, "f0f0f0f0-0000-4000-8000-000000000000", 1))
		}},
		{name: "a later waiter replaces it", end: func(_ *harness, other *harness) {
			go other.waiting(20*time.Millisecond, 300*time.Millisecond).run(other.stop(), "wait")
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, other := twoSeats(t)
			other.must(hookPayload("", "", ""), "tool")

			var group sync.WaitGroup

			var got ran

			started := time.Now()

			group.Go(func() {
				got = other.waiting(20*time.Millisecond, 500*time.Millisecond).run(other.stop(), "wait")
			})

			if tc.end != nil {
				time.Sleep(60 * time.Millisecond)
				tc.end(h, other)
			}

			group.Wait()

			if got.code != 0 || got.err != "" {
				t.Errorf("the waiter should end quietly, waking nothing, got %+v", got)
			}

			if tc.end != nil && time.Since(started) > 400*time.Millisecond {
				t.Errorf("the waiter should end within a poll of its seat or session going, took %v", time.Since(started))
			}
		})
	}
}

func TestASeatlessSessionStartsNoWaiter(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.must("", "release")

	started := time.Now()

	got := h.waiting(time.Second, time.Minute).run(h.stop(), "wait")
	if got.code != 0 || got.err != "" || time.Since(started) > 500*time.Millisecond {
		t.Errorf("a session holding no seat should not wait at all, got %+v after %v", got, time.Since(started))
	}
}
