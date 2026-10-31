package memory_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

// sessionTranscript copies the fixtures, in order, into one transcript file.
func sessionTranscript(t *testing.T, fixtures ...string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "session.jsonl")

	content := ""
	for _, fixture := range fixtures {
		content += test.Fixture(t, "memory/inherit/"+fixture)
	}

	test.WriteFile(t, path, content)

	return path
}

func (h *harness) startHook(source string, transcriptPath string) string {
	data, _ := json.Marshal(map[string]string{"session_id": h.session, "source": source, "transcript_path": transcriptPath})

	return string(data)
}

func (h *harness) promptIn(transcriptPath string) string {
	data, _ := json.Marshal(map[string]string{"session_id": h.session, "prompt": "go on", "transcript_path": transcriptPath})

	return string(data)
}

func (h *harness) toolIn(transcriptPath string) string {
	data, _ := json.Marshal(map[string]any{
		"session_id": h.session, "transcript_path": transcriptPath,
		"tool_name": "Bash", "tool_input": map[string]string{"command": "ls"},
	})

	return string(data)
}

func (h *harness) forkUUID(name string) string {
	h.t.Helper()

	var claim struct {
		ForkUUID string `json:"fork_uuid"`
	}

	data, err := os.ReadFile(h.seatFile(name, ".json"))
	if err != nil {
		h.t.Fatalf("reading the claim: %v", err)
	}

	err = json.Unmarshal(data, &claim)
	if err != nil {
		h.t.Fatalf("decoding the claim: %v", err)
	}

	return claim.ForkUUID
}

// seatedParent is a session holding main whose transcript a hook has read.
func seatedParent(t *testing.T, transcriptPath string) *harness {
	t.Helper()

	parent := newHarness(t)
	parent.must("", "claim", "main")
	parent.file("Gate reads Bash only today", "finding")
	parent.must(parent.promptIn(transcriptPath), "prompt")

	return parent
}

// resumedOrForked is a new claude process on the parent's store, under
// session.
func resumedOrForked(parent *harness, session string) *harness {
	child := newHarnessSharing(parent)
	child.session = session

	return child
}

func TestAClaimRecordsTheUUIDAForkWouldStartWith(t *testing.T) {
	path := sessionTranscript(t, "parent.jsonl")
	parent := seatedParent(t, path)

	if got := parent.forkUUID("main"); got != "5d1c0f6e-parent-first" {
		t.Errorf("a claim should record its transcript's first message uuid, got %q", got)
	}

	parent.must(parent.startHook("compact", path), "session-start")

	if got := parent.forkUUID("main"); got != "" {
		t.Errorf("a compaction should empty the fork uuid until its boundary is in the transcript, got %q", got)
	}

	test.WriteFile(t, path, test.Fixture(t, "memory/inherit/parent.jsonl")+test.Fixture(t, "memory/inherit/boundary.jsonl"))
	parent.must(parent.promptIn(path), "prompt")

	if got := parent.forkUUID("main"); got != "7b2e4c90-parent-boundary" {
		t.Errorf("the first hook after a compaction should record the boundary's uuid, got %q", got)
	}
}

func TestAResumedSessionWhoseParentIsGoneTakesItsSeatOver(t *testing.T) {
	path := sessionTranscript(t, "parent.jsonl")
	parent := seatedParent(t, path)
	parent.processes.Kill(test.OwnerPID)

	resumed := resumedOrForked(parent, test.Session)

	out := resumed.must(resumed.startHook("resume", path), "session-start")
	if !strings.HasPrefix(out, "agentic-memory: seat main, ") || !strings.Contains(out, "Gate reads Bash only today") {
		t.Errorf("a resumed session should hold its parent's seat with the brief, got:\n%s", out)
	}

	if strings.Contains(out, "holds no seat") {
		t.Errorf("a resumed session taking its seat over should not be told it holds none, got:\n%s", out)
	}

	view, code := resumed.holderJSON("main", "--json")
	if code != 0 || view["alive"] != true || view["pid"] != float64(test.OtherOwnerPID) {
		t.Errorf("the resumed session's process should hold the seat, got %d %v", code, view)
	}
}

