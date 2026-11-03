package tools

import (
	"context"
	"encoding/json"
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

// sessionSlots is the own store's slot file in the cache: one slot per
// session, `session/<id>` (internal/slots).
func (lib *library) sessionSlots() *slots.File {
	file := slots.Open(lib.env.Roots.Cache, filepath.Join(lib.env.Roots.StoreCache(lib.key, home.Tools), "sessions.slots"))
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

// The session state's ceiling and caps, in encoded JSON bytes unless they
// count items.
const (
	sessionCeiling  = 110_000
	copyPathBytes   = 1_024
	maxHoldings     = 50
	maxPending      = 20
	pendingBytes    = 512
	pendingKeyBytes = 2_048
)

// saveSession writes the session's state, fitted to its ceiling; a session id
// outside the shape keeps none, and a write that fails is reported.
func (lib *library) saveSession(ctx context.Context, held *sessionState) {
	if !sessionShape.MatchString(lib.session) || lib.key == "" {
		return
	}

	data, err := held.fit()
	if err == nil {
		err = lib.sessionSlots().Put(ctx, lib.sessionKey(), data)
	}

	lib.reportSlot("keeping", lib.sessionKey(), err)
}

// fit is the state's JSON once its caps hold: a holding whose copy path passes
// its cap is left out and the oldest past maxHoldings go; a pending nudge
// whose key passes its cap is left out, the oldest past maxPending go, and
// each line is cut to its bytes. Past the ceiling it gives up its oldest
// delivered nudges, then the versions told of that no holding names, its
// pending nudges, and its oldest holdings, down to none at all.
func (held *sessionState) fit() ([]byte, error) {
	held.Holdings = slices.DeleteFunc(held.Holdings, func(holding holding) bool {
		return slots.EncodedLen(holding.Copy) > copyPathBytes
	})
	held.Holdings = held.Holdings[max(0, len(held.Holdings)-maxHoldings):]
	held.Pending = slices.DeleteFunc(held.Pending, func(pending nudge) bool {
		return slots.EncodedLen(pending.Key) > pendingKeyBytes
	})
	held.Pending = held.Pending[max(0, len(held.Pending)-maxPending):]

	for index, pending := range held.Pending {
		held.Pending[index].Line = slots.CutJSON(pending.Line, pendingBytes)
	}

	for {
		data, err := json.Marshal(held)
		if err != nil {
			return nil, fmt.Errorf("encoding the session: %w", err)
		}

		if len(data) <= sessionCeiling {
			return data, nil
		}

		switch {
		case len(held.Nudged) > 0:
			held.Nudged = slots.NewerHalf(held.Nudged)
		case held.dropStaleNotified():
		case len(held.Pending) > 0:
			held.Pending = nil
		case len(held.Holdings) > 0:
			held.Holdings = slots.NewerHalf(held.Holdings)
		default:
			return nil, fmt.Errorf("%w: %d bytes", slots.ErrOverCeiling, len(data))
		}
	}
}

// dropStaleNotified forgets the versions told of that no holding names,
// answering whether there were any.
func (held *sessionState) dropStaleNotified() bool {
	dropped := false

	for key := range held.Notified {
		if !slices.ContainsFunc(held.Holdings, func(holding holding) bool { return holdingKey(holding.Project, holding.Tool) == key }) {
			delete(held.Notified, key)

			dropped = true
		}
	}

	return dropped
}

func (lib *library) forgetSession(ctx context.Context) {
	if sessionShape.MatchString(lib.session) && lib.key != "" {
		lib.reportSlot("clearing", lib.sessionKey(), lib.sessionSlots().Delete(ctx, lib.sessionKey()))
	}
}

// reportSlot reports a slot write that failed on stderr, which the agent
// never reads: a lost write costs session state, never the call.
func (lib *library) reportSlot(action string, key string, err error) {
	if err != nil {
		_, _ = fmt.Fprintf(lib.streams.Err, "%s: %s %s: %v\n", self, action, key, err)
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
	lib.saveSession(ctx, held)

	return strings.Join(lines, "\n")
}
