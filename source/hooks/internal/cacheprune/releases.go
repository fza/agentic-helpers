package cacheprune

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
)

const (
	// UsedName is the file the launcher, plugin/hooks/run, touches in the
	// folder of the version it runs at each SessionStart.
	UsedName = "used"
	// releaseUse is how long a version a SessionStart used stays.
	releaseUse = 14 * 24 * time.Hour
	// processPrefix opens the name of every binary any release ships.
	processPrefix = "agentic-"
	// manifestLimit bounds the read of the plugin's manifest.
	manifestLimit = 1 << 20
)

// versionName is a folder the launcher unpacked a release into; nothing else
// in the data directory is ever removed.
var versionName = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// Processes lists the executable path of every running process whose name
// starts with prefix.
type Processes interface {
	Executables(ctx context.Context, prefix string) ([]string, error)
}

// Releases is the plugin's data directory, where the launcher unpacks each
// release into `<Dir>/<version>/`, the version the plugin names, and the
// process table telling which versions run.
type Releases struct {
	Dir       string
	Named     string
	Processes Processes
}

// PluginReleases is the data directory and named version of the plugin a
// hook or MCP server runs under: Claude Code hands it CLAUDE_PLUGIN_DATA and
// CLAUDE_PLUGIN_ROOT, whose `.claude-plugin/plugin.json` names the version.
// A command run by hand gets neither, and its run keeps every release.
func PluginReleases(getenv func(string) string, processes Processes) Releases {
	if getenv == nil {
		return Releases{}
	}

	dir := getenv("CLAUDE_PLUGIN_DATA")
	root := getenv("CLAUDE_PLUGIN_ROOT")

	if !filepath.IsAbs(dir) || !filepath.IsAbs(root) {
		return Releases{}
	}

	return Releases{Dir: filepath.Clean(dir), Named: namedVersion(root), Processes: processes}
}

func namedVersion(root string) string {
	file, err := os.Open(filepath.Join(root, ".claude-plugin", "plugin.json"))
	if err != nil {
		return ""
	}

	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, manifestLimit))
	if err != nil {
		return ""
	}

	var manifest struct {
		Version string `json:"version"`
	}

	err = json.Unmarshal(data, &manifest)
	if err != nil {
		return ""
	}

	return manifest.Version
}

// known is whether the run knows enough to keep every release a session may
// still run: without the named version or the process table it keeps all.
func (releases Releases) known() bool {
	return releases.Dir != "" && releases.Named != "" && releases.Processes != nil
}

// running holds every version a running process executes a program from,
// among the first path components of whatever else runs, which only a folder
// named as a version is ever looked up by.
func (releases Releases) running(ctx context.Context) (map[string]bool, error) {
	paths, err := releases.Processes.Executables(ctx, processPrefix)
	if err != nil {
		return nil, fmt.Errorf("reading the process table: %w", err)
	}

	// A process's path names the folder as it was started from (darwin) or
	// with every link resolved (Linux), so both forms of the directory count.
	resolved, _ := filepath.EvalSymlinks(releases.Dir)
	dirs := []string{releases.Dir, resolved}

	versions := map[string]bool{}

	for _, path := range paths {
		for _, dir := range dirs {
			rel, _ := filepath.Rel(dir, filepath.Clean(path))
			version, _, _ := strings.Cut(rel, string(filepath.Separator))
			versions[version] = true
		}
	}

	return versions, nil
}

// usedSince is whether a SessionStart touched the version's use mark at or
// after cutoff.
func (releases Releases) usedSince(version string, cutoff time.Time) bool {
	info, err := os.Lstat(filepath.Join(releases.Dir, version, UsedName))

	return err == nil && !info.ModTime().Before(cutoff)
}

// pruneReleases deletes every release folder no session may still run: not
// the named version, not one a running process executes from, and not one a
// SessionStart used within releaseUse. A folder goes the way a vector store
// does, renamed to `.pruned-<version>` and then removed, so the launcher
// never finds half a version under its name; one a crash left goes first.
// The data directory is the root of every removal: Claude Code's and the
// owner's, so a link to it is followed, and none below it ever is.
func pruneReleases(ctx context.Context, releases Releases, now time.Time) ([]string, error) {
	if !releases.known() {
		return nil, nil
	}

	held, err := safefs.Root(releases.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	defer held.Close()

	listed, err := held.Entries()
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", releases.Dir, err)
	}

	var errs []error

	remove := func(tomb string, version string) {
		err := held.RemoveAll(tomb)
		if err != nil {
			errs = append(errs, fmt.Errorf("deleting release %s: %w", version, err))
		}
	}

	for _, entry := range listed {
		version, tomb := strings.CutPrefix(entry.Name(), tombPrefix)
		if tomb && entry.IsDir() && versionName.MatchString(version) {
			remove(entry.Name(), version)
		}
	}

	running, err := releases.running(ctx)
	if err != nil {
		return nil, errors.Join(append(errs, err)...)
	}

	cutoff := now.Add(-releaseUse)

	var deleted []string

	for _, entry := range listed {
		version := entry.Name()
		if !entry.IsDir() || !versionName.MatchString(version) || version == releases.Named || running[version] ||
			releases.usedSince(version, cutoff) {
			continue
		}

		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())

			break
		}

		tomb := tombPrefix + version

		err = held.Rename(version, held, tomb)
		if err != nil {
			errs = append(errs, fmt.Errorf("moving release %s aside: %w", version, err))

			continue
		}

		deleted = append(deleted, filepath.Join(releases.Dir, version))
		remove(tomb, version)
	}

	return deleted, errors.Join(errs...)
}