func TestAForkOfARunningSessionGetsASeatOnItsShelf(t *testing.T) {
	parent := seatedParent(t, sessionTranscript(t, "parent.jsonl"))
	fork := resumedOrForked(parent, test.OtherSession)

	out := fork.must(fork.startHook("resume", sessionTranscript(t, "fork.jsonl")), "session-start")
	if !strings.HasPrefix(out, "agentic-memory: seat main-2 ") || !strings.Contains(out, "Gate reads Bash only today") {
		t.Errorf("a fork of a running session should get a new seat on its parent's shelf, with its brief, got:\n%s", out)
	}

	if shelfID(t, parent.store(), "main-2") != shelfID(t, parent.store(), "main") {
		t.Error("the fork's seat should file onto its parent's shelf")
	}

	view, code := parent.holderJSON("main", "--json")
	if code != 0 || view["session_id"] != test.Session || view["alive"] != true {
		t.Errorf("the running parent should keep its seat, got %d %v", code, view)
	}
}

func TestAForkOfACompactedSessionFindsItsParentByTheBoundary(t *testing.T) {
	path := sessionTranscript(t, "parent.jsonl")
	parent := seatedParent(t, path)
	parent.must(parent.startHook("compact", path), "session-start")
	test.WriteFile(t, path, test.Fixture(t, "memory/inherit/parent.jsonl")+test.Fixture(t, "memory/inherit/boundary.jsonl"))
	parent.must(parent.promptIn(path), "prompt")
	parent.processes.Kill(test.OwnerPID)

	fork := resumedOrForked(parent, test.OtherSession)

	out := fork.must(fork.startHook("resume", sessionTranscript(t, "fork-compacted.jsonl")), "session-start")
	if !strings.HasPrefix(out, "agentic-memory: seat main, ") {
		t.Errorf("a fork of a compacted session whose parent is gone should take the parent's seat over, got:\n%s", out)
	}
}

func TestASessionMatchingNoClaimIsToldItHoldsNoSeat(t *testing.T) {
	cases := []struct {
		name       string
		source     string
		transcript string
		filedFrom  string
	}{
		{name: "a transcript another session never held", source: "startup", transcript: "stranger.jsonl", filedFrom: "parent.jsonl"},
		{name: "a claim from a release recording no fork uuid", source: "startup", transcript: "fork.jsonl", filedFrom: ""},
		{name: "a fork whose transcript is already written", source: "fork", transcript: "stranger.jsonl", filedFrom: "parent.jsonl"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parent := newHarness(t)
			parent.must("", "claim", "main")

			if tc.filedFrom != "" {
				parent.must(parent.promptIn(sessionTranscript(t, tc.filedFrom)), "prompt")
			}

			parent.processes.Kill(test.OwnerPID)

			stranger := resumedOrForked(parent, test.OtherSession)

			out := stranger.must(stranger.startHook(tc.source, sessionTranscript(t, tc.transcript)), "session-start")
			if !strings.Contains(out, "agentic-memory: this session holds no seat. Seats: main (free, ") {
				t.Errorf("a session matching no claim should get the seatless notice, got:\n%s", out)
			}

			if _, code := stranger.holderJSON("main", "--json"); code != 1 {
				t.Error("a session matching no claim should leave the seat free")
			}
		})
	}
}

func TestAClaimWithoutAForkUUIDStillMatchesItsSessionID(t *testing.T) {
	parent := newHarness(t)
	parent.must("", "claim", "main")
	parent.processes.Kill(test.OwnerPID)

	resumed := resumedOrForked(parent, test.Session)

	if out := resumed.must(resumed.startHook("resume", ""), "session-start"); !strings.HasPrefix(out, "agentic-memory: seat main, ") {
		t.Errorf("a resume should take over a claim carrying its session id, fork uuid or not, got:\n%s", out)
	}
}

func TestAResumedForkTakesItsOwnSeatOverItsParents(t *testing.T) {
	parent := seatedParent(t, sessionTranscript(t, "parent.jsonl"))
	fork := resumedOrForked(parent, test.OtherSession)
	fork.must(fork.startHook("resume", sessionTranscript(t, "fork.jsonl")), "session-start")
	fork.must("", "release")

	parent.clock.Advance(time.Hour)
	parent.must("", "release")
	parent.must("", "claim", "main")
	parent.must(parent.promptIn(sessionTranscript(t, "parent.jsonl")), "prompt")
	parent.processes.Kill(test.OwnerPID)

	if out := fork.must(fork.startHook("resume", sessionTranscript(t, "fork.jsonl")), "session-start"); !strings.HasPrefix(out, "agentic-memory: seat main-2 ") {
		t.Errorf("a resumed fork should take its own seat over, not the parent's it shares a fork uuid with, got:\n%s", out)
	}
}

