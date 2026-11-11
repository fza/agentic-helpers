package pluginconfig

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

const keySecretReadSkip = "shell_guard_secret_read_skip"

// SecretReadSkip / Project is a checkout root as written, cleaned, and Globs
// the paths under it no read is refused for, each relative to it, matched by
// doublestar, `**` crossing directories.
type SecretReadSkip struct {
	Project string
	Globs   []string
}

// SecretReadSkips reads `shell_guard_secret_read_skip`: comma-separated
// `<project>=<glob>[;<glob>]` entries, the shape `shell_guard_extra_roots`
// takes. An entry whose project is neither absolute nor under `~`, or whose
// glob is absolute, climbs through `..` or does not compile, is ignored
// whole, and named among the problems.
func SecretReadSkips(value string) ([]SecretReadSkip, []string) {
	var entries []SecretReadSkip

	var problems []string

	for _, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		parsed, why := secretReadSkip(entry)
		if why != "" {
			problems = append(problems, fmt.Sprintf("%s: %s ignored; %s", keySecretReadSkip, entry, why))

			continue
		}

		entries = append(entries, parsed)
	}

	return entries, problems
}

func secretReadSkip(entry string) (SecretReadSkip, string) {
	const notAnEntry = "it is no <project>=<glob>[;<glob>] entry"

	project, globs, _ := strings.Cut(entry, "=")

	parsed := SecretReadSkip{}

	var why string

	parsed.Project, why = rootPath(strings.TrimSpace(project))
	if why != "" {
		return SecretReadSkip{}, why
	}

	for _, glob := range strings.Split(globs, ";") {
		glob = strings.TrimSpace(glob)

		switch {
		case glob == "":
			continue
		case path.IsAbs(glob) || strings.HasPrefix(glob, "~"):
			return SecretReadSkip{}, glob + " is not relative to the project"
		case slices.Contains(strings.Split(glob, "/"), ".."):
			return SecretReadSkip{}, glob + " climbs through .."
		case !doublestar.ValidatePattern(glob):
			return SecretReadSkip{}, glob + " is no glob"
		}

		parsed.Globs = append(parsed.Globs, glob)
	}

	if parsed.Project == "" || len(parsed.Globs) == 0 {
		return SecretReadSkip{}, notAnEntry
	}

	return parsed, ""
}
