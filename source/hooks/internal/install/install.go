// Package install wires this checkout's hooks into Claude Code, or takes them
// back out.
//
// Claude Code reads one settings file per user. That file also carries
// credentials and everything else a person configured, so this edits only the
// hook entries running these binaries and leaves every other key where it
// found it. A timestamped copy goes beside the file before anything is written.
//
// Running it twice changes nothing the second time: every entry running one of
// these binaries, or one of the scripts they replaced, is removed before the
// current set goes in, whichever path it named, so a moved install leaves
// nothing stale behind.
//
// Skills are linked rather than copied, so an edit in this checkout reaches
// every project at once and no copy drifts from the file it came from.
package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
)

const (
	usage   = "usage: agentic-install [--print|--remove]"
	timeout = 15
)

var ErrNoCheckout = errors.New("no agentic-helpers checkout at or above the working directory")

type wiring struct {
	event   string
	matcher string
	binary  string
	mode    string
}

// What each hook answers, and when. The carry-forward reads the session as it
// opens and writes as it closes; the gate starts a turn on each prompt, refuses
// a call its reads do not cover before it runs, and records what a call
// covered after.
var wirings = []wiring{
	{event: "SessionStart", binary: "agentic-carryforward", mode: "session-start"},
	{event: "SessionStart", binary: "agentic-carryforward", mode: "session-log 1"},
	{event: "SessionStart", binary: "agentic-carryforward", mode: "session-log 2"},
	{event: "SessionStart", binary: "agentic-carryforward", mode: "session-log 3"},
	{event: "SessionStart", binary: "agentic-carryforward", mode: "session-log 4"},
	{event: "SessionStart", binary: "agentic-carryforward", mode: "session-log 5"},
	{event: "UserPromptSubmit", binary: "agentic-carryforward", mode: "prompt"},
	{event: "UserPromptSubmit", binary: "agentic-grounding-gate", mode: "turn"},
	{event: "Stop", binary: "agentic-carryforward", mode: "stop"},
	{event: "PostToolUse", matcher: "Bash", binary: "agentic-grounding-gate", mode: "record"},
	{event: "PostToolUse", matcher: "Write|Edit", binary: "agentic-grounding-gate", mode: "record"},
	{event: "PreToolUse", matcher: "AskUserQuestion|Write|Edit|NotebookEdit|Bash", binary: "agentic-grounding-gate", mode: "gate"},
	{event: "SessionStart", binary: "agentic-scratch", mode: "start"},
	{event: "SessionEnd", binary: "agentic-scratch", mode: "end"},
}

// Matching on the names rather than on a path is what makes a move
// survivable: an entry written before the binaries moved still names them.
var owned = []string{
	"agentic-carryforward", "agentic-grounding-gate", "agentic-scratch",
	"carryforward.py", "grounding_gate.py", "scratch.py",
}

// Env is where an install runs: the settings directory it edits, the
// directory the hook binaries sit in, and the working directory the checkout
// is found from.
type Env struct {
	ConfigDir  string
	BinDir     string
	WorkingDir string
	Now        func() time.Time
}

type entry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

func Main(ctx context.Context, args []string, env Env, streams hookio.Streams) int {
	removing, showing := false, false

	for _, arg := range args {
		switch arg {
		case "--remove":
			removing = true
		case "--print":
			showing = true
		default:
			_, _ = fmt.Fprintln(streams.Err, usage)

			return 1
		}
	}

	checkout, err := findCheckout(env.WorkingDir)
	if err != nil {
		_, _ = fmt.Fprintln(streams.Err, err)

		return 1
	}

	run := &installer{env: env, streams: streams, checkout: checkout, removing: removing, showing: showing}

	return run.do(ctx)
}

// findCheckout walks up to the directory holding this repository's skills
// and hook module, since an installed binary carries no path to either.
func findCheckout(start string) (string, error) {
	walked := start
	for {
		if isFile(filepath.Join(walked, "source", "hooks", "go.mod")) && isDir(filepath.Join(walked, "skills")) {
			return walked, nil
		}

		parent := filepath.Dir(walked)
		if parent == walked {
			return "", ErrNoCheckout
		}

		walked = parent
	}
}

type installer struct {
	env      Env
	streams  hookio.Streams
	checkout string
	removing bool
	showing  bool
}

func (run *installer) say(format string, args ...any) {
	_, _ = fmt.Fprintf(run.streams.Out, format+"\n", args...)
}

func (run *installer) warn(format string, args ...any) {
	_, _ = fmt.Fprintf(run.streams.Err, format+"\n", args...)
}

func (run *installer) settingsPath() string {
	return filepath.Join(run.env.ConfigDir, "settings.json")
}

