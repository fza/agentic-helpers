// Package seat keeps the seat claims of a memory store: one file per seat under
// the store's `seats/`, naming the session holding it, the process that session
// runs in, and its transcript. A caller finds its own seat through the process
// that owns it, so a command the Bash tool runs, the plugin's MCP server and a
// hook all resolve the same seat, and `/clear` changes nothing.
package seat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"

	"github.com/fza/agentic-helpers/source/hooks/internal/atomicfile"
	"github.com/fza/agentic-helpers/source/hooks/internal/filelock"
	"github.com/fza/agentic-helpers/source/hooks/internal/process"
	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
)

// How far up the process tree to look for the owner. A hook runs through a
// shell, so the immediate parent is not reliably the Claude process.
const ancestorDepth = 12

// A seat name is a word in any script, counted in runes. Letters only, never a
// separator or a dot, so a name is always a plain path segment.
var namePattern = regexp.MustCompile(`^\p{L}[\p{L}\p{M}\p{N}-]{0,23}$`)

// ErrBadName refuses a seat name that is no plain path segment, so no caller
// can read, write or remove a file outside the seats directory.
var ErrBadName = errors.New(`a seat name is a letter, then up to 23 letters, marks, digits or dashes, in any script`)

// Normal is the one spelling of a seat name: NFC, so a letter typed precomposed
// or decomposed names one seat with one file, then lower-cased.
func Normal(name string) string {
	return strings.ToLower(norm.NFC.String(strings.TrimSpace(name)))
}

// Claim is who holds a seat. Closed is when its holder released it or its
// session ended, kept on the seat's last claim; Reserved is when a claim made
// for a session that has not yet taken it over was written; MovedFrom names
// the seat a handover moved the session off, until its next hook tells it.
type Claim struct {
	SessionID      string `json:"session_id"`
	PID            int    `json:"pid"`
	Started        string `json:"started"`
	Claimed        string `json:"claimed,omitempty"`
	SeizedFrom     string `json:"seized_from,omitempty"`
	TranscriptPath string `json:"transcript_path,omitempty"`
	Closed         string `json:"closed,omitempty"`
	Reserved       string `json:"reserved,omitempty"`
	MovedFrom      string `json:"moved_from,omitempty"`
	// ForkUUID is the uuid a fork of the holder's session would start its
	// transcript with: a fork names no parent, but copies the parent's records
	// with their uuids unchanged from its latest compaction boundary on, or
	// from its first message where it never compacted.
	ForkUUID string `json:"fork_uuid,omitempty"`
	// Commit is HEAD when the seat was claimed, where a hook-written draft of
	// the seat's state counts the session's commits from.
	Commit string `json:"commit,omitempty"`
}

type Processes interface {
	Self() int
	Parent() int
	ParentAndName(ctx context.Context, pid int) (int, string)
	StartTime(ctx context.Context, pid int) (time.Time, bool)
}

// ValidName holds a name already in its Normal spelling to the name rule.
func ValidName(name string) bool {
	return Normal(name) == name && namePattern.MatchString(name)
}

func Dir(store string) string {
	return filepath.Join(store, "seats")
}

func Path(dir string, name string) string {
	return filepath.Join(dir, name+".json")
}

// LastPath keeps the claim a seat's previous holder left, so the next claim
// still finds that session's transcript after a release or a sweep.
func LastPath(dir string, name string) string {
	return filepath.Join(dir, name+".last.json")
}

func Read(dir string, name string) (Claim, bool) {
	if !ValidName(name) {
		return Claim{}, false
	}

	return readClaim(Path(dir, name))
}

func ReadLast(dir string, name string) (Claim, bool) {
	if !ValidName(name) {
		return Claim{}, false
	}

	return readClaim(LastPath(dir, name))
}

func readClaim(path string) (Claim, bool) {
	var held Claim

	data, err := os.ReadFile(path)
	if err != nil {
		return Claim{}, false
	}

	err = json.Unmarshal(data, &held)
	if err != nil {
		return Claim{}, false
	}

	return held, true
}

func Write(root string, dir string, name string, held Claim) error {
	if !ValidName(name) {
		return fmt.Errorf("%w, %q is not one", ErrBadName, name)
	}

	return writeClaim(root, Path(dir, name), held)
}

func writeClaim(root string, path string, held Claim) error {
	data, err := json.MarshalIndent(held, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the claim: %w", err)
	}

	err = safefs.MkdirAll(root, filepath.Dir(path), 0o700)
	if err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}

	return atomicfile.Write(root, path, append(data, '\n'), 0o600)
}

// Lock holds the seats directory's lock until the returned function runs. A
// claim is read, judged and written as one step under it: two sessions judging
// one free seat apart would both write it, and both be told they hold it.
func Lock(ctx context.Context, root string, dir string) (func(), error) {
	err := safefs.MkdirAll(root, dir, 0o700)
	if err != nil {
		return nil, fmt.Errorf("creating %s: %w", dir, err)
	}

	file, err := safefs.Lock(root, filepath.Join(dir, ".lock"), 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening the seats lock: %w", err)
	}

	err = filelock.Exclusive(file)
	if err != nil {
		_ = file.Close()

		return nil, fmt.Errorf("taking the seats lock: %w", err)
	}

	err = ctx.Err()
	if err != nil {
		_ = filelock.Unlock(file)
		_ = file.Close()

		return nil, fmt.Errorf("holding the seats lock: %w", err)
	}

	return func() {
		_ = filelock.Unlock(file)
		_ = file.Close()
	}, nil
}

