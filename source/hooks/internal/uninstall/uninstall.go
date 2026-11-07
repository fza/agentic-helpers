// Package uninstall takes agentic-helpers off the machine, `agentic-helpers
// uninstall`: it refuses while another Claude Code session runs, exports
// every store (internal/export) and checks the archive reads back whole, asks
// Claude Code to drop the plugin and its marketplace, then removes only what
// agentic-helpers can prove its own, and lists what it leaves because others
// share it.
//
// The plugin goes before its folders do, so no session starts a hook from a
// plugin whose binaries are gone; the session running the uninstall is told
// to restart.
//
// No prompt: agents run it, so its flags are the consent. --discard-data
// skips the export, --ignore-sessions passes over a session record a crash
// left, --dry-run changes nothing.
package uninstall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/export"
	"github.com/fza/agentic-helpers/source/hooks/internal/gitignore"
	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/process"
	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
	"github.com/fza/agentic-helpers/source/hooks/internal/sessionrecord"
)

const (
	self = export.Self + " uninstall"
	// pluginID and marketplace are how Claude Code names the plugin and the
	// marketplace it comes from; its data folder is `<plugin>-<marketplace>`,
	// or `<plugin>-inline` for one loaded from a folder.
	pluginID    = "agentic-helpers@agentic-helpers"
	marketplace = "agentic-helpers"
	dataPrefix  = "agentic-helpers-"

	// The lines agentic-memory and agentic-scratch add to git's global
	// excludes file, and the key a seat claim writes into a project's local
	// Claude Code settings.
	scratchPattern  = ".tmp/"
	settingsPattern = "**/.claude/settings.local.json"
	localSettings   = ".claude/settings.local.json"
	autoMemoryKey   = "autoMemoryEnabled"
)

const usage = "usage: " + self + " [--export-to <file> | --discard-data] [--ignore-sessions] [--dry-run]"

var (
	ErrInsideRemoval = errors.New("the export would lie inside a folder the uninstall removes")
	ErrLiveSessions  = errors.New("other Claude Code sessions run on this machine")
)

// Runner runs a command and reports how it ended; an error is a command that
// never started, as a missing `claude`.
type Runner interface {
	Run(ctx context.Context, dir string, argv []string) (process.Result, error)
}

// Env is what an uninstall reads and runs: the export's environment, the
// session records, SessionID the calling session's id where a Claude Code
// session calls, the user's home and environment, Claude Code's config
// folder, git for the global excludes file, the old layout's folders, and
// Claude, the runner `claude` goes through.
type Env struct {
	Export    export.Env
	Sessions  sessionrecord.Env
	SessionID string
	UserHome  string
	Getenv    func(string) string
	ConfigDir string
	OldTrees  []string
	Git       gitignore.Git
	Claude    Runner
	Now       func() time.Time
}

type options struct {
	exportTo       string
	discard        bool
	dryRun         bool
	ignoreSessions bool
}

func parse(args []string) (options, bool) {
	var parsed options

	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--discard-data":
			parsed.discard = true
		case "--dry-run":
			parsed.dryRun = true
		case "--ignore-sessions":
			parsed.ignoreSessions = true
		case "--export-to":
			if index+1 >= len(args) || args[index+1] == "" {
				return options{}, false
			}

			index++
			parsed.exportTo = args[index]
		default:
			return options{}, false
		}
	}

	if parsed.discard && parsed.exportTo != "" {
		return options{}, false
	}

	return parsed, true
}

type run struct {
	env     Env
	streams hookio.Streams
	failed  bool
}

func (current *run) say(format string, args ...any) {
	_, _ = fmt.Fprintf(current.streams.Out, format+"\n", args...)
}

func (current *run) fail(format string, args ...any) {
	current.failed = true
	_, _ = fmt.Fprintf(current.streams.Err, self+": "+format+"\n", args...)
}