func (run *installer) do(ctx context.Context) int {
	original, settings, err := run.readSettings()
	if err != nil {
		run.warn("%v", err)

		return 1
	}

	hooks, err := run.withoutOurs(settings)
	if err != nil {
		run.warn("%v", err)

		return 1
	}

	if !run.removing {
		hooks, err = run.wired(hooks)
		if err != nil {
			run.warn("%v", err)

			return 1
		}

		run.warnMissing()
	}

	held := settings.without("hooks")
	if len(hooks) > 0 {
		held = settings.set("hooks", raw(hooks))
	}

	if run.showing {
		shown, err := indented(hooks)
		if err != nil {
			run.warn("%v", err)

			return 1
		}

		_, _ = run.streams.Out.Write(shown)

		for _, line := range run.linkSkills(ctx) {
			run.say("%s", line)
		}

		return 0
	}

	for _, line := range run.linkSkills(ctx) {
		run.say("%s", line)
	}

	return run.write(original, settings, held)
}

func (run *installer) write(original []byte, settings object, held object) int {
	before, _ := settings.get("hooks")
	after, _ := held.get("hooks")

	if sameJSON(orEmpty(before), orEmpty(after)) {
		run.say("%s already says this", run.settingsPath())

		return 0
	}

	if original != nil {
		backup := fmt.Sprintf("%s.bak-%s", run.settingsPath(), run.env.Now().UTC().Format("20060102T150405Z"))

		err := os.WriteFile(backup, original, 0o600)
		if err != nil {
			run.warn("copying the old settings to %s: %v", backup, err)

			return 1
		}

		run.say("copied the old settings to %s", backup)
	}

	data, err := indented(held)
	if err != nil {
		run.warn("%v", err)

		return 1
	}

	err = os.MkdirAll(run.env.ConfigDir, 0o755)
	if err == nil {
		err = os.WriteFile(run.settingsPath(), data, 0o600)
	}

	if err != nil {
		run.warn("writing %s: %v", run.settingsPath(), err)

		return 1
	}

	verb := "wired into"
	if run.removing {
		verb = "removed from"
	}

	run.say("%s %s", verb, run.settingsPath())
	run.say("Claude Code reloads the settings, so a running session picks the change up.")

	return 0
}

func orEmpty(value json.RawMessage) json.RawMessage {
	if value == nil {
		return json.RawMessage("{}")
	}

	return value
}

// readSettings returns the file as it stands and parsed. A file that is not a
// JSON object is refused rather than overwritten: it also holds credentials.
func (run *installer) readSettings() ([]byte, object, error) {
	data, err := os.ReadFile(run.settingsPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, object{}, nil
	}

	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", run.settingsPath(), err)
	}

	settings, err := parseObject(data)
	if err != nil {
		return nil, nil, fmt.Errorf("%s is not readable as JSON: %w", run.settingsPath(), err)
	}

	return data, settings, nil
}

func ours(command string) bool {
	for _, name := range owned {
		if strings.Contains(command, name) {
			return true
		}
	}

	return false
}

// withoutOurs is every hook the person configured, minus the ones this
// checkout owns, each event, group and entry in the order it stood.
func (run *installer) withoutOurs(settings object) (object, error) {
	hooksRaw, found := settings.get("hooks")
	if !found {
		return object{}, nil
	}

	hooks, err := parseObject(hooksRaw)
	if err != nil {
		return nil, fmt.Errorf("reading the hooks in %s: %w", run.settingsPath(), err)
	}

	kept := object{}

	for _, event := range hooks {
		var groups []json.RawMessage

		err := json.Unmarshal(event.value, &groups)
		if err != nil {
			return nil, fmt.Errorf("reading the %s hooks: %w", event.key, err)
		}

		var standing []json.RawMessage

		for _, groupRaw := range groups {
			group, err := parseObject(groupRaw)
			if err != nil {
				return nil, fmt.Errorf("reading a %s hook group: %w", event.key, err)
			}

			rest, err := entriesNotOurs(group)
			if err != nil {
				return nil, fmt.Errorf("reading a %s hook group: %w", event.key, err)
			}

			if len(rest) > 0 {
				standing = append(standing, raw(group.set("hooks", raw(rest))))
			}
		}

		if len(standing) > 0 {
			kept = append(kept, field{key: event.key, value: raw(standing)})
		}
	}

	return kept, nil
}

func entriesNotOurs(group object) ([]json.RawMessage, error) {
	entriesRaw, found := group.get("hooks")
	if !found {
		return nil, nil
	}

	var entries []json.RawMessage

	err := json.Unmarshal(entriesRaw, &entries)
	if err != nil {
		return nil, fmt.Errorf("reading its entries: %w", err)
	}

	var rest []json.RawMessage

	for _, entryRaw := range entries {
		var held struct {
			Command string `json:"command"`
		}

		_ = json.Unmarshal(entryRaw, &held)

		if !ours(held.Command) {
			rest = append(rest, entryRaw)
		}
	}

	return rest, nil
}