// unwrittenTranscript is a path Claude Code has not written yet, as a fork's
// is while its SessionStart hooks run.
func unwrittenTranscript(t *testing.T) string {
	t.Helper()

	return filepath.Join(t.TempDir(), "fork.jsonl")
}

// forkBeforeItsTranscript is a fork whose SessionStart and first prompt ran
// as Claude Code runs them: before the fork's transcript is written. It
// answers the transcript's path, still unwritten.
func forkBeforeItsTranscript(t *testing.T, fork *harness) string {
	t.Helper()

	path := unwrittenTranscript(t)

	if out := fork.must(fork.startHook("fork", path), "session-start"); out != "" {
		t.Errorf("a fork whose transcript is not written yet should hear nothing at SessionStart, got:\n%s", out)
	}

	if out := fork.must(fork.promptIn(path), "prompt"); out != "" {
		t.Errorf("a fork whose transcript is not written yet should hear nothing at its first prompt, got:\n%s", out)
	}

	return path
}

func TestAForkIsSeatedAtTheFirstToolCallAfterItsTranscriptIsWritten(t *testing.T) {
	cases := []struct {
		name       string
		parentGone bool
		seat       string
	}{
		{name: "beside a running parent", seat: "main-2"},
		{name: "after its parent ended", parentGone: true, seat: "main"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parent := seatedParent(t, sessionTranscript(t, "parent.jsonl"))
			if tc.parentGone {
				parent.processes.Kill(test.OwnerPID)
			}

			fork := resumedOrForked(parent, test.OtherSession)
			path := forkBeforeItsTranscript(t, fork)

			if _, code := fork.holderJSON(tc.seat, "--json"); code == 0 && !tc.parentGone {
				t.Error("a fork should hold no seat before its transcript is written")
			}

			test.WriteFile(t, path, test.Fixture(t, "memory/inherit/fork.jsonl"))

			out := toolContext(fork.must(fork.toolIn(path), "tool"))
			if !claimedAs(out, tc.seat) || !strings.Contains(out, "Gate reads Bash only today") {
				t.Errorf("a fork should be seated with its brief at its first tool call, got:\n%s", out)
			}

			view, code := fork.holderJSON(tc.seat, "--json")
			if code != 0 || view["session_id"] != test.OtherSession {
				t.Errorf("the fork should hold its seat after its first tool call, got %d %v", code, view)
			}

			if again := fork.must(fork.toolIn(path), "tool"); strings.Contains(again, "Gate reads Bash only today") {
				t.Errorf("a seated fork should get its brief once, got:\n%s", again)
			}

			if tc.parentGone {
				return
			}

			view, code = parent.holderJSON("main", "--json")
			if code != 0 || view["session_id"] != test.Session || view["alive"] != true {
				t.Errorf("the running parent should keep its seat, got %d %v", code, view)
			}
		})
	}
}

func TestAForkWithoutAToolCallIsSeatedAtItsNextPrompt(t *testing.T) {
	parent := seatedParent(t, sessionTranscript(t, "parent.jsonl"))
	fork := resumedOrForked(parent, test.OtherSession)
	path := forkBeforeItsTranscript(t, fork)

	test.WriteFile(t, path, test.Fixture(t, "memory/inherit/fork.jsonl"))

	out := fork.must(fork.promptIn(path), "prompt")
	if !strings.HasPrefix(out, "agentic-memory: seat main-2 ") || !strings.Contains(out, "Gate reads Bash only today") {
		t.Errorf("a fork should be seated with its brief at the first prompt finding its transcript, got:\n%s", out)
	}

	if view, code := fork.holderJSON("main-2", "--json"); code != 0 || view["session_id"] != test.OtherSession {
		t.Errorf("the fork should hold its seat after that prompt, got %d %v", code, view)
	}
}

