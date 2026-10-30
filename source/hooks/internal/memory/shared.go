package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
)

// forAllSeats is the word `--for` takes for every seat, present and future,
// and so the one word no seat may be named.
const forAllSeats = "all"

// recipients is who an entry was filed for besides its filer: every seat, or
// the seats it names by key. It is written as "all" or as the list of keys.
type recipients struct {
	All   bool
	Seats []string
}

func (held recipients) MarshalJSON() ([]byte, error) {
	if held.All {
		return json.Marshal(forAllSeats)
	}

	return json.Marshal(held.Seats)
}

func (held *recipients) UnmarshalJSON(raw []byte) error {
	var word string

	err := json.Unmarshal(raw, &word)
	if err == nil {
		held.All = word == forAllSeats

		return nil
	}

	err = json.Unmarshal(raw, &held.Seats)
	if err != nil {
		return fmt.Errorf("reading who an entry is for: %w", err)
	}

	return nil
}

// recipientsFor turns the names a `--for` gave into the shelf ids an entry
// stores: a shelf name names that shelf, a seat name the seat's shelf. A name
// neither carries is refused, and the filer's own shelf is left out.
func (mem *memory) recipientsFor(names []string, filer string) (*recipients, error) {
	if len(names) == 0 {
		return nil, nil
	}

	if slices.Contains(names, forAllSeats) {
		return &recipients{All: true}, nil
	}

	held := &recipients{}
	shelves, _ := mem.shelves()
	own := mem.shelfOf(filer)

	for _, name := range names {
		shelf, found := shelves.IDOf(name)
		if !found {
			shelf = mem.shelfOf(name)
		}

		if shelf == "" {
			return nil, fmt.Errorf("--for names no seat or shelf called %s; %s who lists them", name, self)
		}

		if shelf != own && !slices.Contains(held.Seats, shelf) {
			held.Seats = append(held.Seats, shelf)
		}
	}

	return held, nil
}

// follows is every topic the shelf of seatName follows, each read through the
// topic merges.
func (mem *memory) follows(folded *state, seatName string) []string {
	shelves, found := mem.shelves()
	if !found {
		return nil
	}

	var topics []string
	for _, topic := range shelves.Shelves[mem.shelfOf(seatName)].Follows {
		topics = append(topics, folded.resolve(topic))
	}

	return topics
}

// receives is whether the entry reaches seatName from another shelf: filed for
// every seat, for seatName's shelf, or under a topic that shelf follows.
func (mem *memory) receives(folded *state, held *entry, seatName string) bool {
	mine := mem.shelfOf(seatName)
	if seatName == "" || mine == "" || mem.entryShelf(held) == mine {
		return false
	}

	if held.forAll || slices.ContainsFunc(held.forKeys, func(key string) bool { return mem.recipientShelf(key) == mine }) {
		return true
	}

	followed := mem.follows(folded, seatName)

	return slices.ContainsFunc(folded.topicsOf(held), func(topic string) bool { return slices.Contains(followed, topic) })
}

// ownOrReceived is whether the entry stands with seatName's own: on its shelf,
// or received from another.
func (mem *memory) ownOrReceived(folded *state, held *entry, seatName string) bool {
	return mem.onShelf(held, seatName) || mem.receives(folded, held, seatName)
}

// annotate names each entry's recipients the way its line shows them: every
// seat, the shelves it was filed for, or the followers of its topics where only
// a follow brings it to anyone.
func (mem *memory) annotate(folded *state) {
	shelves, found := mem.shelves()

	followed := map[string]bool{}

	if found {
		for _, id := range shelves.IDs() {
			for _, topic := range shelves.Shelves[id].Follows {
				followed[folded.resolve(topic)] = true
			}
		}
	}

	for _, held := range folded.entries {
		switch {
		case held.forAll:
			held.recipients = forAllSeats
		case len(held.forKeys) > 0:
			names := make([]string, 0, len(held.forKeys))
			for _, key := range held.forKeys {
				names = append(names, mem.shelfName(mem.recipientShelf(key)))
			}

			held.recipients = strings.Join(names, ", ")
		default:
			var topics []string

			for _, topic := range folded.topicsOf(held) {
				if followed[topic] {
					topics = append(topics, topic)
				}
			}

			held.recipients = ""
			if len(topics) > 0 {
				held.recipients = strings.Join(topics, ", ") + " followers"
			}
		}
	}
}

