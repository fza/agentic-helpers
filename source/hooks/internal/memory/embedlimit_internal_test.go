package memory

import (
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
)

func TestAHookWaitsNoLongerThanItsOwnDeadlineOrTheTimeout(t *testing.T) {
	cases := []struct {
		name    string
		inHook  bool
		timeout time.Duration
		want    time.Duration
	}{
		{name: "hook under a long timeout", inHook: true, timeout: 2 * time.Minute, want: embedLimit},
		{name: "hook under a short timeout", inHook: true, timeout: 5 * time.Second, want: 5 * time.Second},
		{name: "command", timeout: 2 * time.Minute, want: 2 * time.Minute},
		{name: "command without a timeout", want: 2 * time.Minute},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mem := &memory{inHook: tc.inHook, embedding: &embed.Config{Timeout: tc.timeout}}

			if mem.embedLimitFor() != tc.want {
				t.Errorf("one embedding call should be bounded by the shorter of the hook's deadline and the timeout, got %v", mem.embedLimitFor())
			}
		})
	}
}
