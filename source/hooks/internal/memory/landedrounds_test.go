package memory_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestASeenEntryStillCarriesItsLandedFlag(t *testing.T) {
	h := newLandedHarness(t)
	h.must("", "claim", "main")
	id := h.file("Rework the waiter loop", "body", "--kind", "todo", "--item", "settle it", "--about", "README.md")
	h.must("", "brief")

	h.land(test.Commit{SHA: "4a4a4a4a4a4a4a4a", Message: "Readme", Paths: []string{"README.md"}})
	h.subagentHook("git pull")

	if line := briefLine(h.must("", "brief"), id); !strings.HasSuffix(line, "retire? landed: 4a4a4a4a Readme; 0/1") {
		t.Errorf("an entry whose line the context already holds should still print with its landed flag:\n%s", line)
	}
}

func TestACredentialSubjectNeverReachesTheEmbedder(t *testing.T) {
	credential := test.SecretFixture(t, "positive/github-pat.txt")
	token := credential[strings.Index(credential, "ghp_"):strings.LastIndex(credential, `"`)]

	h := newLandedHarness(t)
	h.must("", "claim", "main")
	h.file("Rework the waiter loop", "body", "--kind", "todo", "--item", "settle it")

	h.land(test.Commit{SHA: "4b4b4b4b4b4b4b4b", Message: "Rotate " + token})
	h.runPrompt()

	if !strings.Contains(strings.Join(h.embedder.Texts(), "\n"), "Rework the waiter loop") {
		t.Fatalf("the round should have run its subject test")
	}

	for _, text := range h.embedder.Texts() {
		if strings.Contains(text, token) {
			t.Fatalf("a subject holding a credential should never be embedded")
		}
	}
}

func TestADeadRoundsLockIsTakenOver(t *testing.T) {
	h := newLandedHarness(t)
	h.starter.Run = nil
	h.must("", "claim", "main")
	h.file("Rework the waiter loop", "body", "--kind", "todo", "--item", "settle it")

	h.runPrompt()

	locks, _ := filepath.Glob(filepath.Join(h.cacheDir(), "landed-scan-*.lock"))
	if len(locks) != 1 {
		t.Fatalf("a started round should hold its lock: %v", locks)
	}

	h.bashHook("git commit -m one")

	if starts := h.starter.Starts(); len(starts) != 1 {
		t.Fatalf("a lock a live round holds should start no other: %v", starts)
	}

	old := time.Now().Add(-time.Hour)

	err := os.Chtimes(locks[0], old, old)
	if err != nil {
		t.Fatal(err)
	}

	h.bashHook("git commit -m two")

	if starts := h.starter.Starts(); len(starts) != 2 {
		t.Errorf("a lock a dead round left should be taken over: %v", starts)
	}
}

func TestASubjectTheEmbedderFailedToJudgeIsJudgedByTheNextRound(t *testing.T) {
	h := newLandedHarness(t)
	h.must("", "claim", "main")
	id := h.file("Rework waiter polling loop deadline", "waiter polling loop deadline", "--kind", "todo", "--item", "settle it")

	h.embedder.Err = errors.New("connection refused")
	h.land(test.Commit{SHA: "c5c5c5c5c5c5c5c5", Message: "Rework waiter polling loop deadline"})

	if line := landedLine(h.runPrompt()); strings.Contains(line, id+" by c5c5c5c5") {
		t.Fatalf("a round whose embedder failed should match nothing by subject:\n%s", line)
	}

	h.embedder.Err = nil
	again := h.bashHook("git pull") + "\n" + h.runPrompt()

	if line := landedLine(again); !strings.Contains(line, id+" by c5c5c5c5 ") || !strings.Contains(line, "(subject)") {
		t.Errorf("a commit whose subject the embedder failed to judge should be judged by the next round:\n%s", again)
	}
}

func TestARoundWithoutAnEmbedderMovesItsMarkPastEveryCommit(t *testing.T) {
	h := newLandedHarness(t)
	h.must("", "claim", "main")
	h.file("Rework waiter polling loop deadline", "waiter polling loop deadline", "--kind", "todo", "--item", "settle it")

	h.embedding = false
	h.land(test.Commit{SHA: "c5c5c5c5c5c5c5c5", Message: "Rework waiter polling loop deadline"})
	h.runPrompt()

	h.land(test.Commit{SHA: "c6c6c6c6c6c6c6c6", Message: "Tidy"})
	h.bashHook("git pull")

	reads := h.logReads()
	if last := reads[len(reads)-1]; last[len(last)-1] != "c5c5c5c5c5c5c5c5..HEAD" {
		t.Errorf("a round no embedder is configured for should leave no commit behind its mark: %v", reads)
	}
}
