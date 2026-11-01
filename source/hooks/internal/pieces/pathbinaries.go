package pieces

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// pluginsFolder is where Claude Code installs a plugin; its `bin/` is the one
// place on the PATH an agentic-helpers binary belongs.
const pluginsFolder = "/.claude/plugins/"

// pathBinaryLines says, one line per directory, which `agentic-*` binaries sit
// on the PATH outside a plugin. Every project's Bash calls find them there
// ahead of the plugin, so an unreleased build runs machine-wide and can
// migrate another project's memory store to a format no release reads.
func pathBinaryLines(getenv func(string) string) []string {
	var lines []string

	seen := map[string]bool{}

	for _, dir := range filepath.SplitList(getenv("PATH")) {
		if dir == "" || seen[dir] || strings.Contains(dir+string(filepath.Separator), pluginsFolder) {
			continue
		}

		seen[dir] = true

		found, err := filepath.Glob(filepath.Join(dir, "agentic-*"))
		if err != nil || len(found) == 0 {
			continue
		}

		names := make([]string, 0, len(found))
		for _, path := range found {
			names = append(names, filepath.Base(path))
		}

		slices.Sort(names)

		lines = append(lines, fmt.Sprintf("%s on the PATH holds %s, which every project's Bash runs ahead of the plugin; remove them",
			dir, strings.Join(names, ", ")))
	}

	return lines
}
