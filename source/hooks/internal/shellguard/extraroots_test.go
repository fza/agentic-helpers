package shellguard_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

// extraRootGuard is a guard whose settings open ~/other/.tmp, a root reached
// through a link, and links to the home directory and to / to the project,
// and give a third project only entries that do not parse.
// other/.tmp/link points outside every root.
func extraRootGuard(t *testing.T) guard {
	t.Helper()

	g := newGuard(t, true, "")
	other := filepath.Join(g.tree.Home, "other")
	third := filepath.Join(g.tree.Home, "third")
	shared := filepath.Join(g.tree.Root, "shared")
	linkedRoot := filepath.Join(g.tree.Root, "linked-root")
	homeLink := filepath.Join(g.tree.Root, "home-link")
	slashLink := filepath.Join(g.tree.Root, "slash-link")

	for _, directory := range []string{filepath.Join(other, ".tmp", "drafts"), filepath.Join(other, ".tmp-old"), third, shared} {
		err := os.MkdirAll(directory, 0o755)
		if err != nil {
			t.Fatalf("creating %s: %v", directory, err)
		}
	}

	links := map[string]string{
		filepath.Join(other, ".tmp", "link"): g.tree.Outside, linkedRoot: shared, homeLink: g.tree.Home, slashLink: "/",
	}

	for link, target := range links {
		err := os.Symlink(target, link)
		if err != nil {
			t.Fatalf("linking %s: %v", link, err)
		}
	}

	g.placeholders = strings.NewReplacer(
		"{project}", g.tree.Project, "{tmp}", g.tree.Tmp, "{home}", g.tree.Home, "{outside}", g.tree.Outside,
		"{other}", other, "{third}", third, "{shared}", shared, "{linkedroot}", linkedRoot,
		"{homelink}", homeLink, "{slashlink}", slashLink,
	)

	fixtures := t.TempDir()
	test.FixtureTreeReplacing(t, "shellguard/extra-roots", fixtures, g.placeholders)
	test.FixtureTreeReplacing(t, "shellguard/extra-roots", filepath.Join(g.tree.Project, "scripts"), g.placeholders)
	g.env.Settings = []string{filepath.Join(fixtures, "settings.json")}

	return g
}

func TestAnExtraRootGivesOnlyItsProjectTheInsideVerdict(t *testing.T) {
	cases := []struct {
		name     string
		project  string
		command  string
		decision string
		reason   string
	}{
		{"a removal under the root", "{project}", "rm {other}/.tmp/drafts/x.md", "allow", ""},
		{"a removal under the root written with ~", "{project}", "rm ~/other/.tmp/drafts/x.md", "allow", ""},
		{"a removal of the root itself", "{project}", "rm -r {other}/.tmp", "allow", ""},
		{"a move from under the root", "{project}", "mv {other}/.tmp/drafts/x.md .", "allow", ""},
		{"a git move from under the root", "{project}", "git mv {other}/.tmp/drafts/x.md y.md", "allow", ""},
		{"a removal in a script", "{project}", "bash scripts/clean-other.sh", "allow", ""},
		{"a removal under a root named through a link", "{project}", "rm {linkedroot}/x", "allow", ""},
		{"a removal under where that link points", "{project}", "rm {shared}/x", "allow", ""},
		{"a removal beside the root", "{project}", "rm {other}/notes.md", "ask", "outside project: {other}/notes.md"},
		{"a removal under a name the root prefixes", "{project}", "rm {other}/.tmp-old/x", "ask", "outside project: {other}/.tmp-old/x"},
		{"a removal through a link out of the root", "{project}", "rm {other}/.tmp/link/x", "ask", "outside project: {other}/.tmp/link/x"},
		{"a removal in the home directory a root links to", "{project}", "rm {home}/notes.md", "ask", "outside project: {home}/notes.md"},
		{"a removal of the filesystem root a root links to", "{project}", "rm -rf /", "ask", "outside project: /"},
		{"a removal from a project no entry opens it for", "{third}", "rm {other}/.tmp/drafts/x.md", "ask", "outside project: {other}/.tmp/drafts/x.md"},
		{"a removal under a root of an entry naming a relative path", "{third}", "rm {shared}/x", "ask", "outside project: {shared}/x"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := extraRootGuard(t)
			g.env.ProjectDir = g.placeholders.Replace(tc.project)

			decision, reason := g.judge(tc.command, tc.project)

			want := ""
			if tc.reason != "" {
				want = "agentic-shell-guard: " + g.placeholders.Replace(tc.reason)
			}

			if decision != tc.decision || reason != want {
				t.Errorf("a path under an extra root should count as the project's for that project alone, got %s %q", decision, reason)
			}
		})
	}
}

func TestExtraRootsComeFromNoFileAnAgentWritesAndNoPrefix(t *testing.T) {
	cases := []struct {
		name     string
		settings []string
		getenv   func(string) string
	}{
		{name: "config.yaml", settings: []string{"config.yaml", "none.json"}},
		{name: "the hook environment", settings: []string{"none.json"}, getenv: func(name string) string {
			if name == "CLAUDE_PLUGIN_OPTION_SHELL_GUARD_EXTRA_ROOTS" {
				return "~/project=~/other/.tmp"
			}

			return ""
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := extraRootGuard(t)
			fixtures := filepath.Dir(g.env.Settings[0])

			g.env.Settings = nil
			for _, name := range tc.settings {
				g.env.Settings = append(g.env.Settings, filepath.Join(fixtures, name))
			}

			g.env.Getenv = tc.getenv

			decision, reason := g.judge("rm {other}/.tmp/drafts/x.md", "{project}")
			if decision != "ask" || !strings.Contains(reason, "outside project: ") {
				t.Errorf("an extra root set where an agent can write it should open nothing, got %s %q", decision, reason)
			}
		})
	}
}
