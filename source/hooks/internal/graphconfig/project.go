package graphconfig

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
)

const defaultEntries = ".sdd/graph"

// Project is where a directory's sdd state lives. Dir is the `.sdd/` holding
// the project's config, `grounding.yaml` and `tmp/`; Entries is the directory
// its graph entries live under. Both are empty where no graph governs.
type Project struct {
	Dir     string
	Entries string
}

// sddConfig is the part of `.sdd/config.yaml` and `.sdd/config.local.yaml` the
// hooks read. sdd owns the files and refuses keys it does not know, so a key
// here is one sdd itself defines.
type sddConfig struct {
	ProjectDir string `yaml:"project_dir"`
	GraphDir   string `yaml:"graph_dir"`
	RepoID     string `yaml:"repo_id"`
	Language   string `yaml:"language"`
}

// Resolve finds the project governing start: the `.sdd/` GraphDir walks up to,
// followed through `project_dir` where that sets one.
func Resolve(start string) Project {
	return resolveFrom(GraphDir(start))
}

// ResolveAt resolves the project whose `.sdd/` sits directly in root, walking
// no further up: a checkout without one has none.
func ResolveAt(root string) Project {
	held := filepath.Join(root, GraphDirName)

	info, err := os.Stat(held)
	if err != nil || !info.IsDir() {
		return Project{}
	}

	return resolveFrom(held)
}

// In sdd's split layout a code repository's `.sdd/` holds only `project_dir`,
// naming the folder that holds the graph repository's `.sdd/`, and every other
// part of the project lives there. A relative `project_dir` or `graph_dir`
// joins to the folder holding the `.sdd/` that sets it, as sdd resolves both,
// never to the git toplevel. A `project_dir` naming no `.sdd/` keeps the
// code repository's own `.sdd/`, so nothing is ever created in a missing
// checkout, and reads an empty graph there.
func resolveFrom(dir string) Project {
	if dir == "" {
		return Project{}
	}

	own := readConfig(dir)
	if own.ProjectDir == "" {
		return Project{Dir: dir, Entries: entriesOf(dir, own.GraphDir)}
	}

	target := joinTo(filepath.Dir(dir), own.ProjectDir)
	graph := filepath.Join(target, GraphDirName)

	info, err := os.Stat(graph)
	if err != nil || !info.IsDir() {
		return Project{Dir: dir, Entries: filepath.Join(target, defaultEntries)}
	}

	return Project{Dir: graph, Entries: entriesOf(graph, readConfig(graph).GraphDir)}
}

func (project Project) config() sddConfig {
	if project.Dir == "" {
		return sddConfig{}
	}

	return readConfig(project.Dir)
}

func (project Project) RepoID() string {
	return project.config().RepoID
}

func (project Project) Language() string {
	return project.config().Language
}

// A `graph_dir` leaving the folder holding its `.sdd/` is the split layout
// before `project_dir`, which sdd refuses; it reads as unset rather than
// as the graph another repository holds.
func entriesOf(dir, graphDir string) string {
	base := filepath.Dir(dir)
	fallback := filepath.Join(base, defaultEntries)

	if graphDir == "" {
		return fallback
	}

	entries := joinTo(base, graphDir)

	inside, err := filepath.Rel(base, entries)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return fallback
	}

	return entries
}

func joinTo(base, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}

	return filepath.Join(base, path)
}

// readConfig overlays the local file on the shared one, a key the local file
// leaves empty inheriting the shared value, as sdd merges them. A file that is
// missing or does not parse contributes nothing: sdd itself refuses to run on
// one, and a hook has nothing better to fall back to than the defaults.
func readConfig(dir string) sddConfig {
	shared := parseConfig(filepath.Join(dir, "config.yaml"))
	local := parseConfig(filepath.Join(dir, "config.local.yaml"))

	return sddConfig{
		ProjectDir: overlay(shared.ProjectDir, local.ProjectDir),
		GraphDir:   overlay(shared.GraphDir, local.GraphDir),
		RepoID:     overlay(shared.RepoID, local.RepoID),
		Language:   overlay(shared.Language, local.Language),
	}
}

func parseConfig(path string) sddConfig {
	raw, err := os.ReadFile(path)
	if err != nil {
		return sddConfig{}
	}

	var parsed sddConfig

	err = yaml.Unmarshal(raw, &parsed)
	if err != nil {
		return sddConfig{}
	}

	return sddConfig{
		ProjectDir: strings.TrimSpace(parsed.ProjectDir),
		GraphDir:   strings.TrimSpace(parsed.GraphDir),
		RepoID:     strings.TrimSpace(parsed.RepoID),
		Language:   strings.TrimSpace(parsed.Language),
	}
}

func overlay(shared, local string) string {
	if local != "" {
		return local
	}

	return shared
}
