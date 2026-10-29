package tools

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
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

// sessionSlotWidth holds a session's holdings, each with its files' hashes.
const sessionSlotWidth = 128 << 10

// sessionSlots is the own store's slot file in the cache: one slot per
// session, `session/<id>` (internal/slots).
func (lib *library) sessionSlots() *slots.File {
	file := slots.Open(filepath.Join(lib.env.Roots.StoreCache(lib.key, home.Tools), "sessions.slots"), sessionSlotWidth)
	file.Now = lib.now

	return file
}

func (lib *library) sessionKey() string { return "session/" + lib.session }

func (lib *library) loadSession(ctx context.Context) *sessionState {
	held := &sessionState{Notified: map[string]int{}}
	if !sessionShape.MatchString(lib.session) || lib.key == "" {
		return held
	}

	lib.sessionSlots().GetJSON(ctx, lib.sessionKey(), held)

	if held.Notified == nil {
		held.Notified = map[string]int{}
	}

	return held
}

// saveSession writes the session's state; one too large for its slot gives
// up its oldest delivered nudges, then its oldest holdings.
func (lib *library) saveSession(ctx context.Context, held *sessionState) error {
	if !sessionShape.MatchString(lib.session) || lib.key == "" {
		return fmt.Errorf("session %q names no session slot", lib.session)
	}

	for {
		err := lib.sessionSlots().PutJSON(ctx, lib.sessionKey(), held)
		if !errors.Is(err, slots.ErrTooLarge) {
			return err
		}

		switch {
		case len(held.Nudged) > 0:
			held.Nudged = held.Nudged[len(held.Nudged)/2+len(held.Nudged)%2:]
		case len(held.Holdings) > 0:
			held.Holdings = held.Holdings[len(held.Holdings)/2+len(held.Holdings)%2:]
		default:
			return err
		}
	}
}

func (lib *library) forgetSession(ctx context.Context) {
	if sessionShape.MatchString(lib.session) && lib.key != "" {
		_ = lib.sessionSlots().Delete(ctx, lib.sessionKey())
	}
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
func (lib *library) takeNudges(ctx context.Context) string {
	if lib.session == "" || lib.key == "" {
		return ""
	}

	held := lib.loadSession(ctx)
	if len(held.Pending) == 0 {
		return ""
	}

	lines := make([]string, 0, len(held.Pending))
	for _, pending := range held.Pending {
		lines = append(lines, pending.Line)
		held.Nudged = append(held.Nudged, pending.Key)
	}

	held.Pending = nil
	_ = lib.saveSession(ctx, held)

	return strings.Join(lines, "\n")
}
