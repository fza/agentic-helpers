package carryforward

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// A role is a path segment and a file-name stem.
var rolePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,23}$`)

// How far up the process tree to look for the owner. A hook runs through a
// shell, so the immediate parent is not reliably the Claude process.
const ancestorDepth = 12

type ProcessTable interface {
	Self() int
	Parent() int
	ParentAndName(ctx context.Context, pid int) (int, string)
	StartTime(ctx context.Context, pid int) string
}

type lock struct {
	SessionID  string `json:"session_id"`
	PID        int    `json:"pid"`
	Started    string `json:"started"`
	Claimed    string `json:"claimed,omitempty"`
	SeizedFrom string `json:"seized_from,omitempty"`
}

func validRole(role string) bool {
	return rolePattern.MatchString(role)
}

func (hook *hook) lockPath(role string) string {
	return filepath.Join(hook.rolesDir(), role+".json")
}

func (hook *hook) readLock(role string) (lock, bool) {
	var held lock

	data, err := os.ReadFile(hook.lockPath(role))
	if err != nil {
		return lock{}, false
	}

	err = json.Unmarshal(data, &held)
	if err != nil {
		return lock{}, false
	}

	return held, true
}

func (hook *hook) writeLock(role string, held lock) error {
	return writeJSON(hook.lockPath(role), held)
}

// holders is every seat a lock file claims, whether its holder still runs or not.
func (hook *hook) holders() map[string]lock {
	held := map[string]lock{}

	paths, err := filepath.Glob(filepath.Join(hook.rolesDir(), "*.json"))
	if err != nil {
		return held
	}

	for _, path := range paths {
		role := strings.TrimSuffix(filepath.Base(path), ".json")

		record, found := hook.readLock(role)
		if found && record.SessionID != "" {
			held[role] = record
		}
	}

	return held
}

func sortedRoles(held map[string]lock) []string {
	roles := make([]string, 0, len(held))
	for role := range held {
		roles = append(roles, role)
	}

	slices.Sort(roles)

	return roles
}

func (hook *hook) roleOfSession(session string) string {
	held := hook.holders()
	for _, role := range sortedRoles(held) {
		if held[role].SessionID == session {
			return role
		}
	}

	return ""
}

func (hook *hook) roleOfThisProcess(ctx context.Context) string {
	pid := hook.ownerPID(ctx)

	held := hook.holders()
	for _, role := range sortedRoles(held) {
		if held[role].PID == pid {
			return role
		}
	}

	return ""
}

// roleUsage lists the seats held now, the ones a release or a refresh can name.
func (hook *hook) roleUsage() string {
	roles := sortedRoles(hook.holders())
	if len(roles) == 0 {
		return "none held"
	}

	return strings.Join(roles, "|")
}

func (hook *hook) isOwner(ctx context.Context, pid int) bool {
	_, name := hook.env.Processes.ParentAndName(ctx, pid)

	return name != "" && name == hook.env.OwnerName
}

func (hook *hook) ownerAlive(ctx context.Context, pid int, started string) bool {
	if pid <= 0 || !hook.isOwner(ctx, pid) {
		return false
	}

	return started == "" || hook.env.Processes.StartTime(ctx, pid) == started
}

// ownerPID is the nearest ancestor running the owner process.
func (hook *hook) ownerPID(ctx context.Context) int {
	pid := hook.env.Processes.Self()
	for range ancestorDepth {
		parent, _ := hook.env.Processes.ParentAndName(ctx, pid)
		if parent <= 1 {
			break
		}

		_, name := hook.env.Processes.ParentAndName(ctx, parent)
		if name == hook.env.OwnerName {
			return parent
		}

		pid = parent
	}

	return hook.env.Processes.Parent()
}

// A session cannot name its own id from inside a command, so the session-start
// payload records it against the process it belongs to, and a claim reads it
// back.
func (hook *hook) sessionsPath() string {
	return filepath.Join(hook.memoryDir(), "sessions.json")
}

func (hook *hook) readSessions() map[string]string {
	known := map[string]string{}

	data, err := os.ReadFile(hook.sessionsPath())
	if err != nil {
		return known
	}

	err = json.Unmarshal(data, &known)
	if err != nil || known == nil {
		return map[string]string{}
	}

	return known
}

func (hook *hook) rememberSession(ctx context.Context, session string) error {
	own := strconv.Itoa(hook.ownerPID(ctx))

	known := hook.readSessions()
	known[own] = session

	for held := range known {
		pid, err := strconv.Atoi(held)
		if held != own && (err != nil || !hook.isOwner(ctx, pid)) {
			delete(known, held)
		}
	}

	return writeJSON(hook.sessionsPath(), known)
}

func (hook *hook) sessionOfThisProcess(ctx context.Context) string {
	return hook.readSessions()[strconv.Itoa(hook.ownerPID(ctx))]
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s: %w", filepath.Base(path), err)
	}

	return writeAtomic(path, append(data, '\n'))
}

func writeAtomic(path string, data []byte) error {
	temporary := fmt.Sprintf("%s.tmp%d", path, os.Getpid())

	err := os.WriteFile(temporary, data, 0o644)
	if err != nil {
		return fmt.Errorf("writing %s: %w", filepath.Base(path), err)
	}

	err = os.Rename(temporary, path)
	if err != nil {
		return fmt.Errorf("replacing %s: %w", filepath.Base(path), err)
	}

	return nil
}