func TestAForkMatchingNoClaimIsToldOnceItsTranscriptIsWritten(t *testing.T) {
	parent := seatedParent(t, sessionTranscript(t, "parent.jsonl"))
	fork := resumedOrForked(parent, test.OtherSession)
	path := forkBeforeItsTranscript(t, fork)

	test.WriteFile(t, path, test.Fixture(t, "memory/inherit/stranger.jsonl"))

	if out := toolContext(fork.must(fork.toolIn(path), "tool")); !strings.Contains(out, "agentic-memory: this session holds no seat. Seats: main (") {
		t.Errorf("a fork matching no claim should get the seatless notice at the first hook finding its transcript, got:\n%s", out)
	}

	if out := fork.must(fork.promptIn(path), "prompt"); out != "" {
		t.Errorf("a fork should hear the seatless notice once, got:\n%s", out)
	}

	if out := fork.must(fork.toolIn(path), "tool"); out != "" {
		t.Errorf("a fork should hear the seatless notice once, got:\n%s", out)
	}
}

func TestAForkClaimingBeforeItsFirstPromptKeepsItsSeat(t *testing.T) {
	parent := seatedParent(t, sessionTranscript(t, "parent.jsonl"))
	fork := resumedOrForked(parent, test.OtherSession)
	path := unwrittenTranscript(t)

	fork.must(fork.startHook("fork", path), "session-start")
	fork.must("", "claim", "review")

	if out := fork.must(fork.promptIn(path), "prompt"); strings.Contains(out, "holds no seat") || strings.Contains(out, "main-2") {
		t.Errorf("a fork holding its own seat should be neither seated again nor told it holds none, got:\n%s", out)
	}

	if view, code := fork.holderJSON("review", "--json"); code != 0 || view["session_id"] != test.OtherSession {
		t.Errorf("a fork's own claim should stand at its first prompt, got %d %v", code, view)
	}

	if _, code := fork.holderJSON("main-2", "--json"); code != 1 {
		t.Error("a fork holding its own seat should get no seat on its parent's shelf")
	}

	fork.must("", "release")
	test.WriteFile(t, path, test.Fixture(t, "memory/inherit/fork.jsonl"))

	if out := fork.must(fork.promptIn(path), "prompt"); out != "" {
		t.Errorf("the first prompt should have removed the fork's marker, got:\n%s", out)
	}
}

func TestAHookWithoutASessionIDLeavesForksAlone(t *testing.T) {
	parent := seatedParent(t, sessionTranscript(t, "parent.jsonl"))
	fork := resumedOrForked(parent, test.OtherSession)
	path := unwrittenTranscript(t)
	fork.must(fork.startHook("fork", path), "session-start")
	test.WriteFile(t, path, test.Fixture(t, "memory/inherit/fork.jsonl"))

	anonymous := resumedOrForked(parent, "")

	if out := anonymous.must(anonymous.startHook("fork", unwrittenTranscript(t)), "session-start"); !strings.Contains(out, "holds no seat") {
		t.Errorf("a fork start naming no session should get the seatless notice, got:\n%s", out)
	}

	if out := anonymous.must(anonymous.promptIn(path), "prompt"); out != "" {
		t.Errorf("a prompt naming no session should match no fork, got:\n%s", out)
	}

	if out := fork.must(fork.promptIn(path), "prompt"); !strings.HasPrefix(out, "agentic-memory: seat main-2 ") {
		t.Errorf("a fork should still be seated at its first prompt, got:\n%s", out)
	}
}

func TestAForkMarkerNobodyPromptedIsSweptAWeekOn(t *testing.T) {
	parent := seatedParent(t, sessionTranscript(t, "parent.jsonl"))
	fork := resumedOrForked(parent, test.OtherSession)
	path := unwrittenTranscript(t)
	fork.must(fork.startHook("fork", path), "session-start")
	test.WriteFile(t, path, test.Fixture(t, "memory/inherit/fork.jsonl"))

	ageSlots(t, parent.cacheDir(), "fork/", parent.clock.Now().Add(-8*24*time.Hour))
	parent.must(parent.startHook("resume", ""), "session-start")

	if out := fork.must(fork.promptIn(path), "prompt"); out != "" {
		t.Errorf("a fork marker older than a week should be swept, got:\n%s", out)
	}
}
