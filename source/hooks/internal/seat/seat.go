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
)

// How far up the process tree to look for the owner. A hook runs through a
// shell, so the immediate parent is not reliably the Claude process.
const ancestorDepth = 12

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,23}$`)

// ErrBadName refuses a seat name that is no plain path segment, so no caller
// can read, write or remove a file outside the seats directory.
var ErrBadName = errors.New("a seat name matches ^[a-z][a-z0-9-]{0,23}$")

type Claim struct {
	SessionID      string `json:"session_id"`
	PID            int    `json:"pid"`
	Started        string `json:"started"`
	Claimed        string `json:"claimed,omitempty"`
	SeizedFrom     string `json:"seized_from,omitempty"`
	TranscriptPath string `json:"transcript_path,omitempty"`
}

type Processes interface {
	Self() int
	Parent() int
	ParentAndName(ctx context.Context, pid int) (int, string)
	StartTime(ctx context.Context, pid int) string
}

func ValidName(name string) bool {
	return namePattern.MatchString(name)
}

// StoreRoot holds every project's memory store on the machine, whatever
// config directory a session runs under.
func StoreRoot(home string) string {
	return filepath.Join(home, ".claude", "agentic-memory")
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

func Write(dir string, name string, held Claim) error {
	if !ValidName(name) {
		return fmt.Errorf("%w, %q does not", ErrBadName, name)
	}

	return writeClaim(Path(dir, name), held)
}

func writeClaim(path string, held Claim) error {
	data, err := json.MarshalIndent(held, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the claim: %w", err)
	}

	err = os.MkdirAll(filepath.Dir(path), 0o700)
	if err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}

	temp := path + ".tmp"

	err = os.WriteFile(temp, append(data, '\n'), 0o600)
	if err != nil {
		return fmt.Errorf("writing %s: %w", temp, err)
	}

	err = os.Rename(temp, path)
	if err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}

	return nil
}

// Remove frees a seat, keeping what its holder left as the seat's last claim.
func Remove(dir string, name string) error {
	if !ValidName(name) {
		return fmt.Errorf("%w, %q does not", ErrBadName, name)
	}

	held, found := Read(dir, name)
	if found {
		err := writeClaim(LastPath(dir, name), held)
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
	pid := processes.Self()
	for range ancestorDepth {
		parent, _ := processes.ParentAndName(ctx, pid)
		if parent <= 1 {
			break
		}

		_, name := processes.ParentAndName(ctx, parent)
		if name == ownerName {
			return parent
		}

		pid = parent
	}

	return processes.Parent()
}

// Alive reports whether the claim's holder still runs: the same pid, still the
// owner process, started at the same time, so a reused pid never passes.
func Alive(ctx context.Context, processes Processes, ownerName string, held Claim) bool {
	if held.PID <= 0 {
		return false
	}

	_, name := processes.ParentAndName(ctx, held.PID)
	if name == "" || name != ownerName {
		return false
	}

	return held.Started == "" || processes.StartTime(ctx, held.PID) == held.Started
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
