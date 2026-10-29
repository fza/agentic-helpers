package graphconfig_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/graphconfig"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

// layout builds a workspace holding a code repository `mika` and, beside it,
// the graph repository `mika-sdd`, from the sdd config fixtures each names.
type layout struct {
	code      string
	codeLocal string
	graph     string
	strayDir  bool
}

func (shape layout) build(t *testing.T) string {
	t.Helper()

	workspace := graphconfig.RealPath(t.TempDir())
	code := filepath.Join(workspace, "mika")

	makeDir(t, filepath.Join(code, "backend"))
	makeDir(t, filepath.Join(code, ".sdd"))

	if shape.code != "" {
		test.WriteFile(t, filepath.Join(code, ".sdd", "config.yaml"), test.Fixture(t, "sddconfig/"+shape.code))
	}

	if shape.codeLocal != "" {
		test.WriteFile(t, filepath.Join(code, ".sdd", "config.local.yaml"), test.Fixture(t, "sddconfig/"+shape.codeLocal))
	}

	if shape.strayDir {
		makeDir(t, filepath.Join(code, ".sdd", "graph"))
	}

	if shape.graph != "" {
		test.WriteFile(t, filepath.Join(workspace, "mika-sdd", ".sdd", "config.yaml"), test.Fixture(t, "sddconfig/"+shape.graph))
		test.WriteFile(t, filepath.Join(workspace, "moved", "mika-sdd", ".sdd", "config.yaml"), test.Fixture(t, "sddconfig/"+shape.graph))
	}

	return workspace
}

func makeDir(t *testing.T, path string) {
	t.Helper()

	err := os.MkdirAll(path, 0o755)
	if err != nil {
		t.Fatalf("creating %s: %v", path, err)
	}
}

func TestResolve(t *testing.T) {
	cases := []struct {
		name        string
		shape       layout
		wantDir     string
		wantEntries string
		wantRepoID  string
		wantLang    string
	}{
		{
			name:        "a project with neither key keeps its own graph",
			shape:       layout{code: "plain.yaml"},
			wantDir:     "mika/.sdd",
			wantEntries: "mika/.sdd/graph",
			wantRepoID:  "gitlab.com/example/mika",
		},
		{
			name:        "project_dir leads to the graph repository and its graph_dir",
			shape:       layout{code: "project-dir.yaml", graph: "graph-repo.yaml", strayDir: true},
			wantDir:     "mika-sdd/.sdd",
			wantEntries: "mika-sdd/graph",
			wantRepoID:  "gitlab.com/example/mika-sdd",
			wantLang:    "de",
		},
		{
			name:        "the local config overrides project_dir",
			shape:       layout{code: "project-dir.yaml", codeLocal: "project-dir-local.yaml", graph: "graph-repo.yaml"},
			wantDir:     "moved/mika-sdd/.sdd",
			wantEntries: "moved/mika-sdd/graph",
			wantRepoID:  "gitlab.com/example/mika-sdd",
			wantLang:    "de",
		},
		{
			name:        "project_dir set only in the local config is followed",
			shape:       layout{codeLocal: "project-dir.yaml", graph: "graph-repo.yaml"},
			wantDir:     "mika-sdd/.sdd",
			wantEntries: "mika-sdd/graph",
			wantRepoID:  "gitlab.com/example/mika-sdd",
			wantLang:    "de",
		},
		{
			name:        "a code-repository graph_dir pointing elsewhere is not followed",
			shape:       layout{code: "graph-dir-outside.yaml", graph: "graph-repo.yaml", strayDir: true},
			wantDir:     "mika/.sdd",
			wantEntries: "mika/.sdd/graph",
			wantRepoID:  "gitlab.com/example/mika-sdd",
		},
		{
			name:        "a project_dir naming no checkout keeps the code repository's .sdd and an empty graph",
			shape:       layout{code: "project-dir.yaml"},
			wantDir:     "mika/.sdd",
			wantEntries: "mika-sdd/.sdd/graph",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workspace := tc.shape.build(t)
			code := filepath.Join(workspace, "mika")

			for _, project := range []graphconfig.Project{
				graphconfig.Resolve(filepath.Join(code, "backend")),
				graphconfig.ResolveAt(code),
			} {
				if project.Dir != filepath.Join(workspace, tc.wantDir) {
					t.Errorf("the project's settings should live in the resolved .sdd, got: %s", project.Dir)
				}

				if project.Entries != filepath.Join(workspace, tc.wantEntries) {
					t.Errorf("the entries should live under the resolved project's graph_dir, got: %s", project.Entries)
				}

				if project.RepoID() != tc.wantRepoID {
					t.Errorf("the repo_id should be the resolved project's, got: %q", project.RepoID())
				}

				if project.Language() != tc.wantLang {
					t.Errorf("the language should be the resolved project's, got: %q", project.Language())
				}
			}
		})
	}
}

func TestResolveWithoutAGraph(t *testing.T) {
	root := graphconfig.RealPath(t.TempDir())
	below := filepath.Join(root, "source")
	makeDir(t, below)

	if graphconfig.Resolve(below) != (graphconfig.Project{}) {
		t.Error("a tree without a graph should resolve to no project")
	}

	makeDir(t, filepath.Join(root, ".sdd"))

	if graphconfig.ResolveAt(below) != (graphconfig.Project{}) {
		t.Error("resolving at a checkout should not walk up to a graph above it")
	}
}

func TestLink(t *testing.T) {
	cases := []struct {
		name   string
		config string
		want   string
	}{
		{name: "no config", config: "", want: ""},
		{name: "the fork's project_dir", config: "project_dir: ../app-sdd\n", want: "app-sdd"},
		{name: "a graph_dir leaving the checkout", config: "graph_dir: ../app-sdd/.sdd/graph\n", want: "app-sdd/.sdd/graph"},
		{name: "a graph_dir inside the checkout", config: "graph_dir: graph\n", want: ""},
		{name: "repo_id alone", config: "repo_id: github.com/acme/app\n", want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			code := filepath.Join(workspace, "app")
			makeDir(t, filepath.Join(code, ".sdd"))

			if tc.config != "" {
				test.WriteFile(t, filepath.Join(code, ".sdd", "config.yaml"), tc.config)
			}

			want := ""
			if tc.want != "" {
				want = filepath.Join(workspace, tc.want)
			}

			if got := graphconfig.Link(code); got != want {
				t.Errorf("the link should name the graph's place outside the checkout and only that, got %q", got)
			}
		})
	}
}
