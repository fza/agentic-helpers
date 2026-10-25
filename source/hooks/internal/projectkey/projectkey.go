// Package projectkey names a project the way sdd names it for its search index,
// so both tools agree on one identity per repository on the machine: the
// `repo_id` the project's `.sdd/config.yaml` declares, else `local/` and a hash
// of the git common directory, which every worktree of a repository shares.
package projectkey

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/fza/agentic-helpers/source/hooks/internal/project"
)

type config struct {
	RepoID string `yaml:"repo_id"`
}

// Key is the project's identity for a session working in base. Outside git the
// hash covers base itself.
func Key(ctx context.Context, base string, lookup project.CommonDirLookup) string {
	root := project.Root(ctx, base, lookup)

	id := declaredRepoID(root)
	if id != "" {
		return id
	}

	stable, err := filepath.Abs(base)
	if err != nil {
		stable = base
	}

	common, err := lookup.CommonDir(ctx, base)
	if err == nil && common != "" {
		stable = filepath.Clean(common)
	}

	sum := sha256.Sum256([]byte(stable))

	return "local/" + hex.EncodeToString(sum[:])[:12]
}

func declaredRepoID(root string) string {
	raw, err := os.ReadFile(filepath.Join(root, ".sdd", "config.yaml"))
	if err != nil {
		return ""
	}

	var parsed config

	err = yaml.Unmarshal(raw, &parsed)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(parsed.RepoID)
}

// Dir is where a key lives below a root: a `repo_id` such as
// `github.com/fza/agentic-helpers` becomes nested directories, as in sdd.
func Dir(root string, key string) string {
	return filepath.Join(root, filepath.FromSlash(key))
}