// Main runs `agentic-helpers uninstall`.
func Main(ctx context.Context, args []string, env Env, streams hookio.Streams) int {
	parsed, valid := parse(args)
	if !valid {
		_, _ = fmt.Fprintln(streams.Err, usage)

		return 1
	}

	current := &run{env: env, streams: streams}
	removals := current.removals()
	traces := current.traces(ctx)

	target := parsed.exportTo
	if target == "" {
		target = filepath.Join(env.UserHome, "agentic-helpers-export-"+env.Now().UTC().Format(home.BackupStamp)+".tar.gz")
	}

	target, err := filepath.Abs(target)
	if err != nil {
		current.fail("resolving %s: %v; nothing removed", target, err)

		return 1
	}

	for _, removal := range removals {
		for _, kept := range []string{env.UserHome, env.ConfigDir} {
			if kept != "" && within(kept, removal) {
				current.fail("%s holds %s, which is no folder of agentic-helpers' own; nothing removed", removal, kept)

				return 1
			}
		}
	}

	if !parsed.discard && slices.ContainsFunc(removals, func(removal string) bool { return within(target, removal) }) {
		current.fail("%s: %v; nothing removed", target, ErrInsideRemoval)

		return 1
	}

	var others []sessionrecord.Listed

	if !parsed.ignoreSessions {
		others, err = current.otherSessions(ctx)
		if err != nil {
			current.fail("%v; nothing removed", err)

			return 1
		}
	}

	if parsed.dryRun {
		current.dryRun(parsed, target, removals, traces, others)

		return 0
	}

	if len(others) > 0 {
		current.fail("%v; quit them first, or pass --ignore-sessions where a record outlived a crashed session; "+
			"nothing removed", ErrLiveSessions)
		current.listSessions(current.streams.Err, others)

		return 1
	}

	if parsed.discard {
		current.say("%s: --discard-data: memory stores, the tool library and attachments are deleted with no export", self)
	} else if !current.export(ctx, target) {
		return 1
	}

	current.claude(ctx)

	for _, removal := range removals {
		err = remove(removal)
		if err != nil {
			current.fail("%v", err)

			continue
		}

		current.say("removed %s", removal)
	}

	if len(removals) == 0 {
		current.say("nothing of agentic-helpers' own was left on this machine")
	}

	current.say("restart this Claude Code session: its hooks still name the plugin just removed")
	current.listTraces(traces)

	if current.failed {
		return 1
	}

	return 0
}

// export writes the archive and reads it back whole; false stops the
// uninstall before anything is removed.
func (current *run) export(ctx context.Context, target string) bool {
	result, err := export.Write(ctx, current.env.Export, target, false)
	if err != nil {
		current.fail("export failed: %v; nothing removed", err)

		return false
	}

	_, err = export.Verify(result.Path)
	if err != nil {
		current.fail("export failed: %v; nothing removed", err)

		return false
	}

	for _, line := range export.Summary(result) {
		current.say("%s", line)
	}

	current.say("%s reads back whole", result.Path)

	return true
}

func (current *run) dryRun(parsed options, target string, removals []string, traces []trace,
	others []sessionrecord.Listed,
) {
	if len(others) > 0 {
		current.say("a real run is refused while these sessions run; quit them first, or pass --ignore-sessions " +
			"where a record outlived a crashed session")
		current.listSessions(current.streams.Out, others)
	}

	if parsed.discard {
		current.say("would delete memory stores, the tool library and attachments with no export (--discard-data)")
	} else {
		current.say("would export every store to %s and check it reads back whole", target)
	}

	for _, argv := range claudeCommands() {
		current.say("would run %s", strings.Join(argv, " "))
	}

	for _, removal := range removals {
		current.say("would remove %s", removal)
	}

	current.listTraces(traces)
}

