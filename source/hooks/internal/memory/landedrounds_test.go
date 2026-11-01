package memory_test

import (
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
	id := h.file("Rework the waiter loop", "body", "--kind", "todo", "--about", "README.md")
	h.must("", "brief")

	h.land(test.Commit{SHA: "4a4a4a4a4a4a4a4a", Message: "Readme", Paths: []string{"README.md"}})
	h.subagentHook("git pull")

	if line := briefLine(h.must("", "brief"), id); !strings.HasSuffix(line, "retire? landed: 4a4a4a4a Readme") {
		t.Errorf("an entry whose line the context already holds should still print with its landed flag:\n%s", line)
	}
}

func TestACredentialSubjectNeverReachesTheEmbedder(t *testing.T) {
	credential := test.SecretFixture(t, "positive/github-pat.txt")
	token := credential[strings.Index(credential, "ghp_"):strings.LastIndex(credential, `"`)]

	h := newLandedHarness(t)
	h.must("", "claim", "main")
	h.file("Rework the waiter loop", "body", "--kind", "todo")

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
	h.file("Rework the waiter loop", "body", "--kind", "todo")

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
