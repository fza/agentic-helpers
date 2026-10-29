package memory

import (
	"cmp"
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
	"github.com/fza/agentic-helpers/source/hooks/internal/space"
)

// Per-session state, and the per-seat state a session leaves its seat's next
// one, lives in the store's slot file in the cache, `sessions.slots`, keyed
// `<purpose>/<session or seat key>` (internal/slots). sessionSlotWidth holds
// a month's capture names and a long session's shown set.
const (
	sessionSlotsName = "sessions.slots"
	sessionSlotWidth = 128 << 10
)

// sessionPurposes are the keys a session's own state goes under: what the
// housekeeping reclaims once nobody came back to it.
var sessionPurposes = []string{"shown/", "ladder/", "graph-seen/", "fork/", "waiter/", "migration-failed/"}

func (mem *memory) slotFile() *slots.File {
	file := slots.Open(filepath.Join(mem.cacheDir(), sessionSlotsName), sessionSlotWidth)
	file.Now = mem.now

	return file
}

// trimmer is a slot value that can give up its oldest marks to fit its slot.
type trimmer interface {
	trim() bool
}

// readSlot decodes key's value into target, answering whether one was there.
// Like the files it stands in for, a slot read takes no context: nothing it
// does waits.
func (mem *memory) readSlot(key string, target any) bool {
	return mem.slotFile().GetJSON(context.Background(), key, target)
}

// writeSlot sets key's value; a value too large for its slot drops its
// oldest marks until it fits, where it can.
func (mem *memory) writeSlot(key string, value any) error {
	for {
		err := mem.slotFile().PutJSON(context.Background(), key, value)

		trimmed, canTrim := value.(trimmer)
		if !errors.Is(err, slots.ErrTooLarge) || !canTrim || !trimmed.trim() {
			return err
		}
	}
}

func (mem *memory) hasSlot(key string) bool {
	_, found, err := mem.slotFile().Get(context.Background(), key)

	return err == nil && found
}

func (mem *memory) clearSlot(key string) {
	_ = mem.slotFile().Delete(context.Background(), key)
}

func (mem *memory) shownKey(session string) string { return "shown/" + session }

func (mem *memory) ladderKey(session string) string { return "ladder/" + session }

func (mem *memory) forkKey(session string) string { return "fork/" + session }

func (mem *memory) waiterKey(session string) string { return "waiter/" + session }

func (mem *memory) graphMarkKey(session string) string {
	return "graph-seen/" + cmp.Or(session, "no-session")
}

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
	kept := []string{
		graphNamesPrefix + now.Format("2006/01"),
		graphNamesPrefix + monthStart.AddDate(0, 0, -1).Format("2006/01"),
	}

	_ = mem.slotFile().Reclaim(ctx, func(slot slots.Slot) bool {
		if strings.HasPrefix(slot.Key, graphNamesPrefix) {
			return !slices.Contains(kept, slot.Key)
		}

		for _, purpose := range sessionPurposes {
			if strings.HasPrefix(slot.Key, purpose) {
				return stale(slot)
			}
		}

		return false
	})
}

// trim drops the older half of each mark list, answering whether anything
// was left to drop.
func (shown *shownSet) trim() bool {
	if len(shown.IDs)+len(shown.Bodies)+len(shown.Traps)+len(shown.Notes) == 0 {
		return false
	}

	shown.IDs = shown.IDs[len(shown.IDs)/2+len(shown.IDs)%2:]
	shown.Bodies = shown.Bodies[len(shown.Bodies)/2+len(shown.Bodies)%2:]
	shown.Traps = shown.Traps[len(shown.Traps)/2+len(shown.Traps)%2:]
	shown.Notes = shown.Notes[len(shown.Notes)/2+len(shown.Notes)%2:]

	return true
}

// recordStore refreshes the store's size ledger once a write changed one of
// its files (internal/space). The store's own figures limit nothing; the
// ledger keeps what each of its files holds checkable without reading them.
func (mem *memory) recordStore(ctx context.Context) {
	if mem.key != "" && mem.storeExists() {
		_ = space.Refresh(ctx, mem.store(), nil)
	}
}