// otherSessions answers every live session record but the calling
// session's own. The calling session is the one SessionID names, or the one
// whose Claude Code process runs this call.
func (current *run) otherSessions(ctx context.Context) ([]sessionrecord.Listed, error) {
	records, err := sessionrecord.Records(ctx, current.env.Sessions)
	if err != nil {
		return nil, err
	}

	owner := 0
	if current.env.Sessions.Processes != nil {
		owner = seat.OwnerAncestor(ctx, current.env.Sessions.Processes, current.env.Sessions.OwnerName)
	}

	var others []sessionrecord.Listed

	for _, record := range records {
		own := record.SessionID == current.env.SessionID || owner > 0 && record.PID == owner
		if !record.Gone && !own {
			others = append(others, record)
		}
	}

	return others, nil
}

func (current *run) listSessions(out io.Writer, sessions []sessionrecord.Listed) {
	for _, session := range sessions {
		_, _ = fmt.Fprintf(out, "  session %s (pid %d) in %s\n", session.SessionID, session.PID, session.Cwd)
	}
}

// removals is every folder agentic-helpers keeps on its own that exists: the
// cache root, the data root, the old layout's folders, and the plugin's data
// folders.
func (current *run) removals() []string {
	candidates := []string{current.env.Export.Roots.Cache, current.env.Export.Roots.Data}
	candidates = append(candidates, current.env.OldTrees...)
	candidates = append(candidates, current.pluginData()...)

	var found []string

	for _, candidate := range candidates {
		if !filepath.IsAbs(candidate) || slices.Contains(found, filepath.Clean(candidate)) {
			continue
		}

		_, err := os.Lstat(candidate)
		if err == nil {
			found = append(found, filepath.Clean(candidate))
		}
	}

	return found
}

// pluginData is the plugin's data folder Claude Code names in
// CLAUDE_PLUGIN_DATA where it is agentic-helpers', else every
// `agentic-helpers-*` folder under the config folder's `plugins/data/`.
func (current *run) pluginData() []string {
	named := ""
	if current.env.Getenv != nil {
		named = current.env.Getenv("CLAUDE_PLUGIN_DATA")
	}

	if filepath.IsAbs(named) && strings.HasPrefix(filepath.Base(named), dataPrefix) {
		return []string{filepath.Clean(named)}
	}

	if current.env.ConfigDir == "" {
		return nil
	}

	found, err := filepath.Glob(filepath.Join(current.env.ConfigDir, "plugins", "data", dataPrefix+"*"))
	if err != nil {
		return nil
	}

	return found
}

// remove removes the folder at target through its parent's descriptor, which
// never follows a link below it (internal/safefs): a link planted inside
// goes, never what it points at.
func remove(target string) error {
	parent, err := safefs.Root(filepath.Dir(target))
	if err != nil {
		return fmt.Errorf("removing %s: %w", target, err)
	}

	defer parent.Close()

	return parent.RemoveAll(filepath.Base(target))
}

