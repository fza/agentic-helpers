package memory

import (
	"context"
	"os"
	"path/filepath"

	"github.com/fza/agentic-helpers/source/hooks/internal/graphconfig"
)

const graphCloneFile = "graph-clone.json"

// graphCloneAnswer is where `sdd repo path` placed sdd's copy of graph_repo,
// kept in the store cache so a memory process asks once, not on every hook
// call.
type graphCloneAnswer struct {
	GraphRepo string `json:"graph_repo"`
	Path      string `json:"path"`
}

// graphClone is sdd's own copy of the repository the `.sdd/` in root names as
// `graph_repo`, empty where it names none or no copy can be found. The kept
// answer stands while `graph_repo` is unchanged and its folder exists; else
// `sdd repo path` is asked, only where the installed sdd carries it. A failed
// or empty answer keeps nothing, so the next process asks again.
func (mem *memory) graphClone(ctx context.Context, root string) string {
	repo := graphconfig.GraphRepo(root)
	if repo == "" {
		return ""
	}

	kept := filepath.Join(mem.cacheDir(), graphCloneFile)

	var held graphCloneAnswer
	if mem.key != "" && readJSON(kept, &held) && held.GraphRepo == repo && isFolder(held.Path) {
		return held.Path
	}

	if mem.env.Graph == nil || mem.env.Sdd != nil && !mem.env.Sdd.Of(ctx, root).RepoPath {
		return ""
	}

	// A failed call answers with an empty path, which names no folder.
	path, _ := mem.env.Graph.RepoPath(ctx, root, repo)
	if !isFolder(path) {
		return ""
	}

	if mem.key != "" {
		_ = writeJSON(mem.env.Roots.Cache, kept, graphCloneAnswer{GraphRepo: repo, Path: path})
	}

	return path
}

func isFolder(path string) bool {
	info, err := os.Stat(path)

	return err == nil && info.IsDir()
}
