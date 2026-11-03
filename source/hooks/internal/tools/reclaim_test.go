package tools_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/promptvector"
	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
)

func TestSessionStartReclaimsTheKeysOfSessionsGoneAWeek(t *testing.T) {
	cases := []struct {
		name string
		path func(h *harness) string
		// now is the clock the file's writes read: the harness's own, or the
		// machine's for the prompt vectors, which take no clock.
		now   func(h *harness) time.Time
		stale string
		fresh string
	}{
		{
			name: "the own store's session file",
			path: func(h *harness) string {
				return filepath.Join(h.roots().StoreCache(filepath.Base(filepath.Dir(h.store())), home.Tools), "sessions.slots")
			},
			now:   func(h *harness) time.Time { return h.clock.Now() },
			stale: "session/crashed", fresh: "session/recent",
		},
		{
			name:  "the shared seen file",
			path:  func(h *harness) string { return filepath.Join(h.roots().SharedCache(home.Tools), "sessions.slots") },
			now:   func(h *harness) time.Time { return h.clock.Now() },
			stale: "seen/101-1700000000", fresh: "seen/202-1700000000",
		},
		{
			name: "the prompt vectors",
			path: func(h *harness) string {
				return filepath.Join(promptvector.Dir(h.roots().Cache), "sessions.slots")
			},
			now:   func(*harness) time.Time { return time.Now() },
			stale: "crashed", fresh: "recent",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.tool("word-count", "Count the words of a file")

			path := tc.path(h)
			file := slots.Open(h.roots().Cache, path, slots.MinWidth)
			now := tc.now(h)

			file.Now = func() time.Time { return now.Add(-slots.StaleAge - time.Hour) }
			put(t, file, tc.stale)

			file.Now = func() time.Time { return now.Add(-slots.StaleAge + time.Hour) }
			put(t, file, tc.fresh)

			h.hook("start", map[string]any{"source": "startup"})

			if held(t, file, tc.stale) || !held(t, file, tc.fresh) {
				t.Error("SessionStart should clear a key no write touched for a week and keep one written within it")
			}
		})
	}
}

func put(t *testing.T, file *slots.File, key string) {
	t.Helper()

	err := file.Put(context.Background(), key, []byte(`{}`))
	if err != nil {
		t.Fatalf("writing %s: %v", key, err)
	}
}

func held(t *testing.T, file *slots.File, key string) bool {
	t.Helper()

	_, found, err := file.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("reading %s: %v", key, err)
	}

	return found
}
