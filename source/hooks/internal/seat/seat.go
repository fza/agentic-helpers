// Package seat reads the seat claims the carry-forward keeps under
// `.memory/roles/`: one file per seat, naming the session holding it and the
// process that session runs in.
package seat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type Claim struct {
	SessionID  string `json:"session_id"`
	PID        int    `json:"pid"`
	Started    string `json:"started"`
	Claimed    string `json:"claimed,omitempty"`
	SeizedFrom string `json:"seized_from,omitempty"`
}

// Dir is where a checkout keeps its claims. root is the main checkout, so every
// worktree of one repository shares its seats.
func Dir(root string) string {
	return filepath.Join(root, ".memory", "roles")
}

func Path(dir string, seat string) string {
	return filepath.Join(dir, seat+".json")
}

func Read(dir string, seat string) (Claim, bool) {
	var held Claim

	data, err := os.ReadFile(Path(dir, seat))
	if err != nil {
		return Claim{}, false
	}

	err = json.Unmarshal(data, &held)
	if err != nil {
		return Claim{}, false
	}

	return held, true
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
		seat := strings.TrimSuffix(filepath.Base(path), ".json")

		claim, found := Read(dir, seat)
		if found && claim.SessionID != "" {
			held[seat] = claim
		}
	}

	return held
}

func Names(held map[string]Claim) []string {
	names := make([]string, 0, len(held))
	for name := range held {
		names = append(names, name)
	}

	slices.Sort(names)

	return names
}

// OfSession is the seat the session holds, or empty where it holds none.
func OfSession(dir string, session string) string {
	held := Held(dir)
	for _, name := range Names(held) {
		if held[name].SessionID == session {
			return name
		}
	}

	return ""
}
