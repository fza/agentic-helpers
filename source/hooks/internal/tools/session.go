package tools

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var sessionShape = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// holding is one tool version a session took, and where its copy lies.
type holding struct {
	Project string     `json:"project"`
	Tool    string     `json:"tool"`
	Version int        `json:"version"`
	Copy    string     `json:"copy"`
	Files   []fileHash `json:"files"`
}

type nudge struct {
	Key  string `json:"key"`
	Line string `json:"line"`
}

// sessionState is what one session holds and has been told.
type sessionState struct {
	Holdings []holding `json:"holdings"`
	// Notified is the newest version a notice named, per holding key.
	Notified map[string]int `json:"notified"`
	// Pending are nudges stop found, delivered once; Nudged are their keys
	// once delivered.
	Pending []nudge  `json:"pending"`
	Nudged  []string `json:"nudged"`
	// Signature is what the notice hook saw last: the session file and the
	// indexes of the stores the holdings live in.
	Signature string `json:"signature"`
}

func (lib *library) sessionPath() string {
	return filepath.Join(lib.ownDir(), "sessions", lib.session+".json")
}

func (lib *library) loadSession() *sessionState {
	held := &sessionState{Notified: map[string]int{}}
	if !sessionShape.MatchString(lib.session) {
		return held
	}

	root, err := openDir(lib.ownDir())
	if err != nil {
		return held
	}

	defer root.close()

	sessions, err := root.sub("sessions")
	if err != nil {
		return held
	}

	defer sessions.close()

	data, err := sessions.read(lib.session+".json", sessionLimit)
	if err != nil {
		return held
	}

	_ = json.Unmarshal(data, held)

	if held.Notified == nil {
		held.Notified = map[string]int{}
	}

	return held
}

func (lib *library) saveSession(held *sessionState) error {
	if !sessionShape.MatchString(lib.session) {
		return fmt.Errorf("session %q names no session file", lib.session)
	}

	data, err := json.Marshal(held)
	if err != nil {
		return fmt.Errorf("encoding the session: %w", err)
	}

	sessions, err := ensureDir(lib.root(), lib.key+"/sessions")
	if err != nil {
		return fmt.Errorf("creating the sessions folder: %w", err)
	}

	defer sessions.close()

	return sessions.replace(lib.session+".json", data)
}

func holdingKey(project string, tool string) string { return project + "\x00" + tool }

func (held *sessionState) hold(next holding) {
	for index, existing := range held.Holdings {
		if existing.Project == next.Project && existing.Tool == next.Tool {
			held.Holdings[index] = next

			return
		}
	}

	held.Holdings = append(held.Holdings, next)
}

func (held *sessionState) addPending(next nudge) {
	if slices.Contains(held.Nudged, next.Key) || slices.ContainsFunc(held.Pending, func(existing nudge) bool {
		return existing.Key == next.Key
	}) {
		return
	}

	held.Pending = append(held.Pending, next)
}

// takeNudges is every nudge stop recorded and no prompt or call delivered yet,
// marked delivered.
func (lib *library) takeNudges() string {
	if lib.session == "" || lib.key == "" {
		return ""
	}

	held := lib.loadSession()
	if len(held.Pending) == 0 {
		return ""
	}

	lines := make([]string, 0, len(held.Pending))
	for _, pending := range held.Pending {
		lines = append(lines, pending.Line)
		held.Nudged = append(held.Nudged, pending.Key)
	}

	held.Pending = nil
	_ = lib.saveSession(held)

	return strings.Join(lines, "\n")
}