// wired folds this checkout's entries into what is already there, one group
// per matcher, so an event with several matchers keeps them apart.
func (run *installer) wired(hooks object) (object, error) {
	for _, wiring := range wirings {
		command := fmt.Sprintf("%q %s", filepath.Join(run.env.BinDir, wiring.binary), wiring.mode)
		added := raw(entry{Type: "command", Command: command, Timeout: timeout})

		var groups []json.RawMessage

		existing, found := hooks.get(wiring.event)
		if found {
			err := json.Unmarshal(existing, &groups)
			if err != nil {
				return nil, fmt.Errorf("reading the %s hooks: %w", wiring.event, err)
			}
		}

		groups, err := withEntry(groups, wiring.matcher, added)
		if err != nil {
			return nil, fmt.Errorf("adding to the %s hooks: %w", wiring.event, err)
		}

		hooks = hooks.set(wiring.event, raw(groups))
	}

	return hooks, nil
}

func withEntry(groups []json.RawMessage, matcher string, added json.RawMessage) ([]json.RawMessage, error) {
	for index, groupRaw := range groups {
		group, err := parseObject(groupRaw)
		if err != nil {
			return nil, err
		}

		held, found := group.get("matcher")

		var named string
		if found {
			_ = json.Unmarshal(held, &named)
		}

		if found != (matcher != "") || named != matcher {
			continue
		}

		var entries []json.RawMessage

		existing, _ := group.get("hooks")
		if existing != nil {
			err = json.Unmarshal(existing, &entries)
			if err != nil {
				return nil, err
			}
		}

		groups[index] = raw(group.set("hooks", raw(append(entries, added))))

		return groups, nil
	}

	group := object{}
	if matcher != "" {
		group = group.set("matcher", raw(matcher))
	}

	return append(groups, raw(group.set("hooks", raw([]json.RawMessage{added})))), nil
}

func (run *installer) warnMissing() {
	var binaries []string

	for _, wiring := range wirings {
		if !slices.Contains(binaries, wiring.binary) {
			binaries = append(binaries, wiring.binary)
		}
	}

	for _, binary := range binaries {
		path := filepath.Join(run.env.BinDir, binary)
		if !isFile(path) {
			run.warn("%s is missing; run `go install -C source/hooks ./cmd/...` from this checkout", path)
		}
	}
}

// linkSkills points the skills directory at this checkout's own, one link per
// skill. A link naming somewhere else is replaced, because a checkout that
// moved would otherwise leave every skill pointing at nothing; a directory
// somebody else put there is left alone.
func (run *installer) linkSkills(ctx context.Context) []string {
	source := filepath.Join(run.checkout, "skills")
	target := filepath.Join(run.env.ConfigDir, "skills")

	entries, err := os.ReadDir(source)
	if err != nil {
		return nil
	}

	var acted []string

	for _, skill := range entries {
		if ctx.Err() != nil || !skill.IsDir() {
			continue
		}

		line := run.linkSkill(filepath.Join(source, skill.Name()), filepath.Join(target, skill.Name()))
		if line != "" {
			acted = append(acted, line)
		}
	}

	return acted
}

func (run *installer) linkSkill(skill string, link string) string {
	wanted := skill
	if run.removing {
		wanted = ""
	}

	held, err := os.Readlink(link)

	isLink := err == nil
	if isLink && held == wanted || !isLink && wanted == "" {
		return ""
	}

	verb, done := "link", "linked"
	if run.removing {
		verb, done = "unlink", "unlinked"
	}

	if run.showing {
		return verb + " " + link
	}

	if exists(link) && !isLink {
		run.warn("%s is not a link; leaving it alone", link)

		return ""
	}

	if isLink {
		err = os.Remove(link)
		if err != nil {
			run.warn("removing %s: %v", link, err)

			return ""
		}
	}

	if !run.removing {
		err = os.MkdirAll(filepath.Dir(link), 0o755)
		if err == nil {
			err = os.Symlink(skill, link)
		}

		if err != nil {
			run.warn("linking %s: %v", link, err)

			return ""
		}
	}

	return done + " " + link
}

func exists(path string) bool {
	_, err := os.Lstat(path)

	return err == nil
}

func isFile(path string) bool {
	info, err := os.Stat(path)

	return err == nil && info.Mode().IsRegular()
}

func isDir(path string) bool {
	info, err := os.Stat(path)

	return err == nil && info.IsDir()
}
