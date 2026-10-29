package memory_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func (h *harness) sensorPath(session string) string {
	return filepath.Join(h.roots().Sessions("claude"), session, "context.json")
}

func exists(path string) bool {
	_, err := os.Stat(path)

	return err == nil
}

func TestAnEndingSessionLeavesNoContextReading(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.sensor(40)

	other := h.sensorPath(test.OtherSession)
	test.WriteFile(t, other, `{"used_percentage":10}`)

	h.must(h.sessionEnd("logout"), "session-end")

	if exists(h.sensorPath(h.session)) {
		t.Error("an ending session's context reading should be removed")
	}

	if !exists(other) {
		t.Error("another session's context reading should stay")
	}
}

func TestASessionIDNamingAPathRemovesNoReading(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	outside := filepath.Join(h.roots().Data, "sessions", "outside", "context.json")
	test.WriteFile(t, outside, "{}")

	data, _ := json.Marshal(map[string]string{"session_id": "../outside", "reason": "logout"})
	h.must(string(data), "session-end")

	if !exists(outside) {
		t.Error("a session id naming a path should reach no file outside the readings")
	}
}

func TestSessionStartSweepsStaleReadingsInEveryProject(t *testing.T) {
	h := newHarness(t)

	stale := h.sensorPath("stale")
	fresh := h.sensorPath("fresh")
	now := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)

	for path, age := range map[string]time.Duration{stale: 6 * time.Minute, fresh: time.Minute} {
		test.WriteFile(t, path, "{}")

		err := os.Chtimes(path, now.Add(-age), now.Add(-age))
		if err != nil {
			t.Fatal(err)
		}
	}

	h.must(hookPayload("startup", "", ""), "session-start")

	if exists(stale) {
		t.Error("a reading too old to be read should be swept, even where this project has no store")
	}

	if !exists(fresh) {
		t.Error("a reading still young enough to be read should stay")
	}
}
