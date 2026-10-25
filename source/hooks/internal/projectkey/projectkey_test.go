package projectkey_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/projectkey"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func local(path string) string {
	sum := sha256.Sum256([]byte(path))

	return "local/" + hex.EncodeToString(sum[:])[:12]
}

func TestKey(t *testing.T) {
	root := t.TempDir()
	declared := t.TempDir()
	test.WriteFile(t, filepath.Join(declared, ".sdd", "config.yaml"), "repo_id: github.com/fza/agentic-helpers\n")

	cases := []struct {
		name   string
		base   string
		lookup test.CommonDir
		want   string
	}{
		{
			name:   "a declared repo_id is the key",
			base:   declared,
			lookup: test.CommonDir{Dir: filepath.Join(declared, ".git")},
			want:   "github.com/fza/agentic-helpers",
		},
		{
			name:   "without a repo_id the git common directory is hashed, as sdd hashes it",
			base:   filepath.Join(root, "worktree"),
			lookup: test.CommonDir{Dir: filepath.Join(root, ".git")},
			want:   local(filepath.Join(root, ".git")),
		},
		{
			name:   "outside git the directory itself is hashed",
			base:   root,
			lookup: test.CommonDir{Err: errors.New("not a git repository")},
			want:   local(root),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := projectkey.Key(context.Background(), tc.base, tc.lookup)
			if got != tc.want {
				t.Errorf("the key should follow sdd's index key, got %s", got)
			}
		})
	}
}

func TestARepoIDThatWouldLeaveTheStoreIsRefused(t *testing.T) {
	for _, id := range []string{
		"../../.ssh", "github.com/../../x", "github.com/./x", "/etc/passwd", "github.com//x", "nohost/x",
		"github.com/a b", "single",
	} {
		t.Run(id, func(t *testing.T) {
			base := t.TempDir()
			test.WriteFile(t, filepath.Join(base, ".sdd", "config.yaml"), "repo_id: \""+id+"\"\n")

			got := projectkey.Key(context.Background(), base, test.CommonDir{Err: errors.New("not a git repository")})
			if got != local(base) {
				t.Errorf("a repo_id outside sdd's shape should fall back to the hashed key, got %s", got)
			}
		})
	}
}

func TestDirNestsARepoID(t *testing.T) {
	got := projectkey.Dir("/store", "github.com/fza/agentic-helpers")
	if got != "/store/github.com/fza/agentic-helpers" {
		t.Errorf("a repo_id should become nested directories, got %s", got)
	}
}
