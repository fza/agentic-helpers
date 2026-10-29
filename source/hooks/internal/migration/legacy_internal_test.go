package migration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestAWritersListNamesOnlyCleanAbsolutePathsInARegularFile(t *testing.T) {
	root := t.TempDir()
	listed := filepath.Join(root, "writers.json")
	test.WriteFile(t, listed, `["/a/.git", "relative/.git", "/a/../b/.git"]`)

	got := readWriters(listed)
	if len(got) != 1 || got[0] != "/a/.git" {
		t.Errorf("only clean absolute paths should be read from a list, got %v", got)
	}

	linked := filepath.Join(root, "linked.json")

	err := os.Symlink(listed, linked)
	if err != nil {
		t.Fatalf("linking: %v", err)
	}

	if got := readWriters(linked); got != nil {
		t.Errorf("a list reached through a link should name nothing, got %v", got)
	}
}

func TestACheckoutIsOfflineOnlyWhereItsVolumeIsGone(t *testing.T) {
	root := t.TempDir()
	volumes := filepath.Join(root, "Volumes")
	test.WriteFile(t, filepath.Join(volumes, "Mounted", "present", ".git", "HEAD"), "ref: refs/heads/main\n")

	env := Env{MountRoots: []string{volumes}}

	cases := []struct {
		name    string
		common  string
		offline bool
	}{
		{name: "on a volume not mounted", common: filepath.Join(volumes, "External", "app", ".git"), offline: true},
		{name: "a checkout that exists", common: filepath.Join(volumes, "Mounted", "present", ".git")},
		{name: "outside every mount root", common: filepath.Join(root, "elsewhere", "app", ".git")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if env.offline(tc.common) != tc.offline {
				t.Error("a checkout should count as offline exactly where it lies below a mount root and is gone")
			}
		})
	}
}
