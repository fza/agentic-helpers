package memory

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
)

// An entry's kind says how fast it decays and where the brief shows it.
const (
	kindRule = "rule"
	kindTrap = "trap"
	kindTodo = "todo"
	kindNote = "note"
)

var entryKinds = []string{kindRule, kindTrap, kindTodo, kindNote}

const retentionReplaced = "--kind rule|trap|todo|note sets the decay (MCP key kind); retain <id>... <level> overrides it"

// kindLevels is the decay each kind sets; a retain override replaces it.
var kindLevels = map[string]string{
	kindRule: "standing",
	kindTrap: "long",
	kindTodo: "short",
	kindNote: "normal",
}

func levelOf(kind string) string {
	return cmp.Or(kindLevels[kind], kindLevels[kindNote])
}

// retainedOverride is the retain override in force where it differs from the
// decay the entry's kind sets, or nothing.
func (held *entry) retainedOverride() string {
	if held.override == "" || held.override == levelOf(held.kind) {
		return ""
	}

	return held.override
}

type rekindPayload struct {
	Kind string `json:"kind"`
}

// statePayload is a seat's state slot as a `state` row carries it: where the
// seat's work stands, overwritten by every write.
type statePayload struct {
	Body    string `json:"body"`
	Session string `json:"session,omitempty"`
	Branch  string `json:"branch,omitempty"`
	Commit  string `json:"commit,omitempty"`
	// Draft marks a state the Stop or SessionEnd hook wrote, not the agent.
	Draft bool `json:"draft,omitempty"`
}

type stateSlot struct {
	statePayload

	at time.Time
}

// kindRefusal says why kind cannot be given an entry filed from from, or
// nothing where it can.
func kindRefusal(kind string, from string) string {
	switch {
	case !slices.Contains(entryKinds, kind):
		return fmt.Sprintf("--kind takes %s, not %q", strings.Join(entryKinds, ", "), kind)
	case kind == kindRule && from != "user":
		return "a rule is the user's grant or instruction: --kind rule needs --from user"
	}

	return ""
}

func (mem *memory) rekind(ctx context.Context, req request) result {
	if !slices.Contains(entryKinds, req.Kind) {
		return refused("rekind takes %s, not %q", strings.Join(entryKinds, ", "), req.Kind)
	}

	_, _, failure, ok := mem.change(ctx, req.ID, true, func(_ *state, held *entry) error {
		if req.Kind == kindRule && held.from != "user" {
			return fmt.Errorf("%s was filed --from %s; only an entry filed --from user becomes a rule", held.id, held.from)
		}

		return nil
	}, memorylog.KindRekind, "", rekindPayload{Kind: req.Kind})
	if !ok {
		return failure
	}

	return done(fmt.Sprintf("%s is now a %s", req.ID, req.Kind))
}

// writeState overwrites the calling seat's state slot. The slot is a log row,
// never an entry: no id, no heat, no topics, outside search and recall.
func (mem *memory) writeState(ctx context.Context, req request) result {
	name, isSeated := mem.seated(ctx)
	if !isSeated {
		return refused("%s", ErrNoSeat)
	}

	body := strings.TrimSpace(req.Body)
	if body == "" {
		return refused("state needs a body on stdin: where the seat's work stands")
	}

	here := mem.checkout(ctx)

	raw, err := json.Marshal(statePayload{Body: body, Session: mem.env.SessionID, Branch: here.branch, Commit: here.commit})
	if err != nil {
		return refused("encoding the state: %v", err)
	}

	err = mem.appendEvents(ctx, memorylog.Event{Kind: memorylog.KindState, Time: mem.now(), Seat: name, Payload: raw})
	if err != nil {
		return refused("%v", err)
	}

	lines := []string{"state written"}

	limit := mem.options().EntryMaxWords
	if count := words(body); limit > 0 && count > limit {
		lines = append(lines, fmt.Sprintf("note: the state has %d words, past the %d it should hold (entry_max_words)",
			count, limit))
	}

	return done(strings.Join(lines, "\n"))
}

// stateLine is how the brief opens on the seat's state slot, its age measured
// now.
func (mem *memory) stateLine(folded *state, seatName string) string {
	slot, found := folded.states[seatName]
	if !found {
		return ""
	}

	where := age(mem.now().Sub(slot.at)) + " old"
	if slot.Branch != "" {
		where += ", branch " + slot.Branch
	}

	if slot.Draft {
		where += ", hook draft"
	}

	return fmt.Sprintf("state (%s): %s", where, slot.Body)
}

// age is a span in its largest whole unit: minutes, hours, days or weeks.
func age(span time.Duration) string {
	switch {
	case span >= 7*24*time.Hour:
		return fmt.Sprintf("%dw", int(span/(7*24*time.Hour)))
	case span >= 24*time.Hour:
		return fmt.Sprintf("%dd", int(span/(24*time.Hour)))
	case span >= time.Hour:
		return fmt.Sprintf("%dh", int(span/time.Hour))
	}

	return fmt.Sprintf("%dm", max(0, int(span/time.Minute)))
}