// setFollows changes what the shelf of the seat the calling session holds
// follows: every seat on it receives what it follows.
func (mem *memory) setFollows(ctx context.Context, req request, add bool) result {
	own, _ := mem.ownSeat(ctx)
	if own == "" {
		return refused("%s", ErrNoSeat)
	}

	var topics []string

	for _, topic := range req.Topics {
		topic = normalTopic(topic)
		if topic != "" {
			topics = append(topics, topic)
		}
	}

	if len(topics) == 0 {
		return refused("name at least one topic")
	}

	unlock, err := mem.lockStore(ctx)
	if err != nil {
		return refused("%v", err)
	}
	defer unlock()

	mem.forgetShelves()

	shelves, found := mem.shelves()
	shelf := mem.shelfOf(own)

	held, known := shelves.Shelves[shelf]
	if !found || !known {
		return refused("this store keeps no shelves yet, so %s cannot follow topics", own)
	}

	for _, topic := range topics {
		switch {
		case add && !slices.Contains(held.Follows, topic):
			held.Follows = append(held.Follows, topic)
		case !add:
			held.Follows = slices.DeleteFunc(held.Follows, func(followed string) bool { return followed == topic })
		}
	}

	slices.Sort(held.Follows)
	shelves.Shelves[shelf] = held

	err = shelves.Save(mem.env.Roots.Data)
	if err != nil {
		return refused("%v", err)
	}

	mem.forgetShelves()

	fields := followFields{Shelf: held.Name, Follows: orEmpty(held.Follows)}

	if len(held.Follows) == 0 {
		return done(held.Name + " follows no topic").with(fields)
	}

	return done(fmt.Sprintf("%s follows %s", held.Name, strings.Join(held.Follows, ", "))).with(fields)
}

func (mem *memory) follow(ctx context.Context, req request) result {
	return mem.setFollows(ctx, req, true)
}

func (mem *memory) unfollow(ctx context.Context, req request) result {
	return mem.setFollows(ctx, req, false)
}

// seenMark is when the newest entry a seat was told had arrived was filed,
// when the newest change to its shelf's entries it was told of was made, and
// how the log stood when the seat last looked.
type seenMark struct {
	At      time.Time `json:"at"`
	Changed time.Time `json:"changed,omitzero"`
	Log     string    `json:"log,omitempty"`
	// Traps is how many traps the seat held when the log stood at Log.
	Traps int `json:"traps,omitempty"`
	// Due is the entries the seat was told came due, each at the due time it
	// was told of; NextDue the earliest due time still ahead when the log
	// stood at Log.
	Due     []string  `json:"due,omitempty"`
	NextDue time.Time `json:"next_due,omitzero"`
}

// arrives is whether the entry is news for seatName: filed by a shelf mate, or
// received from another shelf.
func (mem *memory) arrives(folded *state, held *entry, seatName string) bool {
	if held.seat == seatName {
		return false
	}

	return mem.onShelf(held, seatName) || mem.receives(folded, held, seatName)
}

// arrivals is every entry seatName is told of that was filed after its seen
// mark, oldest first. A seat without a mark has seen everything filed so far.
func (mem *memory) arrivals(folded *state, seatName string) []*entry {
	var mark seenMark
	if !mem.readSlot(mem.seenKey(seatName), &mark) {
		mem.markSeen(seatName, mem.now())

		return nil
	}

	var arrived []*entry

	for _, held := range folded.entryList() {
		if held.status == statusLive && held.filed.After(mark.At) && mem.arrives(folded, held, seatName) {
			arrived = append(arrived, held)
		}
	}

	slices.SortStableFunc(arrived, func(a, b *entry) int { return a.filed.Compare(b.filed) })

	return arrived
}

