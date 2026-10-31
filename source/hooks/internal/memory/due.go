package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
)

// A due time lifts a todo or a note in front of the agent once it nears: the
// brief lists it in its own section from a day before, a running session is
// told once when it comes due, and a week past it the entry is offered for
// retiring. It ranks nothing else: heat, search and recall ignore it.
const (
	dueNear    = 24 * time.Hour
	dueStale   = 7 * 24 * time.Hour
	dueShown   = 5
	dueHeading = "due"
	dueForms   = "RFC 3339 or a date (YYYY-MM-DD, its start in local time)"
)

var (
	ErrDueKind = errors.New("--due takes a todo or a note")
	ErrDueTime = errors.New("--due takes " + dueForms)
)

// dueArg is the `due` key, which `new` and `due` read as a time and `brief`
// as a switch listing every entry carrying one.
type dueArg struct {
	text   string
	listed bool
}

func (arg *dueArg) UnmarshalJSON(data []byte) error {
	var listed bool

	if json.Unmarshal(data, &listed) == nil {
		arg.listed = listed

		return nil
	}

	err := json.Unmarshal(data, &arg.text)
	if err != nil {
		return fmt.Errorf("due takes a time or true: %w", err)
	}

	return nil
}

func parseDue(text string) (time.Time, error) {
	at, err := parseEndTime(strings.TrimSpace(text))
	if err != nil {
		return time.Time{}, fmt.Errorf("%w, not %q", ErrDueTime, text)
	}

	return at, nil
}

func takesDue(kind string) bool {
	return kind == kindTodo || kind == kindNote
}

func duePositional(req *request) error {
	switch len(req.Positional) {
	case 1:
		req.ID = req.Positional[0]
	case 2:
		req.ID, req.Due.text = req.Positional[0], req.Positional[1]
	default:
		return errors.New("an entry id and a time expected, or an id and --clear")
	}

	return nil
}

// dueCommand moves an entry's due time, or clears it.
func (mem *memory) dueCommand(ctx context.Context, req request) result {
	given := strings.TrimSpace(req.Due.text)

	switch {
	case req.Clear && given != "":
		return refused("due takes a time or --clear, not both")
	case !req.Clear && given == "":
		return refused("due takes a time (%s) or --clear", dueForms)
	}

	var at time.Time

	if !req.Clear {
		parsed, err := parseDue(given)
		if err != nil {
			return refused("%v", err)
		}

		at = parsed.UTC()
	}

	_, _, failure, ok := mem.change(ctx, req.ID, true, func(_ *state, held *entry) error {
		if !takesDue(held.kind) {
			return fmt.Errorf("%s is a %s: %w", held.id, held.kind, ErrDueKind)
		}

		return nil
	}, memorylog.KindDue, "", duePayload{Due: at})
	if !ok {
		return failure
	}

	if at.IsZero() {
		return done("due cleared")
	}

	return done("due " + stamp(at))
}

// dueFlag is how a short line says when its entry comes due.
func dueFlag(held *entry, now time.Time) string {
	if held.due.After(now) {
		return "due in " + age(held.due.Sub(now))
	}

	return "due " + age(now.Sub(held.due)) + " ago"
}

// dueEntries is the seat's live entries carrying a due time, earliest first.
func (mem *memory) dueEntries(folded *state, seatName string) []*entry {
	var held []*entry

	for _, one := range folded.entryList() {
		if one.status == statusLive && !one.due.IsZero() && mem.onShelf(one, seatName) {
			held = append(held, one)
		}
	}

	slices.SortStableFunc(held, func(a, b *entry) int { return a.due.Compare(b.due) })

	return held
}

