package pieces_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/pieces"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestTheSessionStartNamesAgenticBinariesOnThePath(t *testing.T) {
	root := t.TempDir()
	stray := filepath.Join(root, "go", "bin")
	plugin := filepath.Join(root, ".claude", "plugins", "cache", "agentic-helpers", "bin")
	other := filepath.Join(root, "other")

	test.WriteFile(t, filepath.Join(stray, "agentic-tools"), "")
	test.WriteFile(t, filepath.Join(stray, "agentic-memory"), "")
	test.WriteFile(t, filepath.Join(plugin, "agentic-memory"), "")
	test.WriteFile(t, filepath.Join(other, "gopls"), "")

	cases := []struct {
		name string
		path []string
		hook bool
		says bool
	}{
		{name: "a stray directory", path: []string{other, stray}, hook: true, says: true},
		{name: "the same directory twice says it once", path: []string{stray, stray}, hook: true, says: true},
		{name: "the plugin's own bin", path: []string{plugin, other}, hook: true},
		{name: "a command run by hand", path: []string{stray}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := layeredEnv(t, "", "")
			env.Hook = tc.hook
			getenv := env.Getenv
			env.Getenv = func(key string) string {
				if key == "PATH" {
					return strings.Join(tc.path, string(filepath.ListSeparator))
				}

				return getenv(key)
			}

			var captured test.Captured

			code := pieces.Main(context.Background(), []string{"check"}, env, captured.Streams(""))
			if code != 0 {
				t.Fatalf("check should exit 0, got %d", code)
			}

			line := "agentic-helpers: " + stray + " on the PATH holds agentic-memory, agentic-tools, " +
				"which every project's Bash runs ahead of the plugin; remove them\n"

			count := strings.Count(captured.Out.String(), line)
			if tc.says && count != 1 {
				t.Errorf("the line should name the sorted binaries once, got:\n%s", captured.Out.String())
			}

			if !tc.says && strings.Contains(captured.Out.String(), "on the PATH holds") {
				t.Errorf("no line should appear, got:\n%s", captured.Out.String())
			}
		})
	}
}