// shelfEdits are the kinds of change any seat on a shelf may make to its
// entries, and so the ones its shelf mates are told of.
var shelfEdits = []memorylog.Kind{
	memorylog.KindRetire, memorylog.KindRetain, memorylog.KindRetitle, memorylog.KindTag, memorylog.KindUntag,
	memorylog.KindRekind, memorylog.KindAttach, memorylog.KindDue,
}

type shelfChange struct {
	held *entry
	change
}

// shelfChanges is every change a shelf mate made to an entry on seatName's
// shelf after its mark, oldest first. A mark from before shelves starts the
// count now.
func (mem *memory) shelfChanges(folded *state, seatName string) []shelfChange {
	var mark seenMark
	if !mem.readSlot(mem.seenKey(seatName), &mark) || mark.Changed.IsZero() {
		mark.Changed = mem.now()
		_ = mem.writeSlot(mem.seenKey(seatName), mark)

		return nil
	}

	var changed []shelfChange

	for _, held := range folded.entryList() {
		if !mem.onShelf(held, seatName) {
			continue
		}

		for _, other := range held.events {
			if other.seat != seatName && other.at.After(mark.Changed) && slices.Contains(shelfEdits, other.kind) {
				changed = append(changed, shelfChange{held: held, change: other})
			}
		}
	}

	slices.SortStableFunc(changed, func(a, b shelfChange) int { return a.at.Compare(b.at) })

	return changed
}

func (mem *memory) markSeen(seatName string, at time.Time) {
	var mark seenMark
	mem.readSlot(mem.seenKey(seatName), &mark)
	mark.At = at
	_ = mem.writeSlot(mem.seenKey(seatName), mark)
}

func (mem *memory) markChanged(seatName string, at time.Time) {
	var mark seenMark
	mem.readSlot(mem.seenKey(seatName), &mark)
	mark.Changed = at
	_ = mem.writeSlot(mem.seenKey(seatName), mark)
}

func (mem *memory) noteLog(seatName string, signature string, traps int, nextDue time.Time) {
	var mark seenMark
	mem.readSlot(mem.seenKey(seatName), &mark)
	mark.Log, mark.Traps, mark.NextDue = signature, traps, nextDue
	_ = mem.writeSlot(mem.seenKey(seatName), mark)
}

// announce is the lines telling seatName what arrived and what its shelf mates
// changed, and moves its mark past them; nothing where nothing did.
func (mem *memory) announce(ctx context.Context, folded *state, seatName string) string {
	arrived := mem.arrivals(folded, seatName)
	changed := mem.shelfChanges(folded, seatName)
	due := mem.comeDue(ctx, folded, seatName)

	if len(arrived) == 0 && len(changed) == 0 {
		return due
	}

	here := mem.checkout(ctx)

	var lines []string

	if len(arrived) > 0 {
		lines = append(lines, fmt.Sprintf("%s: new for %s (%d):", self, seatName, len(arrived)))

		for _, held := range arrived {
			lines = append(lines, "  "+line(here, held))
		}

		mem.markSeen(seatName, arrived[len(arrived)-1].filed)
	}

	if len(changed) > 0 {
		lines = append(lines, fmt.Sprintf("%s: changed on shelf %s (%d):", self, mem.shelfName(mem.shelfOf(seatName)),
			len(changed)))

		for _, other := range changed {
			lines = append(lines, fmt.Sprintf("  %s %s: %s by %s", other.held.id, other.held.title, other.text, other.seat))
		}

		mem.markChanged(seatName, changed[len(changed)-1].at)
	}

	if due != "" {
		lines = append(lines, due)
	}

	return strings.Join(lines, "\n")
}
