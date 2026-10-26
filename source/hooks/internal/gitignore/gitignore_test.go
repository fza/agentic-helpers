package gitignore_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/gitignore"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestEnsure(t *testing.T) {
	cases := []struct {
		name     string
		excludes string // core.excludesFile, "~/" standing for the home
		xdg      string // "set" points XDG_CONFIG_HOME into the temporary tree
		ignored  bool
		existing string
		wantFile string // relative to the temporary tree; empty when nothing is added
		want     string
	}{
		{name: "core.excludesFile absolute", excludes: "abs", wantFile: "home/.gitignore_global", want: ".tmp/\n"},
		{name: "core.excludesFile under the home", excludes: "~/.gitignore_global", wantFile: "home/.gitignore_global", want: ".tmp/\n"},
		{name: "the XDG default", xdg: "set", wantFile: "xdg/git/ignore", want: ".tmp/\n"},
		{name: "the home default", wantFile: "home/.config/git/ignore", want: ".tmp/\n"},
		{name: "a file ending without a newline", excludes: "abs", existing: ".DS_Store", wantFile: "home/.gitignore_global", want: ".DS_Store\n.tmp/\n"},
		{name: "the pattern already there", excludes: "abs", existing: ".DS_Store\n.tmp/\n", want: ".DS_Store\n.tmp/\n"},
		{name: "git ignoring the path by another rule", excludes: "abs", ignored: true, existing: "", want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tree := t.TempDir()
			home := filepath.Join(tree, "home")
			global := filepath.Join(home, ".gitignore_global")

			git := &test.Git{}

			switch tc.excludes {
			case "abs":
				git.ExcludesFile = global
			case "":
			default:
				git.ExcludesFile = tc.excludes
			}

			if tc.ignored {
				git.Ignored = []string{".tmp"}
			}

			if tc.existing != "" {
				test.WriteFile(t, global, tc.existing)
			}

			env := map[string]string{}
			if tc.xdg == "set" {
				env["XDG_CONFIG_HOME"] = filepath.Join(tree, "xdg")
			}

			line, err := gitignore.Global{Git: git, Home: home, Getenv: func(key string) string { return env[key] }}.
				Ensure(context.Background(), "agentic-scratch", tree, ".tmp", ".tmp/")
			if err != nil {
				t.Fatalf("ensuring the pattern: %v", err)
			}

			if tc.wantFile == "" {
				if line != "" {
					t.Errorf("nothing should be added or said, got: %s", line)
				}

				held, _ := os.ReadFile(global)
				if string(held) != tc.want {
					t.Errorf("the excludes file should stay as it was, got: %q", held)
				}

				return
			}

			file := filepath.Join(tree, filepath.FromSlash(tc.wantFile))

			held, err := os.ReadFile(file)
			if err != nil || string(held) != tc.want {
				t.Errorf("the pattern should be appended to git's global excludes file, got %q, %v", held, err)
			}

			if !strings.Contains(line, "agentic-scratch: added .tmp/ to "+file) {
				t.Errorf("the addition should be said once, naming the file, got: %s", line)
			}
		})
	}
}
