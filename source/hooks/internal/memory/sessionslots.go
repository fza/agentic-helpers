package memory

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
	"github.com/fza/agentic-helpers/source/hooks/internal/space"
)

// Per-session state, and the per-seat state a session leaves its seat's next
// one, lives in the store's slot file in the cache, `sessions.slots`, keyed
// `<purpose>/<session or seat key>` (internal/slots).
const sessionSlotsName = "sessions.slots"

// sessionPurposes are the keys a session's own state goes under: what the
// housekeeping reclaims once nobody came back to it.
var sessionPurposes = []string{
	"shown/", "ladder/", "graph-seen/", "fork/", "waiter/", "migration-failed/", "instructions/", "agents/", "seen/agent/",
	"spawn/",
}

// seatPurposes are the keys a seat's own state goes under, `<purpose>/<seat
// id>`: what the housekeeping reclaims once the registry holds the seat no
// longer, as a temporary shelf's purge leaves it, or holds it expired. A
// store running a temporary seat per job otherwise keeps every job's keys.
var seatPurposes = []string{"traps/", "do-now/", "skipped/", "brief/", "seen/", "ledger-ended/", "channel/"}

// endedPurposes are the keys a session's own state goes under that nothing
// reads once it ends: SessionEnd clears them, beside its shown set.
var endedPurposes = []string{"ladder/", "waiter/", "graph-seen/", "fork/"}

func (mem *memory) slotFile() *slots.File {
	file := slots.Open(mem.env.Roots.Cache, filepath.Join(mem.cacheDir(), sessionSlotsName))
	file.Now = mem.now

	return file
}

// readSlot decodes key's value into target, answering whether one was there.
// Like the files it stands in for, a slot read takes no context: nothing it
// does waits.
func (mem *memory) readSlot(key string, target any) bool {
	return mem.slotFile().GetJSON(context.Background(), key, target)
}

// writeSlot sets key's value, fitted to its kind's ceiling. A hook whose
// session id was refused keeps no per-session state, so its empty key writes
// nothing; any other write that fails is reported.
func (mem *memory) writeSlot(key string, value slots.Fitted) {
	if key == "" {
		return
	}

	data, err := slots.Encode(value)
	if err == nil {
		err = mem.slotFile().Put(context.Background(), key, data)
	}

	if err == nil {
		mem.keepMarkOfCache()
	}

	mem.reportSlot("keeping", key, err)
}

func (mem *memory) hasSlot(key string) bool {
	_, found, err := mem.slotFile().Get(context.Background(), key)

	return err == nil && found
}

func (mem *memory) clearSlot(key string) {
	if key != "" {
		mem.reportSlot("clearing", key, mem.slotFile().Delete(context.Background(), key))
	}
}

// updateSlot changes key's value under the slot file's writer lock: change
// learns the value held and answers the one to keep, fitted to its kind, or
// slots.ErrUnchanged. A write that fails is reported.
func (mem *memory) updateSlot(ctx context.Context, key string, change func(value []byte, found bool) (slots.Fitted, error)) {
	if key == "" {
		return
	}

	err := mem.slotFile().Update(ctx, key, func(value []byte, found bool) ([]byte, error) {
		next, err := change(value, found)
		if err != nil {
			return nil, err
		}

		return slots.Encode(next)
	})
	mem.reportSlot("keeping", key, err)
}

// sessionKey is the key of a session's own state under purpose, empty in a
// hook whose payload's session id was refused: a slot refuses an empty key, so
// such a hook keeps no per-session state. No prefix match may use an empty key.
func (mem *memory) sessionKey(purpose string, session string, rest ...string) string {
	if mem.sessionless {
		return ""
	}

	return purpose + session + strings.Join(rest, "")
}

func (mem *memory) shownKey(session string) string { return mem.sessionKey("shown/", session) }

// agentShownKey is the shown set of one subagent of the session, which no
// read of its spawner's touches.
func (mem *memory) agentShownKey(session string, agentID string) string {
	return mem.sessionKey("shown/", session, "/agent/", agentID)
}

// spawnKey is what a fork spawned by the session's Agent call callID takes
// over at its first hook.
func (mem *memory) spawnKey(session string, callID string) string {
	return mem.sessionKey("spawn/", session, "/", callID)
}

func (mem *memory) ladderKey(session string) string { return mem.sessionKey("ladder/", session) }

func (mem *memory) forkKey(session string) string { return mem.sessionKey("fork/", session) }

func (mem *memory) waiterKey(session string) string { return mem.sessionKey("waiter/", session) }

func (mem *memory) graphMarkKey(session string) string { return mem.sessionKey("graph-seen/", session) }

func (mem *memory) trapIndexKey(seatName string) string { return "traps/" + mem.seatKey(seatName) }

func (mem *memory) doNowKey(seatName string) string { return "do-now/" + mem.seatKey(seatName) }

func (mem *memory) skippedKey(seatName string) string { return "skipped/" + mem.seatKey(seatName) }

func (mem *memory) briefMarkKey(seatName string) string { return "brief/" + mem.seatKey(seatName) }

func (mem *memory) seenKey(seatName string) string { return "seen/" + mem.seatKey(seatName) }

// reclaimSessions clears every session's own state stale names, and the
// tables of entry names of every month before the last: a session still in
// the last month at its turn keeps reading its table.
func (mem *memory) reclaimSessions(ctx context.Context, stale func(slots.Slot) bool) {
	now := mem.now().Local()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	kept := []string{now.Format("2006/01"), monthStart.AddDate(0, 0, -1).Format("2006/01")}

	registry, found := mem.registry()

	err := mem.slotFile().Reclaim(ctx, func(slot slots.Slot) bool {
		month, isNames := namesMonth(slot.Key)
		if isNames {
			return !slices.Contains(kept, month)
		}

		for _, purpose := range sessionPurposes {
			if strings.HasPrefix(slot.Key, purpose) {
				return stale(slot)
			}
		}

		for _, purpose := range seatPurposes {
			id, isSeat := strings.CutPrefix(slot.Key, purpose)
			if isSeat {
				return found && seat.IsID(id) && seatGone(registry, id)
			}
		}

		return false
	})
	mem.reportSlot("reclaiming", sessionSlotsName, err)
}

func seatGone(registry seat.Registry, id string) bool {
	held, found := registry.Seats[id]

	return !found || held.Expired
}

// clearEnded clears the state of the session nothing reads once it ended.
func (mem *memory) clearEnded(session string) {
	for _, purpose := range endedPurposes {
		mem.clearSlot(mem.sessionKey(purpose, session))
	}
}

// recordStore raises the store's clock mark and refreshes its size ledger
// once a write changed one of its files (internal/space). The store's own
// figures limit nothing; the ledger keeps what each of its files holds
// checkable without reading them.
func (mem *memory) recordStore(ctx context.Context) {
	if mem.key != "" && mem.storeExists() {
		_ = mem.keepMark()
		_ = space.Refresh(ctx, mem.env.Roots.Data, mem.store(), nil)
	}
}