func within(path string, dir string) bool {
	relative, err := filepath.Rel(dir, path)

	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func claudeCommands() [][]string {
	return [][]string{
		{"claude", "plugin", "uninstall", pluginID},
		{"claude", "plugin", "marketplace", "remove", marketplace},
	}
}

// claude asks Claude Code to drop the plugin, then its marketplace. A
// missing `claude` or a failure prints the command for the user to run; the
// rest of the uninstall stands.
func (current *run) claude(ctx context.Context) {
	for _, argv := range claudeCommands() {
		command := strings.Join(argv, " ")

		if current.env.Claude == nil {
			current.say("no claude command here; run it yourself: %s", command)

			continue
		}

		result, err := current.env.Claude.Run(ctx, current.env.UserHome, argv)

		switch {
		case err != nil:
			current.say("could not run claude (%v); run it yourself: %s", err, command)
		case result.Code != 0:
			current.say("%s failed (%s); run it yourself", command, strings.TrimSpace(result.Stderr+" "+result.Stdout))
		default:
			current.say("ran %s", command)
		}
	}
}

// trace is something agentic-helpers wrote that others share, left in place,
// with the command that removes it, or, where no command does it with the
// tools every machine has, what to change by hand.
type trace struct {
	what    string
	command string
	hand    string
}

func (current *run) listTraces(traces []trace) {
	if len(traces) == 0 {
		return
	}

	current.say("left in place, since other tools or your projects may rely on them; to remove one by hand:")

	for _, held := range traces {
		if held.command != "" {
			current.say("  %s:\n    %s", held.what, held.command)
		} else {
			current.say("  %s: %s", held.what, held.hand)
		}
	}
}

// traces is every shared trace agentic-helpers may have written: its lines in
// git's global excludes file, and in each checkout a store knows its `.tmp/`
// and the auto memory key of its local Claude Code settings.
func (current *run) traces(ctx context.Context) []trace {
	var found []trace

	if current.env.Git != nil {
		global := gitignore.Global{Git: current.env.Git, Home: current.env.UserHome, Getenv: current.env.Getenv}
		excludes := global.File(ctx, current.env.UserHome)

		held, err := os.ReadFile(excludes)
		if err == nil {
			lines := strings.Split(string(held), "\n")

			for _, pattern := range []string{scratchPattern, settingsPattern} {
				if slices.ContainsFunc(lines, func(line string) bool { return strings.TrimSpace(line) == pattern }) {
					found = append(found, trace{
						what:    fmt.Sprintf("the line %s in git's global excludes file %s", pattern, excludes),
						command: withoutLine(pattern, excludes),
					})
				}
			}
		}
	}

	for _, checkout := range current.checkouts() {
		scratch := filepath.Join(checkout, ".tmp")

		info, err := os.Lstat(scratch)
		if err == nil && info.IsDir() {
			found = append(found, trace{what: "the scratch folder " + scratch + "/", command: "rm -rf " + quote(scratch)})
		}

		settings := filepath.Join(checkout, filepath.FromSlash(localSettings))
		if autoMemoryOff(settings) {
			found = append(found, trace{
				what: fmt.Sprintf("the key %s: false in %s", autoMemoryKey, settings),
				hand: fmt.Sprintf("remove the key %q from %s in an editor", autoMemoryKey, settings),
			})
		}
	}

	return found
}

// checkouts is every checkout an identity of a store on the machine admits,
// read before anything is removed; a store that cannot be read adds none.
func (current *run) checkouts() []string {
	dirs, err := current.env.Export.Roots.StoreDirs()
	if err != nil {
		return nil
	}

	var paths []string

	for _, dir := range dirs {
		identity, _, err := home.ReadIdentityFile(dir.Dir)
		if err != nil {
			continue
		}

		for _, path := range identity.Paths() {
			if filepath.IsAbs(path) && !slices.Contains(paths, path) {
				paths = append(paths, path)
			}
		}
	}

	slices.Sort(paths)

	return paths
}

func autoMemoryOff(path string) bool {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) || err != nil {
		return false
	}

	var settings map[string]json.RawMessage

	err = json.Unmarshal(raw, &settings)
	if err != nil {
		return false
	}

	return bytes.Equal(bytes.TrimSpace(settings[autoMemoryKey]), []byte("false"))
}

func quote(text string) string {
	return "'" + strings.ReplaceAll(text, "'", `'\''`) + "'"
}

// withoutLine is a command removing every line that is exactly line from
// file, the same on BSD and GNU grep in any POSIX shell, with no removal a
// guard holds to a call of its own. grep exits 1 where no line is left, which
// still writes the file; an exit above 1, an error, keeps the file as it was
// and leaves the new one beside it.
func withoutLine(line string, file string) string {
	replacement := quote(file + ".new")

	return fmt.Sprintf("grep -vxF %s %s > %s; [ $? -le 1 ] && mv %s %s", quote(line), quote(file), replacement,
		replacement, quote(file))
}
