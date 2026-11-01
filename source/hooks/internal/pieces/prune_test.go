package pieces_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/migration"
	"github.com/fza/agentic-helpers/source/hooks/internal/pieces"
	"github.com/fza/agentic-helpers/source/hooks/internal/vectorseg"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestPruneDeletesStoresPastTheConfiguredRetention(t *testing.T) {
	cases := []struct {
		name    string
		config  string
		deleted bool
	}{
		{name: "the default retention keeps a store unused for 40 days", config: "", deleted: false},
		{name: "a retention of 30 days deletes it", config: "cache_retention: 30d\n", deleted: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
			config := filepath.Join(root, "config.yaml")
			test.WriteFile(t, config, tc.config)

			env := pieces.Env{
				Settings: []string{config, filepath.Join(root, "settings.json")},
				Getenv:   func(string) string { return "" },
				Migration: migration.Env{
					Roots: test.Roots(root), Now: func() time.Time { return now },
				},
			}

			dir := filepath.Join(env.Migration.Roots.SharedCache(home.Tools), "vectors", "fp")
			store := vectorseg.Open(env.Migration.Roots.Cache, dir)

			err := store.Write([]vectorseg.Row{{ID: "a", Hash: "h", Chunks: [][]float32{{1, 0}}}},
				func(string, string) bool { return true })
			if err != nil {
				t.Fatalf("writing a row: %v", err)
			}

			err = os.Remove(filepath.Join(dir, "used.json"))
			if err != nil {
				t.Fatalf("removing the stamp: %v", err)
			}

			written := now.Add(-40 * 24 * time.Hour)

			listed, err := os.ReadDir(dir)
			if err != nil {
				t.Fatalf("listing the store: %v", err)
			}

			for _, entry := range listed {
				err = os.Chtimes(filepath.Join(dir, entry.Name()), written, written)
				if err != nil {
					t.Fatalf("aging the store: %v", err)
				}
			}

			var captured test.Captured

			code := pieces.Main(context.Background(), []string{"prune"}, env, captured.Streams(""))
			if code != 0 {
				t.Fatalf("prune should succeed: %s", captured.Err.String())
			}

			if test.Exists(t, dir) == tc.deleted {
				t.Error("prune should delete a store exactly when it is unused past cache_retention")
			}

			if strings.Contains(captured.Out.String(), "deleted "+dir) != tc.deleted {
				t.Error("prune should name each store it deleted")
			}
		})
	}
}

func TestPruneRunsTheToolLibrarysVersionPruning(t *testing.T) {
	cases := []struct {
		name   string
		config string
		runs   bool
	}{
		{name: "with the configured limits while agentic-tools is on",
			config: "tools_total_mb: 300\ntools_version_lifetime: 30d\n", runs: true},
		{name: "never while agentic-tools is off", config: "tools_enabled: false\n", runs: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
			config := filepath.Join(root, "config.yaml")
			test.WriteFile(t, config, tc.config)

			var called []string

			env := pieces.Env{
				Settings:  []string{config, filepath.Join(root, "settings.json")},
				Getenv:    func(string) string { return "" },
				Migration: migration.Env{Roots: test.Roots(root), Now: func() time.Time { return now }},
				ToolPrune: func(_ context.Context, roots home.Roots, limitMB int, lifetime time.Duration, at time.Time) {
					called = append(called, fmt.Sprintf("%s %d %s %s", roots.Data, limitMB, lifetime, at.Format(time.RFC3339)))
				},
			}

			var captured test.Captured

			code := pieces.Main(context.Background(), []string{"prune"}, env, captured.Streams(""))
			if code != 0 {
				t.Fatalf("prune should succeed: %s", captured.Err.String())
			}

			want := []string{fmt.Sprintf("%s 300 720h0m0s 2026-11-01T12:00:00Z", env.Migration.Roots.Data)}
			if !tc.runs {
				want = nil
			}

			if !slices.Equal(called, want) {
				t.Errorf("prune should run the tool library's pruning once, with the configured limits, while agentic-tools is on")
			}
		})
	}
}
