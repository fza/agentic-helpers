package memory_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

const autoMemoryLine = "agentic-memory: Claude Code's own auto memory is off for this project from the next session"

func localSettingsOf(root string) string {
	return filepath.Join(root, ".claude", "settings.local.json")
}

func globalIgnoreOf(h *harness) string {
	return filepath.Join(h.home, ".config", "git", "ignore")
}

func TestAClaimTurnsOffAutoMemory(t *testing.T) {
	h := newHarness(t)
	h.git.Ignored = []string{".tmp"}

	claimed := h.must("", "claim", "main")
	if strings.Count(claimed, autoMemoryLine) != 1 {
		t.Errorf("the claim should say once that auto memory is off, got:\n%s", claimed)
	}

	held, err := os.ReadFile(localSettingsOf(h.repo))
	if err != nil || string(held) != "{\n  \"autoMemoryEnabled\": false\n}\n" {
		t.Errorf("the claim should create the local settings with auto memory off, got %q, %v", held, err)
	}

	ignore, err := os.ReadFile(globalIgnoreOf(h))
	if err != nil || string(ignore) != "**/.claude/settings.local.json\n" {
		t.Errorf("the local settings should be added to git's global excludes file, got %q, %v", ignore, err)
	}

	if !strings.Contains(claimed, "agentic-memory: added **/.claude/settings.local.json to "+globalIgnoreOf(h)) {
		t.Errorf("the claim should say what it added to the excludes file, got:\n%s", claimed)
	}

	if test.Exists(t, filepath.Join(h.repo, ".git", "info", "exclude")) {
		t.Error("nothing should be written to the repository's info/exclude")
	}

	h.must("", "release")

	again := h.must("", "claim", "main")
	if strings.Contains(again, autoMemoryLine) || strings.Contains(again, "added **/.claude") {
		t.Errorf("a later claim should change and say nothing, got:\n%s", again)
	}
}

func TestAClaimKeepsTheOtherSettings(t *testing.T) {
	h := newHarness(t)
	test.WriteFile(t, localSettingsOf(h.repo), test.Fixture(t, "memory/settings-other-keys.json"))

	h.must("", "claim", "main")

	held, _ := os.ReadFile(localSettingsOf(h.repo))
	want := "{\n  \"autoMemoryEnabled\": false,\n  \"permissions\": {\n    \"allow\": [\"Bash(go test:*)\"]\n  }\n}\n"

	if string(held) != want {
		t.Errorf("the key should be added with every other key and its text kept, got:\n%s", held)
	}
}

func TestAClaimLeavesTheOwnersSettingAlone(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
		option  string
		says    string
	}{
		{name: "auto memory turned on by the owner", fixture: "memory/settings-auto-on.json"},
		{name: "auto memory already off", fixture: "memory/settings-auto-off.json"},
		{name: "a file that does not parse", fixture: "memory/settings-broken.json", says: "does not parse as a JSON object"},
		{name: "the option set to false", fixture: "memory/settings-other-keys.json", option: `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"memory_turns_off_auto":false}}}}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.git.Ignored = []string{".tmp"}
			original := test.Fixture(t, tc.fixture)
			test.WriteFile(t, localSettingsOf(h.repo), original)

			if tc.option != "" {
				test.WriteFile(t, h.settings, tc.option)
			}

			claimed := h.must("", "claim", "main")

			held, _ := os.ReadFile(localSettingsOf(h.repo))
			if string(held) != original {
				t.Errorf("the file should stay byte-identical, got:\n%s", held)
			}

			if strings.Contains(claimed, autoMemoryLine) || test.Exists(t, globalIgnoreOf(h)) {
				t.Errorf("nothing should be turned off or added to the excludes file, got:\n%s", claimed)
			}

			if tc.says != "" && !strings.Contains(claimed, tc.says) {
				t.Errorf("the claim should say why it left the file alone, got:\n%s", claimed)
			}
		})
	}
}

func TestAClaimFromAWorktreeWritesTheMainCheckout(t *testing.T) {
	h := newHarness(t)
	worktree := filepath.Join(filepath.Dir(h.repo), "repo-feature")
	test.WriteFile(t, filepath.Join(worktree, ".git"), "gitdir: "+filepath.Join(h.repo, ".git", "worktrees", "feature")+"\n")
	h.base = worktree
	h.worktrees = map[string]string{worktree: h.repo}

	h.must("", "claim", "main")

	if !test.Exists(t, localSettingsOf(h.repo)) || test.Exists(t, localSettingsOf(worktree)) {
		t.Error("the main checkout's local settings should be written, the worktree's never")
	}
}

func TestAClaimAddsNothingGitIgnoresAlready(t *testing.T) {
	h := newHarness(t)
	h.git.Ignored = []string{".claude/settings.local.json", ".tmp"}

	claimed := h.must("", "claim", "main")

	if !strings.Contains(claimed, autoMemoryLine) || test.Exists(t, globalIgnoreOf(h)) {
		t.Errorf("auto memory should go off with no addition to the excludes file, got:\n%s", claimed)
	}
}

func TestAReservationTurnsOffAutoMemoryBeforeItsSessionStarts(t *testing.T) {
	h := newHarness(t)

	reserved := h.must("", "claim", "impl", "--session", "f00dbabe-0000-4000-8000-000000000000")

	if !strings.Contains(reserved, autoMemoryLine) || !test.Exists(t, localSettingsOf(h.repo)) {
		t.Errorf("a launcher's claim should turn auto memory off before the session it reserves for starts, got:\n%s", reserved)
	}
}

func TestAClaimNeverWritesThroughALink(t *testing.T) {
	for _, linked := range []string{".claude", ".claude/settings.local.json"} {
		t.Run(linked, func(t *testing.T) {
			h := newHarness(t)
			elsewhere := t.TempDir()
			target := filepath.Join(elsewhere, "settings.local.json")
			test.WriteFile(t, target, "{}\n")

			link := filepath.Join(h.repo, filepath.FromSlash(linked))
			pointsAt := target

			if linked == ".claude" {
				pointsAt = elsewhere
			} else {
				err := os.MkdirAll(filepath.Dir(link), 0o755)
				if err != nil {
					t.Fatalf("creating .claude: %v", err)
				}
			}

			err := os.Symlink(pointsAt, link)
			if err != nil {
				t.Fatalf("planting the link: %v", err)
			}

			claimed := h.must("", "claim", "main")

			held, _ := os.ReadFile(target)
			if string(held) != "{}\n" || strings.Contains(claimed, autoMemoryLine) {
				t.Errorf("a linked settings path should be left alone, its target untouched, got %q:\n%s", held, claimed)
			}

			if !strings.Contains(claimed, linked+" is a symbolic link") {
				t.Errorf("the claim should name the link it refused, got:\n%s", claimed)
			}
		})
	}
}
