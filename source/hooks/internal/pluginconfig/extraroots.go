package pluginconfig

import (
	"fmt"
	"path"
	"slices"
	"strings"
)

const keyExtraRoots = "shell_guard_extra_roots"

// ExtraRoot / Project is a checkout root and Roots the directories outside it
// shell-guard treats as that project, each as written, cleaned: absolute or
// under `~`, which only the reader of a path expands.
type ExtraRoot struct {
	Project string
	Roots   []string
}

// ExtraRoots reads `shell_guard_extra_roots`: comma-separated
// `<project>=<root>[;<root>]` entries. An entry that does not parse, names a
// path neither absolute nor under `~`, or opens the filesystem root or the
// home directory is ignored whole, and named among the problems.
func ExtraRoots(value string) ([]ExtraRoot, []string) {
	var entries []ExtraRoot

	var problems []string

	for _, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		parsed, why := extraRoot(entry)
		if why != "" {
			problems = append(problems, fmt.Sprintf("%s: %s ignored; %s", keyExtraRoots, entry, why))

			continue
		}

		entries = append(entries, parsed)
	}

	return entries, problems
}

func extraRoot(entry string) (ExtraRoot, string) {
	const notAnEntry = "it is no <project>=<root>[;<root>] entry"

	// An entry without `=` has no roots, refused below.
	project, roots, _ := strings.Cut(entry, "=")

	parsed := ExtraRoot{}

	var why string

	parsed.Project, why = rootPath(strings.TrimSpace(project))
	if why != "" {
		return ExtraRoot{}, why
	}

	for _, root := range strings.Split(roots, ";") {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}

		cleaned, why := rootPath(root)
		if why != "" {
			return ExtraRoot{}, why
		}

		if cleaned == "/" || cleaned == "~" {
			return ExtraRoot{}, root + " is the filesystem root or the home directory"
		}

		parsed.Roots = append(parsed.Roots, cleaned)
	}

	if parsed.Project == "" || len(parsed.Roots) == 0 {
		return ExtraRoot{}, notAnEntry
	}

	return parsed, ""
}

// rootPath is a path cleaned, `~` kept as its first component, or why it
// cannot be one. A `..` component is refused rather than cleaned away: under
// `~` cleaning would drop it, opening a directory other than the one written.
func rootPath(written string) (string, string) {
	switch {
	case written == "":
		return "", ""
	case slices.Contains(strings.Split(written, "/"), ".."):
		return "", written + " climbs through .."
	case written == "~" || strings.HasPrefix(written, "~/"):
		return path.Clean(written), ""
	case path.IsAbs(written):
		return path.Clean(written), ""
	}

	return "", written + " is neither absolute nor under ~"
}