// Remove frees a seat, keeping what its holder left as the seat's last claim.
func Remove(root string, dir string, name string) error {
	if !ValidName(name) {
		return fmt.Errorf("%w, %q is not one", ErrBadName, name)
	}

	held, found := Read(dir, name)
	if found {
		err := writeClaim(root, LastPath(dir, name), held)
		if err != nil {
			return err
		}
	}

	err := os.Remove(Path(dir, name))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing the claim on %s: %w", name, err)
	}

	return nil
}

// Close frees a seat as Remove does, recording on its last claim when it closed.
func Close(root string, dir string, name string, at string) error {
	if !ValidName(name) {
		return fmt.Errorf("%w, %q is not one", ErrBadName, name)
	}

	held, found := Read(dir, name)
	if found {
		held.Closed = at

		err := writeClaim(root, LastPath(dir, name), held)
		if err != nil {
			return err
		}
	}

	err := os.Remove(Path(dir, name))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing the claim on %s: %w", name, err)
	}

	return nil
}

// Forget removes every file of a seat: its claim, its last claim and its slice.
func Forget(dir string, name string) error {
	if !ValidName(name) {
		return fmt.Errorf("%w, %q is not one", ErrBadName, name)
	}

	for _, path := range []string{Path(dir, name), LastPath(dir, name), filepath.Join(dir, name+".slice")} {
		err := os.Remove(path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("removing %s: %w", path, err)
		}
	}

	return nil
}

// Held is every seat a claim names a session for, whether that session still
// runs or not.
func Held(dir string) map[string]Claim {
	held := map[string]Claim{}

	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return held
	}

	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), ".json")
		if !ValidName(name) {
			continue
		}

		claim, found := Read(dir, name)
		if found && claim.SessionID != "" {
			held[name] = claim
		}
	}

	return held
}

// Known is every seat a store has seen: held now, or left by a holder.
func Known(dir string) []string {
	seen := map[string]bool{}

	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil
	}

	for _, path := range paths {
		name := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(path), ".json"), ".last")
		if ValidName(name) {
			seen[name] = true
		}
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}

	slices.Sort(names)

	return names
}

func Names(held map[string]Claim) []string {
	names := make([]string, 0, len(held))
	for name := range held {
		names = append(names, name)
	}

	slices.Sort(names)

	return names
}

// Owner is the nearest ancestor running the owner process, or the immediate
// parent where none does.
func Owner(ctx context.Context, processes Processes, ownerName string) int {
	owner := ownerAncestor(ctx, processes, ownerName)
	if owner == 0 {
		return processes.Parent()
	}

	return owner
}

// UnderOwner says whether the owner process is among the caller's ancestors,
// as it is for every call an agent's tool makes.
func UnderOwner(ctx context.Context, processes Processes, ownerName string) bool {
	return ownerAncestor(ctx, processes, ownerName) != 0
}

// ownerAncestor is the nearest ancestor running the owner process, zero where
// none does.
func ownerAncestor(ctx context.Context, processes Processes, ownerName string) int {
	parent, _ := processes.ParentAndName(ctx, processes.Self())
	for range ancestorDepth {
		if parent <= 1 {
			break
		}

		grandparent, name := processes.ParentAndName(ctx, parent)
		if name == ownerName {
			return parent
		}

		parent = grandparent
	}

	return 0
}

// Alive reports whether the claim's holder still runs: the same pid, still the
// owner process, started at the same instant, so a reused pid never passes.
//
// A claim written by an earlier release holds `ps` text in the claimer's
// locale. Text in another locale cannot be read back into an instant, so such
// a claim is judged by the process alone until its holder claims again.
func Alive(ctx context.Context, processes Processes, ownerName string, held Claim) bool {
	if held.PID <= 0 {
		return false
	}

	_, name := processes.ParentAndName(ctx, held.PID)
	if name == "" || name != ownerName {
		return false
	}

	recorded, readable := StartedAt(held.Started)
	if !readable {
		return true
	}

	current, found := processes.StartTime(ctx, held.PID)

	return found && current.Equal(recorded)
}

// StartedAt reads a claim's `started`: RFC 3339, as every claim is written now,
// or `ps` text in the C locale and the machine's zone, as earlier releases wrote
// it from a C shell.
func StartedAt(started string) (time.Time, bool) {
	at, err := time.Parse(time.RFC3339, started)
	if err == nil {
		return at, true
	}

	return process.ParseLstart(started, time.Local)
}

// FormatStarted is how a claim records its holder's start: RFC 3339 in UTC.
func FormatStarted(started time.Time) string {
	return started.UTC().Format(time.RFC3339)
}

// Of is the seat the calling process holds, or empty where it holds none.
func Of(ctx context.Context, dir string, processes Processes, ownerName string) (string, Claim) {
	owner := Owner(ctx, processes, ownerName)
	held := Held(dir)

	for _, name := range Names(held) {
		claim := held[name]
		if claim.PID == owner && Alive(ctx, processes, ownerName, claim) {
			return name, claim
		}
	}

	return "", Claim{}
}

// OfSession is the seat a session holds, or empty where it holds none.
func OfSession(dir string, session string) (string, Claim) {
	held := Held(dir)
	for _, name := range Names(held) {
		if held[name].SessionID == session {
			return name, held[name]
		}
	}

	return "", Claim{}
}