// dueSection is the brief's due lines: the entries already due or due within
// a day, earliest first and capped, then one line counting the rest. The ids
// it names as already due are returned, so the session is not told of them
// again.
func (mem *memory) dueSection(here tree, folded *state, seatName string, now time.Time) ([]string, []*entry) {
	var near, later []*entry

	for _, held := range mem.dueEntries(folded, seatName) {
		if held.due.Sub(now) <= dueNear {
			near = append(near, held)
		} else {
			later = append(later, held)
		}
	}

	var (
		lines []string
		past  []*entry
	)

	if len(near) > 0 {
		shown := near[:min(len(near), dueShown)]
		lines = append(lines, fmt.Sprintf("%s (%d of %d):", dueHeading, len(shown), len(near)))

		for _, held := range shown {
			lines = append(lines, "  "+shortLine(here, held, seatName, now, dueFlag(held, now)))

			if !held.due.After(now) {
				past = append(past, held)
			}
		}

		if more := len(near) - len(shown); more > 0 {
			lines = append(lines, runBefore(fmt.Sprintf("+%d more due: %s brief --due", more, self), "choosing what to do next"))
		}
	}

	if len(later) > 0 {
		lines = append(lines, runBefore(fmt.Sprintf("%d due later (next %s): %s brief --due", len(later), stamp(later[0].due), self),
			"planning ahead"))
	}

	return lines, past
}

// dueBrief is `brief --due`: every live entry of the seat carrying a due
// time, earliest first.
func (mem *memory) dueBrief(here tree, folded *state, seatName string) string {
	now := mem.now()

	var lines []string

	for _, held := range mem.dueEntries(folded, seatName) {
		lines = append(lines, mem.lineOf(held, shortLine(here, held, seatName, now, dueFlag(held, now))))
	}

	if len(lines) == 0 {
		return "no live entry of " + seatName + " is due"
	}

	return strings.Join(lines, "\n")
}

// dueKey names an entry at one due time: moving the time makes it news again.
func dueKey(held *entry) string {
	return held.id + "@" + held.due.UTC().Format(time.RFC3339)
}

// comeDue is the lines telling seatName of the entries that came due since it
// was last told, and marks them told; nothing where none did.
func (mem *memory) comeDue(ctx context.Context, folded *state, seatName string) string {
	now := mem.now()

	var mark seenMark
	mem.readSlot(mem.seenKey(seatName), &mark)

	var fresh []*entry

	for _, held := range mem.dueEntries(folded, seatName) {
		if !held.due.After(now) && !slices.Contains(mark.Due, dueKey(held)) {
			fresh = append(fresh, held)
		}
	}

	if len(fresh) == 0 {
		return ""
	}

	mem.markDue(folded, seatName, fresh)

	here := mem.checkout(ctx)
	shown := fresh[:min(len(fresh), dueShown)]
	lines := []string{fmt.Sprintf("%s: due for %s (%d):", self, seatName, len(fresh))}

	for _, held := range shown {
		lines = append(lines, "  "+shortLine(here, held, seatName, now, dueFlag(held, now)))
	}

	if more := len(fresh) - len(shown); more > 0 {
		lines = append(lines, runBefore(fmt.Sprintf("+%d more due: %s brief --due", more, self), "choosing what to do next"))
	}

	return strings.Join(lines, "\n")
}

// markDue records the entries as told. Of those told before, only the ones
// still due stay, so the list never outgrows the seat's due entries.
func (mem *memory) markDue(folded *state, seatName string, told []*entry) {
	if len(told) == 0 {
		return
	}

	var mark seenMark
	mem.readSlot(mem.seenKey(seatName), &mark)

	now := mem.now()
	still := map[string]bool{}

	for _, held := range mem.dueEntries(folded, seatName) {
		if !held.due.After(now) {
			still[dueKey(held)] = true
		}
	}

	kept := make([]string, 0, len(mark.Due)+len(told))

	for _, key := range mark.Due {
		if still[key] {
			kept = append(kept, key)
		}
	}

	for _, held := range told {
		if key := dueKey(held); !slices.Contains(kept, key) {
			kept = append(kept, key)
		}
	}

	mark.Due = kept
	_ = mem.writeSlot(mem.seenKey(seatName), mark)
}

// nextDue is the earliest due time of the seat's entries not yet come due, or
// zero: the moment a tool call has a reason to look again though the log did
// not change.
func (mem *memory) nextDue(folded *state, seatName string) time.Time {
	now := mem.now()

	for _, held := range mem.dueEntries(folded, seatName) {
		if held.due.After(now) {
			return held.due
		}
	}

	return time.Time{}
}
