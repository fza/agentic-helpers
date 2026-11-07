package pieces_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/pieces"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const movedLine = "agentic-helpers: the grounding gate and agentic-capture moved to the sdd-helpers plugin: " +
	"claude plugin marketplace add fza/sdd-helpers, then claude plugin install sdd-helpers@sdd-helpers\n"

func TestTheSessionStartNamesThePluginTheGraphHooksMovedTo(t *testing.T) {
	cases := []struct {
		name      string
		graph     string
		installed string
		says      bool
	}{
		{name: "a graph project without sdd-helpers", graph: "dir", installed: "without-sdd-helpers.json", says: true},
		{name: "a project without a graph", installed: "without-sdd-helpers.json"},
		{name: "a .sdd that is no directory", graph: "file", installed: "without-sdd-helpers.json"},
		{name: "sdd-helpers installed", graph: "dir", installed: "with-sdd-helpers.json"},
		{name: "no installed_plugins.json", graph: "dir", says: true},
		{name: "a garbled installed_plugins.json", graph: "dir", installed: "garbled.json", says: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			project := t.TempDir()

			switch tc.graph {
			case "dir":
				err := os.Mkdir(filepath.Join(project, ".sdd"), 0o755)
				if err != nil {
					t.Fatalf("creating the graph: %v", err)
				}
			case "file":
				test.WriteFile(t, filepath.Join(project, ".sdd"), "not a graph\n")
			}

			installed := pieces.InstalledPlugins(t.TempDir())
			if tc.installed != "" {
				test.WriteFile(t, installed, test.Fixture(t, "installedplugins/"+tc.installed))
			}

			var captured test.Captured

			env := pieces.Env{
				Settings: settings(t, ""), Hook: true, Dir: project, InstalledPlugins: installed,
				Getenv: test.Getenv(test.Stubs(t, "timeout")),
			}

			code := pieces.Main(context.Background(), []string{"check"}, env, captured.Streams(""))
			if code != 0 {
				t.Fatalf("check should exit 0, got %d: %s", code, captured.Err.String())
			}

			said := strings.Count(captured.Out.String(), movedLine)
			if tc.says && said != 1 {
				t.Errorf("a graph project lacking sdd-helpers should be told once where the graph hooks went, got:\n%s",
					captured.Out.String())
			}

			if !tc.says && strings.Contains(captured.Out.String(), "sdd-helpers") {
				t.Errorf("no line should name sdd-helpers here, got:\n%s", captured.Out.String())
			}
		})
	}
}

func TestACommandRunOfCheckNeverNamesTheMovedHooks(t *testing.T) {
	project := t.TempDir()

	err := os.Mkdir(filepath.Join(project, ".sdd"), 0o755)
	if err != nil {
		t.Fatalf("creating the graph: %v", err)
	}

	var captured test.Captured

	env := pieces.Env{Settings: settings(t, ""), Dir: project, InstalledPlugins: pieces.InstalledPlugins(t.TempDir())}

	code := pieces.Main(context.Background(), []string{"check"}, env, captured.Streams(""))
	if code != 0 || strings.Contains(captured.Out.String(), "sdd-helpers") {
		t.Errorf("only the SessionStart hook should name the moved hooks, got %d:\n%s", code, captured.Out.String())
	}
}
